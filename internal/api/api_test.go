package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/istorry/tattle/internal/model"
	"github.com/istorry/tattle/internal/owners"
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
		"/api/status", "/api/machines", "/api/timeline", "/api/peers", "/api/peer?name=example.com",
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

func TestRetentionIsASetting(t *testing.T) {
	c, _, st := setUp(t, "correct horse")
	now := time.Now()
	flow := func(age time.Duration, ip string) model.FlowDelta {
		at := now.Add(-age)
		return model.FlowDelta{Bucket: at.Unix() / 60 * 60, ClientID: "vm-100", ClientIP: "10.0.0.2", ServerIP: ip,
			ServerPort: 443, Proto: "tcp", BytesUp: 1, BytesDown: 1, Packets: 2, Conns: 1, FirstSeen: at.UnixMilli(), LastSeen: at.UnixMilli()}
	}
	if err := st.Apply([]model.FlowDelta{flow(time.Hour, "198.51.100.1"), flow(5*24*time.Hour, "198.51.100.2")}); err != nil {
		t.Fatal(err)
	}
	peers := func() string { return c.want("GET", "/api/peers?range=2592000", "", 200) }

	if body := c.want("GET", "/api/status", "", 200); !strings.Contains(body, `"retentionDays":90`) {
		t.Errorf("status = %s", body)
	}
	if body := peers(); !strings.Contains(body, "198.51.100.2") {
		t.Fatalf("five-day-old traffic missing before the change: %s", body)
	}

	c.want("PUT", "/api/settings", `{"show":"all","localNets":[],"privateIsLocal":true,"retentionDays":0,"detailDays":1}`, 400)
	c.want("PUT", "/api/settings", `{"show":"all","localNets":[],"privateIsLocal":true,"retentionDays":3,"detailDays":2}`, 200)

	// Keeping fewer days removes the older history straight away.
	if body := peers(); strings.Contains(body, "198.51.100.2") || !strings.Contains(body, "198.51.100.1") {
		t.Errorf("after shortening to 3 days: %s", body)
	}
	if body := c.want("GET", "/api/status", "", 200); !strings.Contains(body, `"retentionDays":3`) {
		t.Errorf("status = %s", body)
	}
}

func TestNamingDevices(t *testing.T) {
	c, _, _ := setUp(t, "correct horse")
	anon := &client{t: t, srv: c.srv}
	anon.want("GET", "/api/devices", "", 401)
	anon.want("PUT", "/api/devices", `{"ip":"192.168.1.42","name":"Laptop"}`, 401)

	if body := c.want("PUT", "/api/devices", `{"ip":"192.168.1.42","name":"  My   laptop "}`, 200); body != `[{"ip":"192.168.1.42","name":"My laptop","announced":""}]` {
		t.Errorf("after naming: %s", body)
	}
	c.want("PUT", "/api/devices", `{"ip":"nonsense","name":"x"}`, 400)
	if text := c.want("PUT", "/api/devices", `{"ip":"192.168.1.42","name":"`+strings.Repeat("x", 41)+`"}`, 400); !strings.Contains(text, "too long") {
		t.Errorf("message = %q", text)
	}
	if body := c.want("PUT", "/api/devices", `{"ip":"192.168.1.42","name":""}`, 200); body != `[]` {
		t.Errorf("after removing the name: %s", body)
	}
}

func TestOwnersAreAttachedToInternetAddresses(t *testing.T) {
	c, srv, st := setUp(t, "correct horse")
	now := time.Now()
	flow := func(ip, domain string) model.FlowDelta {
		return model.FlowDelta{Bucket: now.Unix() / 60 * 60, ClientID: "vm-100", ClientIP: "192.168.1.20", ServerIP: ip, Domain: domain,
			ServerPort: 443, Proto: "tcp", BytesUp: 1, BytesDown: 1, Packets: 2, Conns: 1, FirstSeen: now.UnixMilli(), LastSeen: now.UnixMilli()}
	}
	if err := st.Apply([]model.FlowDelta{flow("1.1.1.1", ""), flow("192.168.1.91", ""), flow("203.0.113.5", "unknown-owner.example")}); err != nil {
		t.Fatal(err)
	}

	// Without a table nothing is claimed.
	if body := c.want("GET", "/api/peers", "", 200); strings.Contains(body, `"owner"`) {
		t.Errorf("owner reported without a table: %s", body)
	}

	table, err := owners.Parse(strings.NewReader("1.1.1.0\t1.1.1.255\t13335\tUS\tCLOUDFLARENET - Cloudflare, Inc.\n" +
		"192.168.0.0\t192.168.255.255\t64999\tZZ\tBOGUS - Should never be shown\n"))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetOwners(table, time.Time{})

	body := c.want("GET", "/api/peers", "", 200)
	if !strings.Contains(body, `"owner":{"name":"Cloudflare, Inc.","asn":13335,"country":"US"}`) {
		t.Errorf("no owner for 1.1.1.1: %s", body)
	}
	// An address inside the home network is never looked up, whatever the table says.
	if strings.Contains(body, "Should never be shown") {
		t.Errorf("a local address was given an owner: %s", body)
	}
	if strings.Count(body, `"owner"`) != 1 {
		t.Errorf("expected exactly one owner: %s", body)
	}
	if body := c.want("GET", "/api/peer?name=1.1.1.1", "", 200); !strings.Contains(body, `"owner":{"name":"Cloudflare, Inc."`) {
		t.Errorf("detail has no owner: %s", body)
	}
	if body := c.want("GET", "/api/status", "", 200); !strings.Contains(body, `"ownerRanges":2`) {
		t.Errorf("status = %s", body)
	}
}

func TestMapShowsDevicesAndTheRouter(t *testing.T) {
	c, srv, st := setUp(t, "correct horse")
	(&client{t: t, srv: c.srv}).want("GET", "/api/map", "", 401)

	srv.status.SetGateways([]string{"192.168.1.1"})
	now := time.Now().UnixMilli()
	if err := st.SavePresence([]model.Sighting{
		{MAC: "3c:52:a1:00:00:01", IP: "192.168.1.1", Iface: "vmbr0", Seen: now},
		{MAC: "a4:83:e7:00:00:42", IP: "192.168.1.42", Iface: "vmbr0", Seen: now},
	}); err != nil {
		t.Fatal(err)
	}
	c.want("PUT", "/api/devices", `{"ip":"192.168.1.42","name":"Laptop"}`, 200)

	body := c.want("GET", "/api/map?range=3600", "", 200)
	for _, want := range []string{
		`"id":"dev-3c:52:a1:00:00:01","kind":"router"`,
		`"id":"dev-a4:83:e7:00:00:42","kind":"device","name":"Laptop"`,
		`"links":[]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("map is missing %s:\n%s", want, body)
		}
	}
}
