package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/istorry/tattle/internal/store"
)

// The health page answers "is it working?" without anyone reading logs on
// the host. Each check has a state and is worded for someone who is not a
// network engineer, including what to do when something is wrong.

// States, from fine to broken. "off" is for an optional part that is not
// in use, which is not a problem.
const (
	StateOK   = "ok"
	StateOff  = "off"
	StateWarn = "warn"
	StateBad  = "bad"
)

type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Check struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Summary string `json:"summary"`
	// Advice says what to do about it; empty when nothing needs doing.
	Advice string `json:"advice"`
	Facts  []Fact `json:"facts"`
}

type Health struct {
	// State is the worst state of any check.
	State   string  `json:"state"`
	Version string  `json:"version"`
	Uptime  string  `json:"uptime"`
	Checks  []Check `json:"checks"`
}

// CertInfo describes the HTTPS certificate in use.
type CertInfo struct {
	Generated   bool
	Expires     time.Time
	Fingerprint string
}

const (
	quietAfter      = time.Minute          // no packets for this long is worth a mention
	missedTolerance = 0.001                // share of packets missed before it is
	lowDisk         = 1 << 30              // bytes free
	fullDisk        = 100 << 20            //
	oldOwners       = 180 * 24 * time.Hour // the address-owner table is getting stale
	certSoon        = 14 * 24 * time.Hour  // a provided certificate is about to expire
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.health(r.Context(), time.Now()), nil)
}

// health gathers every check.
func (s *Server) health(ctx context.Context, now time.Time) Health {
	h := Health{Version: s.status.Version, Uptime: span(now.Sub(s.status.Started)), State: StateOK}
	h.Checks = []Check{s.checkCapture(now), s.checkMachines(ctx, now), s.checkStorage(ctx, now), s.checkOwners(now), s.checkConnection(now)}
	rank := map[string]int{StateOK: 0, StateOff: 0, StateWarn: 1, StateBad: 2}
	for _, c := range h.Checks {
		if rank[c.State] > rank[h.State] {
			h.State = c.State
		}
	}
	return h
}

func (s *Server) checkCapture(now time.Time) Check {
	c := Check{ID: "capture", Title: "Watching the network", State: StateOK}
	if s.status.Demo {
		c.Summary = "Demo mode: the traffic shown is invented and nothing is being captured."
		c.Facts = []Fact{}
		return c
	}
	bridges := strings.Join(s.status.Interfaces, ", ")
	packets, missed := s.status.Packets.Load(), s.status.Dropped.Load()
	last := time.UnixMilli(s.status.LastPacket.Load())
	c.Facts = []Fact{{"Bridges watched", bridges}, {"Packets seen", number(packets)}}
	if packets > 0 {
		c.Facts = append(c.Facts, Fact{"Last packet", ago(now.Sub(last))})
	}
	c.Facts = append(c.Facts, Fact{"Missed under load", number(missed)})

	switch {
	case packets == 0 && now.Sub(s.status.Started) > quietAfter:
		c.State = StateBad
		c.Summary = "No traffic has been seen on " + bridges + " since Tattle started."
		c.Advice = "Check that this is the bridge your VMs and containers are attached to (run “ip -br link” on the host), and that at least one of them is running. A different bridge can be chosen with --iface."
	case packets == 0:
		c.Summary = "Just started; waiting for the first traffic on " + bridges + "."
	case now.Sub(last) > quietAfter:
		c.State = StateWarn
		c.Summary = "Nothing has crossed " + bridges + " for " + span(now.Sub(last)) + "."
		c.Advice = "This is normal if every machine is idle or stopped. If they are busy, the bridge may have been taken down or recreated; restarting Tattle picks it up again."
	case float64(missed) > float64(packets)*missedTolerance:
		c.State = StateWarn
		c.Summary = fmt.Sprintf("Capturing on %s, but %s of %s packets were missed.", bridges, number(missed), number(packets))
		c.Advice = "At very busy moments Tattle could not keep up, so the amounts shown may be a little low. Which places were contacted is not affected."
	default:
		c.Summary = "Capturing on " + bridges + "; the last packet arrived " + ago(now.Sub(last)) + "."
	}
	return c
}

func (s *Server) checkMachines(ctx context.Context, now time.Time) Check {
	c := Check{ID: "machines", Title: "Finding your machines", State: StateOK}
	if s.status.Demo {
		list, _ := s.store.Machines(ctx, now.Add(-time.Hour), now, store.ScopeAll)
		c.Summary = fmt.Sprintf("Demo mode: %d pretend machines.", len(list))
		c.Facts = []Fact{}
		return c
	}
	m := s.status.machines()
	c.Facts = []Fact{{"VMs", number(uint64(m.vms))}, {"Containers", number(uint64(m.cts))}, {"Read from", s.status.PVEDir}}
	if !m.at.IsZero() {
		c.Facts = append(c.Facts, Fact{"List refreshed", ago(now.Sub(m.at))})
	}
	switch {
	case m.err != "":
		c.State = StateBad
		c.Summary = "The list of VMs and containers could not be read, so traffic cannot be shown by machine name."
		c.Advice = "Tattle reads their names from " + s.status.PVEDir + " and needs to run as a user that can read it. The error was: " + m.err
	case m.vms+m.cts == 0:
		c.State = StateWarn
		c.Summary = "No VMs or containers were found, so only the host's own traffic is shown."
		c.Advice = "If this host does have guests, check that " + s.status.PVEDir + " is where Proxmox keeps its configuration."
	default:
		c.Summary = fmt.Sprintf("Found %s and %s.", plural(m.vms, "VM"), plural(m.cts, "container"))
	}
	return c
}

