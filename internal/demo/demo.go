// Package demo invents a small home lab and its traffic, so the dashboard
// can be tried on any computer without a Proxmox host.
package demo

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/istorry/tattle/internal/model"
	"github.com/istorry/tattle/internal/owners"
	"github.com/istorry/tattle/internal/store"
)

type dest struct {
	domain   string // "" for a bare address
	ip       string
	serverID string // set when the destination is another machine
	port     uint16
	proto    string
	up, down int64   // typical bytes per connection
	weight   float64 // chance of happening in any given minute
	// fresh, when set, means this only started that long before the demo
	// was opened, so there is something for "What's new" to show.
	fresh time.Duration
}

type machine struct {
	model.Machine
	dests   []dest
	inbound []dest // ip is the caller; port is the service on this machine
}

// Router is the pretend network's way out to the internet.
const Router = "192.168.1.1"

const bridge = "vmbr0"

// gadget is something on the pretend network that is not a machine.
type gadget struct {
	mac, ip string
	// quiet is how long ago it was last heard from; 0 means it is there now.
	quiet time.Duration
}

// gadgets are the devices the map shows around the machines. Some talk to
// the machines; the last few are only ever heard announcing themselves.
var gadgets = []gadget{
	{mac: "3c:52:a1:00:00:01", ip: Router},
	{mac: "a4:83:e7:00:00:42", ip: laptop},
	{mac: "da:a1:19:00:00:57", ip: phone},
	{mac: "64:16:66:00:00:80", ip: tv},
	{mac: "48:3f:da:00:00:91", ip: "192.168.1.91"},
	{mac: "48:3f:da:00:00:92", ip: "192.168.1.92"},
	{mac: "f4:39:09:00:00:60", ip: printer},
	{mac: "b8:27:eb:00:00:64", ip: "192.168.1.64"},
	{mac: "0c:8b:95:00:00:85", ip: "192.168.1.133"},
	{mac: "7c:2e:bd:00:00:71", ip: "192.168.1.71", quiet: 5 * time.Hour},
	{mac: "5e:0f:c2:00:00:77", ip: "192.168.1.77", quiet: 3 * 24 * time.Hour},
}

// heard returns the gadgets' sightings as of now: once long ago, so each
// has a first-seen date, and again when it was last around.
func heard(now time.Time, first bool) []model.Sighting {
	var out []model.Sighting
	for i, g := range gadgets {
		at := now.Add(-g.quiet)
		if first {
			at = now.Add(-time.Duration(34-2*i) * 24 * time.Hour)
		} else if g.quiet > 0 {
			continue
		}
		out = append(out, model.Sighting{MAC: g.mac, IP: g.ip, Iface: bridge, Seen: at.UnixMilli()})
	}
	return out
}

const (
	printer = "192.168.1.60"
	laptop  = "192.168.1.42"
	phone   = "192.168.1.57"
	tv      = "192.168.1.80"
	pihole  = "192.168.1.53"
	docker  = "192.168.1.21"
	hass    = "192.168.1.20"
	nas     = "192.168.1.23"
	windows = "192.168.1.22"
	proxy   = "192.168.1.30"
	jelly   = "192.168.1.31"
	hostIP  = "192.168.1.10"
)

// stun is one relay a VPN asks for its public address.
func stun(ip string) dest {
	return dest{ip: ip, port: 3478, proto: "udp", up: 82, down: 74, weight: 0.2}
}

