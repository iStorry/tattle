package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/istorry/tattle/internal/model"
	"github.com/istorry/tattle/internal/owners"
)

func checkOf(t *testing.T, h Health, id string) Check {
	t.Helper()
	for _, c := range h.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q", id)
	return Check{}
}

// liveServer is a server set up the way a healthy, running host would be.
func liveServer(t *testing.T) (*Server, time.Time) {
	t.Helper()
	srv, st := newServer(t, true)
	now := time.Now()
	s := srv.status
	s.Version, s.Interfaces, s.Started = "1.2.3", []string{"vmbr0"}, now.Add(-3*time.Hour)
	s.PVEDir, s.OwnersPath = "/etc/pve", "/usr/share/tattle/ip2asn.tsv.gz"
	s.Packets.Store(1_234_567)
	s.LastPacket.Store(now.Add(-2 * time.Second).UnixMilli())
	s.SetMachines(3, 4, nil)
	s.Cert = func() CertInfo {
		return CertInfo{Generated: true, Expires: now.Add(800 * 24 * time.Hour), Fingerprint: "AB:CD"}
	}
	table, err := owners.Parse(strings.NewReader("1.1.1.0\t1.1.1.255\t13335\tUS\tCLOUDFLARENET - Cloudflare, Inc.\n"))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetOwners(table, now.Add(-20*24*time.Hour))
	at := now.Add(-50 * time.Hour)
	if err := st.Apply([]model.FlowDelta{{Bucket: at.Unix() / 60 * 60, ClientID: "vm-100", ClientIP: "10.0.0.2", ServerIP: "1.1.1.1",
		ServerPort: 443, Proto: "tcp", BytesUp: 1, BytesDown: 1, Packets: 2, Conns: 1, FirstSeen: at.UnixMilli(), LastSeen: at.UnixMilli()}}); err != nil {
		t.Fatal(err)
	}
	return srv, now
}

func TestHealthWhenEverythingWorks(t *testing.T) {
	srv, now := liveServer(t)
	h := srv.health(context.Background(), now)
	if h.State != StateOK || h.Version != "1.2.3" || h.Uptime != "3 hours" || len(h.Checks) != 5 {
		t.Fatalf("health = %+v", h)
	}
	for _, c := range h.Checks {
		if c.State != StateOK || c.Advice != "" || c.Summary == "" {
			t.Errorf("%s: state %s, advice %q, summary %q", c.ID, c.State, c.Advice, c.Summary)
		}
	}
	capture := checkOf(t, h, "capture")
	if capture.Summary != "Capturing on vmbr0; the last packet arrived 2 seconds ago." {
		t.Errorf("capture summary = %q", capture.Summary)
	}
	if capture.Facts[1] != (Fact{"Packets seen", "1,234,567"}) {
		t.Errorf("capture facts = %+v", capture.Facts)
	}
	if got := checkOf(t, h, "machines").Summary; got != "Found 3 VMs and 4 containers." {
		t.Errorf("machines summary = %q", got)
	}
	if got := checkOf(t, h, "storage").Facts[0]; got != (Fact{"History goes back", "2 days"}) {
		t.Errorf("storage fact = %+v", got)
	}
	if got := checkOf(t, h, "connection").Summary; !strings.Contains(got, "generated") || !strings.Contains(got, "password is required") {
		t.Errorf("connection summary = %q", got)
	}
}