func (s *Server) checkStorage(ctx context.Context, now time.Time) Check {
	c := Check{ID: "storage", Title: "Keeping history", State: StateOK}
	settings := s.Settings()
	kept := fmt.Sprintf("%s, the last %d minute by minute", plural(settings.RetentionDays, "day"), settings.DetailDays)
	oldest, err := s.store.Oldest(ctx)
	if err != nil {
		c.State = StateBad
		c.Summary = "The history could not be read."
		c.Advice = "The database may be damaged or the disk full. The host's log has the details."
		c.Facts = []Fact{}
		return c
	}
	back := "nothing recorded yet"
	if !oldest.IsZero() {
		back = span(now.Sub(oldest))
	}
	if s.status.Demo {
		c.Summary = "Demo mode: history is kept in memory and is lost when the demo stops."
		c.Facts = []Fact{{"History goes back", back}}
		return c
	}
	size, free, diskOK := s.store.Size(), uint64(0), false
	if s.status.DBPath != "" {
		free, diskOK = diskFree(s.status.DBPath)
	}
	c.Facts = []Fact{{"History goes back", back}, {"Set to keep", kept}, {"Database size", byteSize(uint64(size))}}
	if diskOK {
		c.Facts = append(c.Facts, Fact{"Free space on that disk", byteSize(free)})
	}
	if tidied := s.status.LastTidy.Load(); tidied > 0 {
		c.Facts = append(c.Facts, Fact{"Last tidied", ago(now.Sub(time.UnixMilli(tidied)))})
	}
	c.Facts = append(c.Facts, Fact{"Kept in", s.status.DBPath})
	switch {
	case diskOK && free < fullDisk:
		c.State = StateBad
		c.Summary = "The disk holding the history is almost full: " + byteSize(free) + " left."
		c.Advice = "New traffic may fail to save. Free some space on the host, or keep fewer days of history in Settings."
	case diskOK && free < lowDisk:
		c.State = StateWarn
		c.Summary = "The disk holding the history is getting full: " + byteSize(free) + " left."
		c.Advice = "Free some space on the host, or keep fewer days of history in Settings."
	default:
		c.Summary = "History uses " + byteSize(uint64(size)) + " and goes back " + back + "."
	}
	return c
}

func (s *Server) checkOwners(now time.Time) Check {
	c := Check{ID: "owners", Title: "Knowing who owns an address", State: StateOK}
	table := s.owners.Load()
	if table.Len() == 0 {
		c.State = StateOff
		c.Summary = "The table of address owners is not installed, so internet addresses without a name show without an owner."
		c.Advice = "This is optional. To add it, copy ip2asn.tsv.gz to " + s.status.OwnersPath + " and restart Tattle; the README explains where it comes from."
		c.Facts = []Fact{}
		return c
	}
	c.Facts = []Fact{{"Address ranges", number(uint64(table.Len()))}}
	c.Summary = "Loaded " + number(uint64(table.Len())) + " address ranges."
	if s.status.Demo {
		c.Summary = "Demo mode: a small made-up table."
		return c
	}
	if dated := time.UnixMilli(s.ownersDated.Load()); s.ownersDated.Load() > 0 {
		c.Facts = append(c.Facts, Fact{"Table dated", dated.Format("2 January 2006")})
		if now.Sub(dated) > oldOwners {
			c.State = StateWarn
			c.Summary = "The table of address owners is " + span(now.Sub(dated)) + " old, so some owners may be out of date."
			c.Advice = "A newer table comes with each Tattle release, or can be copied to " + s.status.OwnersPath + " by hand."
		}
	}
	return c
}

func (s *Server) checkConnection(now time.Time) Check {
	c := Check{ID: "connection", Title: "Protecting the dashboard", State: StateOK, Facts: []Fact{}}
	signIn := "A password is required."
	if !s.auth.enabled {
		signIn = "No password is required."
	}
	c.Facts = append(c.Facts, Fact{"Signing in", signIn})
	if s.status.Cert == nil {
		c.State = StateOff
		c.Summary = "Served over plain HTTP. " + signIn
		if !s.status.Demo {
			c.Advice = "This is right when a reverse proxy in front of Tattle provides HTTPS. Otherwise, start Tattle without --http so the password is not sent unencrypted."
		}
		return c
	}
	cert := s.status.Cert()
	kind := "your own"
	if cert.Generated {
		kind = "generated by Tattle"
	}
	c.Facts = append(c.Facts, Fact{"Certificate", kind}, Fact{"Valid until", cert.Expires.Format("2 January 2006")}, Fact{"Fingerprint (SHA-256)", cert.Fingerprint})
	switch left := cert.Expires.Sub(now); {
	case left <= 0:
		c.State = StateBad
		c.Summary = "The HTTPS certificate has expired, so browsers will refuse or warn about the connection."
		c.Advice = "Replace the certificate files; Tattle picks up new ones without a restart."
	case !cert.Generated && left < certSoon:
		c.State = StateWarn
		c.Summary = "Your HTTPS certificate expires in " + span(left) + "."
		c.Advice = "Renew it and replace the files; Tattle picks up new ones without a restart."
	case cert.Generated:
		c.Summary = "Encrypted with a certificate Tattle generated, which it renews itself. " + signIn
	default:
		c.Summary = "Encrypted with your own certificate. " + signIn
	}
	return c
}

// number writes a count with thousands separators.
func number(n uint64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return number(uint64(max(n, 0))) + " " + one + "s"
}

// byteSize writes a number of bytes the way the dashboard does.
func byteSize(n uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 || v >= 10 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// span writes a length of time in its largest sensible unit.
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return plural(int(d.Seconds()), "second")
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour")
	}
	return plural(int(d.Hours()/24), "day")
}

func ago(d time.Duration) string {
	if d < 2*time.Second {
		return "just now"
	}
	return span(d) + " ago"
}
