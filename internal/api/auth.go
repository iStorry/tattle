package api

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Signing in works like this:
//
//   - Until a password exists, the dashboard shows first-time setup. Setting
//     the password needs a setup code that is only written to the service
//     log, so it takes access to the host, not just to the network.
//   - Signing in gives the browser a random session token in a cookie that
//     scripts cannot read. Only a hash of the token is stored.
//   - Wrong guesses are limited per address and overall.

const (
	sessionCookie = "tattle_session"
	sessionLength = 30 * 24 * time.Hour

	minPassword = 8
	maxPassword = 256

	failWindow    = 10 * time.Minute
	maxFailsPerIP = 8
	maxFailsTotal = 300
)

// hashRounds is the PBKDF2 work factor (OWASP's 2023 figure for
// HMAC-SHA256). Tests lower it to stay fast.
var hashRounds = 600_000

// hashPassword returns "pbkdf2-sha256$rounds$salt$key".
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, hashRounds, 32)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", hashRounds, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

func checkPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	rounds, err := strconv.Atoi(parts[1])
	if err != nil || rounds < 1 || rounds > 10_000_000 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, rounds, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

func validPassword(p string) error {
	switch {
	case len(p) < minPassword:
		return fmt.Errorf("the password needs at least %d characters", minPassword)
	case len(p) > maxPassword:
		return errors.New("the password is too long")
	}
	return nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// setupAlphabet leaves out characters that are easy to confuse.
const setupAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newSetupCode() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	code := make([]byte, 0, 9)
	for i, b := range raw {
		if i == 4 {
			code = append(code, '-')
		}
		code = append(code, setupAlphabet[int(b)%len(setupAlphabet)])
	}
	return string(code)
}

// normalizeCode lets the code be typed in any case, with or without the dash.
func normalizeCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.NewReplacer("-", "", " ", "").Replace(s)
}

// limiter counts wrong guesses so the password cannot be found by trying
// many. It also keeps password hashing, which is deliberately slow, from
// being used to tie up the host.
type limiter struct {
	mu     sync.Mutex
	perIP  map[string]*failures
	global failures
}

type failures struct {
	count int
	since time.Time
}

func (f *failures) current(now time.Time) int {
	if now.Sub(f.since) > failWindow {
		f.count, f.since = 0, now
	}
	return f.count
}

// allow reports whether another attempt may be made from ip, and if not,
// how long to wait.
func (l *limiter) allow(ip string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.global.current(now) >= maxFailsTotal {
		return false, failWindow - now.Sub(l.global.since)
	}
	if f := l.perIP[ip]; f != nil && f.current(now) >= maxFailsPerIP {
		return false, failWindow - now.Sub(f.since)
	}
	return true, 0
}

func (l *limiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.perIP == nil || len(l.perIP) > 10_000 {
		l.perIP = map[string]*failures{}
	}
	f := l.perIP[ip]
	if f == nil {
		f = &failures{since: now}
		l.perIP[ip] = f
	}
	f.current(now)
	f.count++
	l.global.current(now)
	l.global.count++
}

func (l *limiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.perIP, ip)
}

// auth holds the sign-in state that is not in the database.
type auth struct {
	enabled bool // false in demo mode, which has nothing to protect
	limit   limiter

	mu        sync.Mutex
	setupCode string
}

