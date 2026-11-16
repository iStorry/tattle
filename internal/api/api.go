// Package api serves the dashboard and the JSON it reads.
package api

import (
	"crypto/subtle"
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
	Version       string
	Demo          bool
	Interfaces    []string
	RetentionDays int
	Started       time.Time
	// LocalNets are the networks the host is directly connected to, plus any
	// the user added. Addresses in them count as "inside your network".
	LocalNets []netip.Prefix
	Packets   atomic.Uint64
	Dropped   atomic.Uint64
}

type Server struct {
	store    *store.Store
	status   *Status
	password string
	mux      *http.ServeMux

	mu       sync.Mutex
	settings store.Settings
}

// New builds the HTTP handler. ui is the built dashboard; password, when
// set, is required (with any username) to open the dashboard.
func New(st *store.Store, status *Status, ui fs.FS, password string) *Server {
	s := &Server{store: st, status: status, password: password, mux: http.NewServeMux()}
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
	s.mux.HandleFunc("GET /api/activity", s.handleActivity)
	s.mux.Handle("GET /", http.FileServerFS(ui))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.password != "" {
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(s.password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Tattle"`)
			http.Error(w, "password required", http.StatusUnauthorized)
			return
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
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
		"retentionDays": s.status.RetentionDays,
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

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v, err := s.store.Activity(r.Context(), since(r, time.Now()), q.Get("machine"), scope(r), q.Get("q"), limit(r, 200, 1000))
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
	// Browsers will not send JSON to another site without asking first, so
	// insisting on it stops other web pages from changing the settings.
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "settings must be sent as JSON", http.StatusUnsupportedMediaType)
		return
	}
	settings := store.DefaultSettings()
	body := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	body.DisallowUnknownFields()
	if err := body.Decode(&settings); err != nil {
		http.Error(w, "the settings could not be read", http.StatusBadRequest)
		return
	}
	custom, err := settings.Validate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.SaveSettings(settings); err != nil {
		log.Printf("api: saving settings: %v", err)
		http.Error(w, "the settings could not be saved", http.StatusInternalServerError)
		return
	}
	s.apply(settings, custom)
	writeJSON(w, s.settingsResponse(), nil)
}