func TestHealthProblemsAreExplained(t *testing.T) {
	ctx := context.Background()
	problem := func(id, state string, change func(*Server, time.Time)) Check {
		t.Helper()
		srv, now := liveServer(t)
		change(srv, now)
		h := srv.health(ctx, now)
		c := checkOf(t, h, id)
		if c.State != state {
			t.Errorf("%s: state %s, want %s (%s)", id, c.State, state, c.Summary)
		}
		if (state == StateWarn || state == StateBad) && (c.Advice == "" || h.State != state) {
			t.Errorf("%s: advice %q, overall state %s", id, c.Advice, h.State)
		}
		return c
	}

	c := problem("capture", StateBad, func(s *Server, _ time.Time) { s.status.Packets.Store(0) })
	if !strings.Contains(c.Summary, "No traffic has been seen on vmbr0") || !strings.Contains(c.Advice, "--iface") {
		t.Errorf("no packets: %q / %q", c.Summary, c.Advice)
	}
	// Just started and nothing seen yet is not a fault.
	problem("capture", StateOK, func(s *Server, now time.Time) {
		s.status.Packets.Store(0)
		s.status.Started = now.Add(-5 * time.Second)
	})
	c = problem("capture", StateWarn, func(s *Server, now time.Time) { s.status.LastPacket.Store(now.Add(-10 * time.Minute).UnixMilli()) })
	if !strings.Contains(c.Summary, "for 10 minutes") {
		t.Errorf("quiet: %q", c.Summary)
	}
	c = problem("capture", StateWarn, func(s *Server, _ time.Time) { s.status.Dropped.Store(50_000) })
	if !strings.Contains(c.Summary, "50,000 of 1,234,567 packets were missed") {
		t.Errorf("missed: %q", c.Summary)
	}
	// A handful missed out of a million is not worth raising.
	problem("capture", StateOK, func(s *Server, _ time.Time) { s.status.Dropped.Store(12) })

	problem("machines", StateWarn, func(s *Server, _ time.Time) { s.status.SetMachines(0, 0, nil) })
	c = problem("machines", StateBad, func(s *Server, _ time.Time) { s.status.SetMachines(0, 0, context.DeadlineExceeded) })
	if !strings.Contains(c.Advice, "/etc/pve") || !strings.Contains(c.Advice, "deadline") {
		t.Errorf("unreadable config: %q", c.Advice)
	}

	// The optional table being absent is "off", not a warning.
	c = problem("owners", StateOff, func(s *Server, _ time.Time) { s.owners.Store(nil) })
	if !strings.Contains(c.Advice, "optional") {
		t.Errorf("no table: %q", c.Advice)
	}
	problem("owners", StateWarn, func(s *Server, now time.Time) { s.ownersDated.Store(now.Add(-400 * 24 * time.Hour).UnixMilli()) })

	problem("connection", StateOff, func(s *Server, _ time.Time) { s.status.Cert = nil })
	problem("connection", StateWarn, func(s *Server, now time.Time) {
		s.status.Cert = func() CertInfo { return CertInfo{Expires: now.Add(5 * 24 * time.Hour)} }
	})
	problem("connection", StateBad, func(s *Server, now time.Time) {
		s.status.Cert = func() CertInfo { return CertInfo{Expires: now.Add(-time.Hour)} }
	})
	// A generated certificate near expiry renews itself, so it is not raised.
	problem("connection", StateOK, func(s *Server, now time.Time) {
		s.status.Cert = func() CertInfo { return CertInfo{Generated: true, Expires: now.Add(5 * 24 * time.Hour)} }
	})
}

func TestHealthEndpoint(t *testing.T) {
	c, _, _ := setUp(t, "correct horse")
	anon := &client{t: t, srv: c.srv}
	anon.want("GET", "/api/health", "", 401)
	body := c.want("GET", "/api/health", "", 200)
	for _, want := range []string{`"id":"capture"`, `"id":"machines"`, `"id":"storage"`, `"id":"owners"`, `"id":"connection"`, `"facts":[`} {
		if !strings.Contains(body, want) {
			t.Errorf("health is missing %s: %s", want, body)
		}
	}
	if status := c.want("GET", "/api/status", "", 200); !strings.Contains(status, `"health":"`) {
		t.Errorf("status has no health: %s", status)
	}
}

func TestHealthInDemoMode(t *testing.T) {
	srv, _ := newServer(t, false)
	srv.status.Demo = true
	h := srv.health(context.Background(), time.Now())
	if h.State != StateOK {
		t.Errorf("demo health = %s", h.State)
	}
	for _, id := range []string{"capture", "machines", "storage"} {
		if c := checkOf(t, h, id); !strings.Contains(c.Summary, "Demo mode") {
			t.Errorf("%s: %q", id, c.Summary)
		}
	}
}

func TestFormatting(t *testing.T) {
	for in, want := range map[uint64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567"} {
		if got := number(in); got != want {
			t.Errorf("number(%d) = %q", in, got)
		}
	}
	for in, want := range map[uint64]string{0: "0 B", 900: "900 B", 1536: "1.5 KB", 26 << 20: "26 MB", 3 << 30: "3.0 GB"} {
		if got := byteSize(in); got != want {
			t.Errorf("byteSize(%d) = %q", in, got)
		}
	}
	for in, want := range map[time.Duration]string{time.Second: "1 second", 90 * time.Second: "1 minute", 5 * time.Hour: "5 hours", 72 * time.Hour: "3 days"} {
		if got := span(in); got != want {
			t.Errorf("span(%v) = %q", in, got)
		}
	}
}
