package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/istorry/tattle/internal/store"
)

func init() { hashRounds = 1000 } // keep password hashing fast in tests

type client struct {
	t      *testing.T
	srv    *Server
	cookie *http.Cookie
	ip     string
}

func newServer(t *testing.T, requireLogin bool) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ui := fstest.MapFS{"index.html": {Data: []byte("<title>Tattle</title>")}}
	return New(st, &Status{Started: time.Now()}, ui, requireLogin), st
}

// do sends a request and returns the status and body, keeping any session
// cookie the server sets or clears.
func (c *client) do(method, path, body string) (int, string) {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.ip != "" {
		req.RemoteAddr = c.ip + ":50000"
	}
	rec := httptest.NewRecorder()
	c.srv.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == sessionCookie {
			c.cookie = ck
			if ck.MaxAge < 0 {
				c.cookie = nil
			}
		}
	}
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func (c *client) want(method, path, body string, status int) string {
	c.t.Helper()
	got, text := c.do(method, path, body)
	if got != status {
		c.t.Fatalf("%s %s: status %d, want %d (%s)", method, path, got, status, text)
	}
	return text
}

// setUp completes first-time setup and leaves the client signed in.
func setUp(t *testing.T, password string) (*client, *Server, *store.Store) {
	t.Helper()
	srv, st := newServer(t, true)
	c := &client{t: t, srv: srv}
	c.want("GET", "/api/auth", "", 200)
	c.want("POST", "/api/auth/setup", `{"code":"`+srv.auth.setupCode+`","password":"`+password+`"}`, 204)
	return c, srv, st
}

func TestNothingButSignInIsReachableWithoutASession(t *testing.T) {
	srv, _ := newServer(t, true)
	c := &client{t: t, srv: srv}
	for _, path := range []string{
		"/api/status", "/api/machines", "/api/timeline", "/api/peers", "/api/activity",
		"/api/split", "/api/settings", "/api/does-not-exist",
	} {
		c.want("GET", path, "", 401)
	}
	c.want("PUT", "/api/settings", `{"show":"all","localNets":[],"privateIsLocal":true}`, 401)
	c.want("POST", "/api/auth/password", `{"current":"x","new":"long enough password"}`, 401)

	// The page itself is served, because it is what shows the sign-in form.
	if body := c.want("GET", "/", "", 200); !strings.Contains(body, "Tattle") {
		t.Errorf("index = %q", body)
	}
	if body := c.want("GET", "/api/auth", "", 200); body != `{"enabled":true,"setup":true,"signedIn":false}` {
		t.Errorf("auth = %s", body)
	}
}

func TestFirstTimeSetup(t *testing.T) {
	srv, _ := newServer(t, true)
	c := &client{t: t, srv: srv}
	c.want("GET", "/api/auth", "", 200)
	code := srv.auth.setupCode
	if len(code) != 9 || code[4] != '-' {
		t.Fatalf("setup code = %q", code)
	}

	c.want("POST", "/api/auth/setup", `{"code":"WRONG-CODE","password":"correct horse"}`, 403)
	if text := c.want("POST", "/api/auth/setup", `{"code":"`+code+`","password":"short"}`, 400); !strings.Contains(text, "at least 8") {
		t.Errorf("short password message = %q", text)
	}
	c.want("GET", "/api/status", "", 401)

	// The code may be typed in lower case without the dash.
	typed := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	c.want("POST", "/api/auth/setup", `{"code":"`+typed+`","password":"correct horse"}`, 204)
	if c.cookie == nil || !c.cookie.HttpOnly || c.cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", c.cookie)
	}
	c.want("GET", "/api/status", "", 200)
	if body := c.want("GET", "/api/auth", "", 200); body != `{"enabled":true,"setup":false,"signedIn":true}` {
		t.Errorf("auth = %s", body)
	}

	// Setup cannot be run a second time, even with the old code.
	other := &client{t: t, srv: srv}
	other.want("POST", "/api/auth/setup", `{"code":"`+code+`","password":"another password"}`, 409)
}

func TestLoginAndLogout(t *testing.T) {
	c, srv, st := setUp(t, "correct horse")
	c.want("POST", "/api/auth/logout", `{}`, 204)
	c.want("GET", "/api/status", "", 401)

	c.want("POST", "/api/auth/login", `{"password":"wrong horse"}`, 401)
	c.want("GET", "/api/status", "", 401)
	c.want("POST", "/api/auth/login", `{"password":"correct horse"}`, 204)
	c.want("GET", "/api/status", "", 200)

	// Another site cannot submit a form to these endpoints.
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader("password=correct+horse"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("form post: status %d", rec.Code)
	}

	// The token is not stored as-is, and an expired session stops working.
	if ok, _ := st.SessionValid(c.cookie.Value, time.Now()); ok {
		t.Error("the raw token is usable as a stored session")
	}
	if ok, _ := st.SessionValid(tokenHash(c.cookie.Value), time.Now().Add(sessionLength+time.Hour)); ok {
		t.Error("session is still valid after it should have expired")
	}
	if err := st.PruneSessions(time.Now().Add(sessionLength + time.Hour)); err != nil {
		t.Fatal(err)
	}
	c.want("GET", "/api/status", "", 401)
}