func lab() []machine {
	dns := dest{ip: pihole, serverID: "ct-200", port: 53, proto: "udp", up: 90, down: 180, weight: 0.9}
	ntp := dest{domain: "time.cloudflare.com", ip: "203.0.113.123", port: 123, proto: "udp", up: 90, down: 90, weight: 0.05}
	return []machine{
		{
			Machine: model.Machine{ID: "host", Kind: "host", Name: "pve", Node: "pve", MACs: []string{"bc:24:11:00:00:10"}, IPs: []string{hostIP}, Bridges: []string{bridge}},
			dests: []dest{
				dns, ntp,
				{domain: "download.proxmox.com", ip: "198.51.100.14", port: 443, proto: "tcp", up: 4_000, down: 900_000, weight: 0.01},
				{domain: "enterprise.proxmox.com", ip: "198.51.100.15", port: 443, proto: "tcp", up: 2_000, down: 6_000, weight: 0.02},
			},
			inbound: []dest{{ip: laptop, port: 8006, proto: "tcp", up: 12_000, down: 180_000, weight: 0.15}},
		},
		{
			Machine: model.Machine{ID: "vm-100", Kind: "vm", VMID: 100, Name: "home-assistant", Node: "pve", MACs: []string{"bc:24:11:00:01:00"}, IPs: []string{hass}, Bridges: []string{bridge}},
			dests: []dest{
				dns, ntp,
				{domain: "api.github.com", ip: "198.51.100.30", port: 443, proto: "tcp", up: 2_500, down: 40_000, weight: 0.03},
				{domain: "api.open-meteo.com", ip: "198.51.100.41", port: 443, proto: "tcp", up: 1_800, down: 9_000, weight: 0.2},
				{domain: "", ip: "192.168.1.91", port: 80, proto: "tcp", up: 600, down: 1_400, weight: 0.8},
				{domain: "", ip: "192.168.1.92", port: 1883, proto: "tcp", up: 400, down: 2_200, weight: 0.7},
				{domain: "", ip: tv, port: 8009, proto: "tcp", up: 900, down: 1_100, weight: 0.3},
			},
			inbound: []dest{
				{ip: phone, port: 8123, proto: "tcp", up: 8_000, down: 95_000, weight: 0.25},
				{ip: proxy, serverID: "ct-201", port: 8123, proto: "tcp", up: 3_000, down: 30_000, weight: 0.1},
			},
		},
		{
			Machine: model.Machine{ID: "vm-101", Kind: "vm", VMID: 101, Name: "docker-host", Node: "pve", MACs: []string{"bc:24:11:00:01:01"}, IPs: []string{docker}, Bridges: []string{bridge}},
			dests: []dest{
				dns, ntp,
				{domain: "registry-1.docker.io", ip: "198.51.100.60", port: 443, proto: "tcp", up: 9_000, down: 38_000_000, weight: 0.02},
				{domain: "auth.docker.io", ip: "198.51.100.61", port: 443, proto: "tcp", up: 2_200, down: 5_500, weight: 0.03},
				{domain: "ghcr.io", ip: "198.51.100.62", port: 443, proto: "tcp", up: 5_000, down: 12_000_000, weight: 0.01},
				{domain: "api.telegram.org", ip: "198.51.100.70", port: 443, proto: "tcp", up: 1_500, down: 2_400, weight: 0.4},
				{domain: "hooks.slack.com", ip: "198.51.100.71", port: 443, proto: "tcp", up: 2_100, down: 900, weight: 0.05},
				{domain: "", ip: nas, serverID: "vm-103", port: 2049, proto: "tcp", up: 250_000, down: 1_800_000, weight: 0.5},
				{domain: "telemetry.example-app.io", ip: "203.0.113.200", port: 80, proto: "tcp", up: 3_400, down: 500, weight: 0.12},
				{domain: "updates.new-plugin.example", ip: "203.0.113.210", port: 443, proto: "tcp", up: 2_800, down: 190_000, weight: 0.5, fresh: 25 * time.Minute},
				// A VPN like Tailscale timing each of its relays: many nameless
				// addresses, one tiny exchange each. The dashboard folds them.
				stun("192.0.2.11"), stun("192.0.2.12"), stun("192.0.2.13"), stun("192.0.2.14"),
				stun("192.0.2.140"), stun("192.0.2.141"), stun("192.0.2.142"), stun("192.0.2.143"),
			},
			inbound: []dest{{ip: laptop, port: 22, proto: "tcp", up: 30_000, down: 70_000, weight: 0.04}},
		},
		{
			Machine: model.Machine{ID: "vm-102", Kind: "vm", VMID: 102, Name: "windows-11", Node: "pve", MACs: []string{"bc:24:11:00:01:02"}, IPs: []string{windows}, Bridges: []string{bridge}},
			dests: []dest{
				dns,
				{domain: "settings-win.data.microsoft.com", ip: "198.51.100.90", port: 443, proto: "tcp", up: 6_000, down: 9_000, weight: 0.3},
				{domain: "v10.events.data.microsoft.com", ip: "198.51.100.91", port: 443, proto: "tcp", up: 14_000, down: 3_000, weight: 0.35},
				{domain: "dl.delivery.mp.microsoft.com", ip: "198.51.100.92", port: 443, proto: "tcp", up: 12_000, down: 52_000_000, weight: 0.008},
				{domain: "edge.microsoft.com", ip: "198.51.100.93", port: 443, proto: "udp", up: 5_000, down: 60_000, weight: 0.1},
				{domain: "time.windows.com", ip: "203.0.113.124", port: 123, proto: "udp", up: 90, down: 90, weight: 0.03},
				{domain: "", ip: "203.0.113.66", port: 6881, proto: "udp", up: 48_000, down: 310_000, weight: 0.6, fresh: 3 * time.Hour},
			},
			inbound: []dest{{ip: laptop, port: 3389, proto: "tcp", up: 400_000, down: 6_000_000, weight: 0.03}},
		},
		{
			Machine: model.Machine{ID: "vm-103", Kind: "vm", VMID: 103, Name: "truenas", Node: "pve", MACs: []string{"bc:24:11:00:01:03"}, IPs: []string{nas}, Bridges: []string{bridge}},
			dests: []dest{
				dns, ntp,
				{domain: "update.freenas.org", ip: "198.51.100.100", port: 443, proto: "tcp", up: 2_000, down: 14_000, weight: 0.01},
				{domain: "s3.eu-central-003.backblazeb2.com", ip: "198.51.100.101", port: 443, proto: "tcp", up: 64_000_000, down: 90_000, weight: 0.006},
			},
			inbound: []dest{
				{ip: laptop, port: 445, proto: "tcp", up: 500_000, down: 9_000_000, weight: 0.08},
				{ip: tv, port: 445, proto: "tcp", up: 80_000, down: 22_000_000, weight: 0.04},
			},
		},
		{
			Machine: model.Machine{ID: "ct-200", Kind: "ct", VMID: 200, Name: "pihole", Node: "pve", MACs: []string{"bc:24:11:00:02:00"}, IPs: []string{pihole}, Bridges: []string{bridge}},
			dests: []dest{
				ntp,
				{domain: "", ip: "9.9.9.9", port: 53, proto: "udp", up: 2_400, down: 5_200, weight: 0.95},
				{domain: "", ip: "1.1.1.1", port: 53, proto: "udp", up: 1_900, down: 4_100, weight: 0.9},
				{domain: "raw.githubusercontent.com", ip: "198.51.100.31", port: 443, proto: "tcp", up: 3_000, down: 2_400_000, weight: 0.004},
			},
			inbound: []dest{
				{ip: laptop, port: 53, proto: "udp", up: 1_400, down: 3_000, weight: 0.8},
				{ip: phone, port: 53, proto: "udp", up: 900, down: 2_100, weight: 0.7},
				{ip: tv, port: 53, proto: "udp", up: 700, down: 1_500, weight: 0.5},
			},
		},
		{
			Machine: model.Machine{ID: "ct-201", Kind: "ct", VMID: 201, Name: "nginx-proxy", Node: "pve", MACs: []string{"bc:24:11:00:02:01"}, IPs: []string{proxy}, Bridges: []string{bridge}},
			dests: []dest{
				dns,
				{domain: "acme-v02.api.letsencrypt.org", ip: "198.51.100.110", port: 443, proto: "tcp", up: 7_000, down: 11_000, weight: 0.004},
				{domain: "", ip: jelly, serverID: "ct-202", port: 8096, proto: "tcp", up: 40_000, down: 7_500_000, weight: 0.12},
			},
			inbound: []dest{
				{ip: "203.0.113.77", port: 443, proto: "tcp", up: 30_000, down: 6_800_000, weight: 0.1},
				{ip: "198.51.100.222", port: 443, proto: "tcp", up: 1_200, down: 2_800, weight: 0.03},
				{ip: "203.0.113.9", port: 22, proto: "tcp", up: 900, down: 700, weight: 0.02},
				{ip: "198.51.100.77", port: 443, proto: "tcp", up: 2_000, down: 5_000, weight: 0.4, fresh: 8 * time.Minute},
			},
		},
		{
			Machine: model.Machine{ID: "ct-202", Kind: "ct", VMID: 202, Name: "jellyfin", Node: "pve", MACs: []string{"bc:24:11:00:02:02"}, IPs: []string{jelly}, Bridges: []string{bridge}},
			dests: []dest{
				dns,
				{domain: "api.themoviedb.org", ip: "198.51.100.120", port: 443, proto: "tcp", up: 2_600, down: 48_000, weight: 0.06},
				{domain: "image.tmdb.org", ip: "198.51.100.121", port: 443, proto: "tcp", up: 1_900, down: 640_000, weight: 0.04},
				{domain: "repo.jellyfin.org", ip: "198.51.100.122", port: 443, proto: "tcp", up: 1_500, down: 30_000, weight: 0.01},
				{domain: "", ip: nas, serverID: "vm-103", port: 445, proto: "tcp", up: 90_000, down: 14_000_000, weight: 0.1},
			},
			inbound: []dest{{ip: tv, port: 8096, proto: "tcp", up: 60_000, down: 18_000_000, weight: 0.08}},
		},
	}
}

