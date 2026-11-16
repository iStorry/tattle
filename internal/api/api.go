// Package api serves the dashboard and the JSON it reads.
package api

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/istorry/tattle/internal/store"
)

// Status describes what the collector is doing. The counters are updated by
// the capture loop while the server reads them.
type Status struct {
	Version    string
	Demo       bool
	Interfaces []string
	Started    time.Time
	// LocalNets are the networks the host is directly connected to, plus any
	// the user added. Addresses in them count as "inside your network".
	LocalNets []netip.Prefix
	Packets   atomic.Uint64
	Dropped   atomic.Uint64
}

type Server struct {
	store  *store.Store
	status *Status
	auth   auth
	mux    *http.ServeMux

	mu       sync.Mutex
	settings store.Settings
}

// New builds the HTTP handler. ui is the built dashboard. With requireLogin,
// everything but the sign-in pages needs a signed-in browser.
func New(st *store.Store, status *Status, ui fs.FS, requireLogin bool) *Server {
	s := &Server{store: st, status: status, mux: http.NewServeMux()}
	s.auth.enabled = requireLogin
	s.mux.HandleFunc("GET /api/auth", s.handleAuth)
	s.mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("POST /api/auth/password", s.handleChangePassword)
	settings, err := st.LoadSettings()
	if err != nil {
		log.Printf("reading settings, using the defaults: %v", err)
	}
	custom, _ := settings.Validate()
	s.apply(settings, custom)
	s.mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	s.mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	s.mux.HandleFunc("GET /api/split", s.handleSplit)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/machines", s.handleMachines)
	s.mux.HandleFunc("GET /api/timeline", s.handleTimeline)
	s.mux.HandleFunc("GET /api/peers", s.handlePeers)
	s.mux.HandleFunc("GET /api/peer", s.handlePeer)
	s.mux.Handle("GET /", http.FileServerFS(ui))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	// The dashboard loads nothing from other sites and may not be framed.
	h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
	if s.auth.enabled && strings.HasPrefix(r.URL.Path, "/api/") && !openPaths[r.URL.Path] && !s.signedIn(r) {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// openPaths are the only API paths that answer without a session. The
// dashboard's own files are also served, since they hold no data and are
// what draws the sign-in page.
var openPaths = map[string]bool{
	"/api/auth":        true,
	"/api/auth/setup":  true,
	"/api/auth/login":  true,
	"/api/auth/logout": true,
}

// Settings returns the settings currently in effect.
func (s *Server) Settings() store.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// apply makes settings take effect: the host's own networks and the ones
// the user added together define "inside your network".
func (s *Server) apply(settings store.Settings, custom []netip.Prefix) {
	s.mu.Lock()
	s.settings = settings
	s.mu.Unlock()
	store.SetLocal(append(append([]netip.Prefix{}, s.status.LocalNets...), custom...), settings.PrivateIsLocal)
}

// scope reads the "scope" query parameter.
func scope(r *http.Request) string {
	switch v := r.URL.Query().Get("scope"); v {
	case store.ScopeInternet, store.ScopeLocal:
		return v
	}
	return store.ScopeAll
}

// maxRange matches the longest range the dashboard offers.
const maxRange = 31 * 24 * time.Hour

// since reads the "range" query parameter, a number of seconds back from now.
func since(r *http.Request, now time.Time) time.Time {
	secs, err := strconv.Atoi(r.URL.Query().Get("range"))
	d := time.Duration(secs) * time.Second
	if err != nil || d < time.Minute {
		d = time.Hour
	}
	return now.Add(-min(d, maxRange))
}

func limit(r *http.Request, def, most int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n < 1 {
		return def
	}
	return min(n, most)
}

func writeJSON(w http.ResponseWriter, v any, err error) {
	if err != nil {
		log.Printf("api: %v", err)
		http.Error(w, "something went wrong reading the history", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"version":       s.status.Version,
		"demo":          s.status.Demo,
		"interfaces":    s.status.Interfaces,
		"retentionDays": s.Settings().RetentionDays,
		"localNetworks": s.status.LocalNets,
		"started":       s.status.Started.UnixMilli(),
		"now":           time.Now().UnixMilli(),
		"packets":       s.status.Packets.Load(),
		"dropped":       s.status.Dropped.Load(),
	}, nil)
}

func (s *Server) handleMachines(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	v, err := s.store.Machines(r.Context(), since(r, now), now, scope(r))
	writeJSON(w, v, err)
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	v, err := s.store.Timeline(r.Context(), since(r, now), now, r.URL.Query().Get("machine"), scope(r))
	writeJSON(w, v, err)
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Peers(r.Context(), since(r, time.Now()), r.URL.Query().Get("machine"), scope(r), limit(r, 500, 2000))
	writeJSON(w, v, err)
}

func (s *Server) handlePeer(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := q.Get("name")
	if name == "" || len(name) > 255 {
		http.Error(w, "which destination?", http.StatusBadRequest)
		return
	}
	now := time.Now()
	v, err := s.store.Peer(r.Context(), since(r, now), now, q.Get("direction"), name, q.Get("machine"))
	writeJSON(w, v, err)
}

func (s *Server) handleSplit(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Split(r.Context(), since(r, time.Now()), r.URL.Query().Get("machine"))
	writeJSON(w, v, err)
}

func (s *Server) settingsResponse() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	detected := s.status.LocalNets
	if detected == nil {
		detected = []netip.Prefix{}
	}
	return map[string]any{"settings": s.settings, "detected": detected}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.settingsResponse(), nil)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	settings := store.DefaultSettings()
	if !readJSON(w, r, &settings) {
		return
	}
	custom, err := settings.Validate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.SaveSettings(settings); err != nil {
		fail(w, "saving settings", err)
		return
	}
	shorter := settings.RetentionDays < s.Settings().RetentionDays
	s.apply(settings, custom)
	if shorter {
		// Keeping less history takes effect now, not at the next clean-up.
		if err := s.store.Prune(time.Now().AddDate(0, 0, -settings.RetentionDays)); err != nil {
			log.Printf("api: removing old history: %v", err)
		}
	}
	writeJSON(w, s.settingsResponse(), nil)
}