func TestWrongGuessesAreLimited(t *testing.T) {
	c, srv, _ := setUp(t, "correct horse")
	attacker := &client{t: t, srv: srv, ip: "203.0.113.9"}
	for i := 0; i < maxFailsPerIP; i++ {
		attacker.want("POST", "/api/auth/login", `{"password":"guess"}`, 401)
	}
	// Locked out now, even with the right password.
	if text := attacker.want("POST", "/api/auth/login", `{"password":"correct horse"}`, 429); !strings.Contains(text, "try again in") {
		t.Errorf("message = %q", text)
	}
	// Someone at another address is not affected.
	owner := &client{t: t, srv: srv, ip: "192.168.1.42"}
	owner.want("POST", "/api/auth/login", `{"password":"correct horse"}`, 204)
	_ = c

	// Guessing the setup code is limited the same way.
	fresh, _ := newServer(t, true)
	guesser := &client{t: t, srv: fresh, ip: "203.0.113.9"}
	for i := 0; i < maxFailsPerIP; i++ {
		guesser.want("POST", "/api/auth/setup", `{"code":"AAAA-AAAA","password":"correct horse"}`, 403)
	}
	guesser.want("POST", "/api/auth/setup", `{"code":"`+fresh.auth.setupCode+`","password":"correct horse"}`, 429)
}

func TestChangingThePassword(t *testing.T) {
	c, srv, _ := setUp(t, "correct horse")
	elsewhere := &client{t: t, srv: srv}
	elsewhere.want("POST", "/api/auth/login", `{"password":"correct horse"}`, 204)

	c.want("POST", "/api/auth/password", `{"current":"wrong","new":"battery staple"}`, 403)
	c.want("POST", "/api/auth/password", `{"current":"correct horse","new":"short"}`, 400)
	c.want("POST", "/api/auth/password", `{"current":"correct horse","new":"battery staple"}`, 204)

	c.want("GET", "/api/status", "", 200)         // the browser that changed it stays signed in
	elsewhere.want("GET", "/api/status", "", 401) // every other browser is signed out
	elsewhere.want("POST", "/api/auth/login", `{"password":"correct horse"}`, 401)
	elsewhere.want("POST", "/api/auth/login", `{"password":"battery staple"}`, 204)
}

func TestResettingAForgottenPassword(t *testing.T) {
	c, srv, st := setUp(t, "correct horse")
	if err := st.ClearPassword(); err != nil { // what "tattle reset-password" does
		t.Fatal(err)
	}
	c.want("GET", "/api/status", "", 401)
	if body := c.want("GET", "/api/auth", "", 200); body != `{"enabled":true,"setup":true,"signedIn":false}` {
		t.Errorf("auth = %s", body)
	}
	c.want("POST", "/api/auth/setup", `{"code":"`+srv.auth.setupCode+`","password":"a new password"}`, 204)
	c.want("GET", "/api/status", "", 200)
}

func TestDemoModeNeedsNoLogin(t *testing.T) {
	srv, _ := newServer(t, false)
	c := &client{t: t, srv: srv}
	c.want("GET", "/api/status", "", 200)
	if body := c.want("GET", "/api/auth", "", 200); body != `{"enabled":false,"setup":false,"signedIn":true}` {
		t.Errorf("auth = %s", body)
	}
}

func TestPasswordHashing(t *testing.T) {
	a, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := hashPassword("correct horse")
	if a == b {
		t.Error("two hashes of one password are identical; the salt is not random")
	}
	if strings.Contains(a, "correct horse") {
		t.Error("the hash contains the password")
	}
	if !checkPassword(a, "correct horse") || checkPassword(a, "correct horsf") || checkPassword(a, "") {
		t.Error("checkPassword gave a wrong answer")
	}
	for _, bad := range []string{"", "plain", "pbkdf2-sha256$0$AA$AA", "pbkdf2-sha256$1000$!!$AA", "md5$1000$AA$AA"} {
		if checkPassword(bad, "correct horse") {
			t.Errorf("accepted malformed hash %q", bad)
		}
	}
}