// around returns n varied by up to plus or minus 60 percent.
func around(n int64) int64 {
	return max(60, int64(float64(n)*(0.4+rand.Float64()*1.2)))
}

// minute produces the traffic for one minute. scale is the fraction of the
// minute being generated, so the live loop can emit a little every few
// seconds and still average out to the same rates as the backfill.
func minute(lab []machine, at, opened time.Time, scale float64) []model.FlowDelta {
	return span(lab, at, opened, scale, 60)
}

// hour produces an hour's traffic as hourly totals, the form older history
// is kept in.
func hour(lab []machine, at, opened time.Time) []model.FlowDelta {
	return span(lab, at, opened, 60, 3600)
}

// span produces the traffic for one bucket of the given width in seconds.
// scale is how many minutes' worth of activity the bucket holds.
func span(lab []machine, at, opened time.Time, scale float64, width int64) []model.FlowDelta {
	bucket := at.Unix() / width * width
	// Homes are quieter at night.
	hour := float64(at.Hour()) + float64(at.Minute())/60
	if hour < 7 {
		scale *= 0.35
	}
	var out []model.FlowDelta
	emit := func(clientID, clientIP, serverID, serverIP string, d dest) {
		if d.fresh > 0 && at.Before(opened.Add(-d.fresh)) {
			return
		}
		// Usually one connection or none; over a longer bucket, several.
		conns := int64(1)
		if expected := d.weight * scale; expected > 1 {
			conns = int64(expected + rand.Float64())
		} else if rand.Float64() > expected {
			return
		}
		out = append(out, model.FlowDelta{
			Bucket: bucket, ClientID: clientID, ClientIP: clientIP, ServerID: serverID, ServerIP: serverIP,
			ServerPort: d.port, Proto: d.proto, Domain: d.domain,
			BytesUp: conns * around(d.up), BytesDown: conns * around(d.down), Packets: conns * (4 + rand.Int64N(40)), Conns: conns,
			FirstSeen: at.UnixMilli() - rand.Int64N(1500), LastSeen: at.UnixMilli(),
		})
	}
	for _, m := range lab {
		for _, d := range m.dests {
			emit(m.ID, m.IPs[0], d.serverID, d.ip, d)
		}
		for _, d := range m.inbound {
			if d.serverID != "" {
				continue // already produced by the other machine's outgoing list
			}
			emit("", d.ip, m.ID, m.IPs[0], d)
		}
	}
	return out
}