// currentSetupCode returns the code for first-time setup, creating and
// logging it the first time it is needed.
func (s *Server) currentSetupCode() string {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	if s.auth.setupCode == "" {
		s.auth.setupCode = newSetupCode()
		log.Printf("no password is set yet: open the dashboard and enter setup code %s", s.auth.setupCode)
	}
	return s.auth.setupCode
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// signedIn reports whether the request carries a valid session.
func (s *Server) signedIn(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	ok, err := s.store.SessionValid(tokenHash(c.Value), time.Now())
	if err != nil {
		log.Printf("api: checking session: %v", err)
	}
	return ok
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(sessionLength)
	if err := s.store.AddSession(tokenHash(token), expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		// Only over HTTPS when the dashboard is reached over HTTPS.
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	return nil
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// readJSON decodes a request body. Browsers will not send JSON to another
// site without asking first, so insisting on it stops other web pages from
// making requests on a signed-in user's behalf.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "the request must be sent as JSON", http.StatusUnsupportedMediaType)
		return false
	}
	body := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	body.DisallowUnknownFields()
	if err := body.Decode(v); err != nil {
		http.Error(w, "the request could not be read", http.StatusBadRequest)
		return false
	}
	return true
}

func fail(w http.ResponseWriter, what string, err error) {
	log.Printf("api: %s: %v", what, err)
	http.Error(w, "something went wrong; the host's log has the details", http.StatusInternalServerError)
}

// tooMany answers a request that is over the attempt limit.
func (s *Server) tooMany(w http.ResponseWriter, r *http.Request) bool {
	ok, wait := s.auth.limit.allow(clientIP(r), time.Now())
	if ok {
		return false
	}
	minutes := int(wait.Minutes()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	http.Error(w, fmt.Sprintf("too many wrong attempts; try again in %d minutes", minutes), http.StatusTooManyRequests)
	return true
}

// handleAuth tells the dashboard which page to show.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if !s.auth.enabled {
		writeJSON(w, map[string]bool{"enabled": false, "setup": false, "signedIn": true}, nil)
		return
	}
	hash, err := s.store.PasswordHash()
	if err != nil {
		fail(w, "reading password", err)
		return
	}
	if hash == "" {
		s.currentSetupCode() // make sure the code is in the log before it is asked for
	}
	writeJSON(w, map[string]bool{"enabled": true, "setup": hash == "", "signedIn": hash != "" && s.signedIn(r)}, nil)
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req struct{ Code, Password string }
	if s.tooMany(w, r) || !readJSON(w, r, &req) {
		return
	}
	hash, err := s.store.PasswordHash()
	if err != nil {
		fail(w, "reading password", err)
		return
	}
	if hash != "" {
		http.Error(w, "a password has already been set", http.StatusConflict)
		return
	}
	want := normalizeCode(s.currentSetupCode())
	if subtle.ConstantTimeCompare([]byte(normalizeCode(req.Code)), []byte(want)) != 1 {
		s.auth.limit.fail(clientIP(r), time.Now())
		http.Error(w, "that setup code is not right; check the host's log for the current one", http.StatusForbidden)
		return
	}
	if err := validPassword(req.Password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.setPassword(w, r, req.Password) {
		return
	}
	s.auth.mu.Lock()
	s.auth.setupCode = ""
	s.auth.mu.Unlock()
	log.Printf("the dashboard password was set from %s", clientIP(r))
}

// setPassword stores a new password and signs the caller in again, since
// storing it signs everyone out.
func (s *Server) setPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	hash, err := hashPassword(password)
	if err == nil {
		err = s.store.SetPasswordHash(hash)
	}
	if err == nil {
		err = s.startSession(w, r)
	}
	if err != nil {
		fail(w, "setting password", err)
		return false
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	if s.tooMany(w, r) || !readJSON(w, r, &req) {
		return
	}
	hash, err := s.store.PasswordHash()
	if err != nil {
		fail(w, "reading password", err)
		return
	}
	if hash == "" {
		http.Error(w, "no password has been set yet", http.StatusConflict)
		return
	}
	if len(req.Password) > maxPassword || !checkPassword(hash, req.Password) {
		s.auth.limit.fail(clientIP(r), time.Now())
		http.Error(w, "that password is not right", http.StatusUnauthorized)
		return
	}
	s.auth.limit.reset(clientIP(r))
	if err := s.startSession(w, r); err != nil {
		fail(w, "starting session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "the request must be sent as JSON", http.StatusUnsupportedMediaType)
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.DeleteSession(tokenHash(c.Value)); err != nil {
			log.Printf("api: ending session: %v", err)
		}
	}
	clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

// handleChangePassword is only reachable when signed in.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct{ Current, New string }
	if s.tooMany(w, r) || !readJSON(w, r, &req) {
		return
	}
	hash, err := s.store.PasswordHash()
	if err != nil {
		fail(w, "reading password", err)
		return
	}
	if len(req.Current) > maxPassword || !checkPassword(hash, req.Current) {
		s.auth.limit.fail(clientIP(r), time.Now())
		http.Error(w, "the current password is not right", http.StatusForbidden)
		return
	}
	if err := validPassword(req.New); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.setPassword(w, r, req.New) {
		log.Printf("the dashboard password was changed from %s", clientIP(r))
	}
}