// Run fills in five weeks of history and then keeps producing live traffic until
// ctx is cancelled.
func Run(ctx context.Context, st *store.Store) error {
	l := lab()
	machines := make([]model.Machine, len(l))
	for i, m := range l {
		machines[i] = m.Machine
	}
	if err := st.SaveMachines(machines); err != nil {
		return fmt.Errorf("demo: %w", err)
	}

	now := time.Now()
	// Some of the pretend devices announce a name; the rest are left for
	// the visitor to name.
	if err := st.SaveAnnounced(map[string]string{laptop: "macbook-pro", phone: "pixel-8", tv: "living-room-tv",
		printer: "hp-envy-6000", "192.168.1.64": "raspberrypi"}, now); err != nil {
		return fmt.Errorf("demo: %w", err)
	}
	var lastHeard []model.Sighting
	for _, g := range gadgets {
		lastHeard = append(lastHeard, model.Sighting{MAC: g.mac, IP: g.ip, Iface: bridge, Seen: now.Add(-g.quiet).UnixMilli()})
	}
	if err := st.SavePresence(append(heard(now, true), lastHeard...)); err != nil {
		return fmt.Errorf("demo: %w", err)
	}
	var history []model.FlowDelta
	// Five weeks of history, so the longer time ranges have something to
	// show and every machine is past its learning period: hourly totals for
	// all but the last two days, as a real database would hold.
	detailFrom := now.Add(-48 * time.Hour).Truncate(time.Hour)
	for t := now.Add(-35 * 24 * time.Hour).Truncate(time.Hour); t.Before(detailFrom); t = t.Add(time.Hour) {
		history = append(history, hour(l, t, now)...)
	}
	for t := detailFrom; t.Before(now); t = t.Add(time.Minute) {
		history = append(history, minute(l, t, now, 1)...)
	}
	if err := st.Apply(history); err != nil {
		return fmt.Errorf("demo: %w", err)
	}

	const every = 2 * time.Second
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case t := <-tick.C:
			// Lean towards busier than the backfill so the page visibly moves.
			if err := st.Apply(minute(l, t, now, 4*every.Seconds()/60)); err != nil {
				return fmt.Errorf("demo: %w", err)
			}
			if err := st.SavePresence(heard(t, false)); err != nil {
				return fmt.Errorf("demo: %w", err)
			}
		}
	}
}

// Owners is a made-up table of who owns the demo's internet addresses, in
// the same format as the real one.
func Owners() *owners.Table {
	table, err := owners.Parse(strings.NewReader(
		"1.1.1.0\t1.1.1.255\t13335\tUS\tCLOUDFLARENET - Cloudflare, Inc.\n" +
			"9.9.9.0\t9.9.9.255\t19281\tUS\tQUAD9-AS-1 - Quad9\n" +
			"192.0.2.0\t192.0.2.127\t64503\tUS\tEXAMPLE-RELAY - Example Relay Networks\n" +
			"192.0.2.128\t192.0.2.255\t64504\tDE\tEXAMPLE-HOST - Example Hosting GmbH\n" +
			"198.51.100.0\t198.51.100.255\t64500\tUS\tEXAMPLE-CLOUD - Example Cloud Hosting\n" +
			"203.0.113.0\t203.0.113.127\t64501\tDE\tEXAMPLE-TELECOM - Example Telecom GmbH\n" +
			"203.0.113.128\t203.0.113.255\t64502\tNL\tEXAMPLE-CDN - Example Content Delivery B.V.\n"))
	if err != nil {
		panic(err)
	}
	return table
}
