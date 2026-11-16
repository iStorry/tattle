package store

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/istorry/tattle/internal/model"
)

func seeded(t *testing.T) (*Store, time.Time) {
	t.Helper()
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(); SetLocal(nil, true) })
	now := time.Unix(1_800_000_000, 0)
	b := now.Unix() / 60 * 60
	ms := now.UnixMilli()
	flow := func(cid, cip, sid, sip, domain string, port uint16, up, down int64) model.FlowDelta {
		return model.FlowDelta{Bucket: b, ClientID: cid, ClientIP: cip, ServerID: sid, ServerIP: sip, ServerPort: port,
			Proto: "tcp", Domain: domain, BytesUp: up, BytesDown: down, Packets: 2, Conns: 1, FirstSeen: ms, LastSeen: ms}
	}
	err = st.Apply([]model.FlowDelta{
		flow("vm-100", "10.20.30.20", "", "198.51.100.30", "api.github.com", 443, 100, 1000), // internet, outgoing
		flow("vm-100", "10.20.30.20", "", "10.20.30.91", "", 80, 10, 20),                     // local device
		flow("vm-100", "10.20.30.20", "ct-200", "10.20.30.53", "", 53, 5, 7),                 // another machine
		flow("", "203.0.113.9", "ct-200", "10.20.30.53", "", 22, 30, 40),                     // internet, incoming
		flow("ct-200", "10.20.30.53", "", "172.20.0.9", "", 443, 1, 2),                       // private, other subnet
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveMachines([]model.Machine{{ID: "vm-100", Kind: "vm", VMID: 100, Name: "a"}, {ID: "ct-200", Kind: "ct", VMID: 200, Name: "b"}}); err != nil {
		t.Fatal(err)
	}
	return st, now
}

func names(peers []Peer) map[string]bool {
	out := map[string]bool{}
	for _, p := range peers {
		out[p.Direction+" "+p.Name] = p.Local
	}
	return out
}

func TestScopeFiltersEveryView(t *testing.T) {
	st, now := seeded(t)
	ctx, since := context.Background(), now.Add(-time.Hour)
	SetLocal([]netip.Prefix{netip.MustParsePrefix("10.20.30.0/24")}, true)

	all, err := st.Peers(ctx, since, "", ScopeAll, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"out api.github.com": false, "out 10.20.30.91": true, "out 10.20.30.53": true,
		"in 203.0.113.9": false, "out 172.20.0.9": true}
	if got := names(all); len(got) != len(want) {
		t.Fatalf("all = %v", got)
	} else {
		for k, local := range want {
			if l, ok := got[k]; !ok || l != local {
				t.Errorf("%s: present=%v local=%v, want local=%v", k, ok, l, local)
			}
		}
	}

	internet, _ := st.Peers(ctx, since, "", ScopeInternet, 100)
	if got := names(internet); len(got) != 2 || got["out api.github.com"] || got["in 203.0.113.9"] {
		t.Errorf("internet only = %v", got)
	}
	local, _ := st.Peers(ctx, since, "", ScopeLocal, 100)
	if got := names(local); len(got) != 3 {
		t.Errorf("local only = %v", got)
	}

	sum := func(scope string) (sent, received int64) {
		tl, err := st.Timeline(ctx, since, now, "", scope)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range tl.Points {
			sent += p.Sent
			received += p.Received
		}
		return
	}
	if s, r := sum(ScopeInternet); s != 100+40 || r != 1000+30 {
		t.Errorf("internet timeline = %d sent, %d received", s, r)
	}
	if s, r := sum(ScopeLocal); s != 10+5+1 || r != 20+7+2 {
		t.Errorf("local timeline = %d sent, %d received", s, r)
	}

	machines, _ := st.Machines(ctx, since, now, ScopeInternet)
	if len(machines) != 2 || machines[0].Sent != 100 || machines[1].Sent != 40 || machines[0].Destinations != 1 {
		t.Errorf("internet machines = %+v", machines)
	}

	split, err := st.Split(ctx, since, "")
	if err != nil {
		t.Fatal(err)
	}
	if split.Internet != (Share{Bytes: 1170, Places: 2}) || split.Local != (Share{Bytes: 45, Places: 3}) {
		t.Errorf("split = %+v", split)
	}
}

func TestChangingTheRulesRelabelsHistory(t *testing.T) {
	st, now := seeded(t)
	ctx, since := context.Background(), now.Add(-time.Hour)

	// Only the host's own network counts: the other private subnet is now outside.
	SetLocal([]netip.Prefix{netip.MustParsePrefix("10.20.30.0/24")}, false)
	split, _ := st.Split(ctx, since, "")
	if split.Local.Places != 2 || split.Internet.Places != 3 {
		t.Errorf("split = %+v", split)
	}
	// Nothing configured and private ranges off: only machine-to-machine is local.
	SetLocal(nil, false)
	split, _ = st.Split(ctx, since, "")
	if split.Local.Places != 1 {
		t.Errorf("split = %+v", split)
	}
}

func TestSettings(t *testing.T) {
	st, _ := seeded(t)
	got, err := st.LoadSettings()
	if err != nil || !got.PrivateIsLocal || got.Show != ScopeAll || len(got.LocalNets) != 0 {
		t.Fatalf("defaults = %+v, %v", got, err)
	}

	if got.RetentionDays != DefaultRetentionDays || got.DetailDays != DefaultDetailDays {
		t.Errorf("default retention = %d", got.RetentionDays)
	}

	in := Settings{LocalNets: []string{" 10.20.30.7/24 ", "10.20.30.0/24", "", "192.0.2.5", "2001:db8::/32"}, Show: ScopeInternet, RetentionDays: 30, DetailDays: 7}
	nets, err := in.Validate()
	if err != nil {
		t.Fatal(err)
	}
	wantNets := []string{"10.20.30.0/24", "192.0.2.5/32", "2001:db8::/32"}
	if len(nets) != 3 || len(in.LocalNets) != 3 {
		t.Fatalf("nets = %v", in.LocalNets)
	}
	for i, w := range wantNets {
		if in.LocalNets[i] != w {
			t.Errorf("net %d = %q, want %q", i, in.LocalNets[i], w)
		}
	}
	if err := st.SaveSettings(in); err != nil {
		t.Fatal(err)
	}
	got, _ = st.LoadSettings()
	if got.Show != ScopeInternet || got.PrivateIsLocal || len(got.LocalNets) != 3 || got.RetentionDays != 30 {
		t.Errorf("reloaded = %+v", got)
	}

	// Settings saved by older versions keep working: one from before either
	// period existed, and one that kept fewer days than today's detail period.
	saved := func(value string) Settings {
		t.Helper()
		if _, err := st.db.Exec(`UPDATE settings SET value = ? WHERE key = 'settings'`, value); err != nil {
			t.Fatal(err)
		}
		got, err := st.LoadSettings()
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := saved(`{"localNets":[],"privateIsLocal":true,"show":"local"}`); got.Show != ScopeLocal ||
		got.RetentionDays != DefaultRetentionDays || got.DetailDays != DefaultDetailDays {
		t.Errorf("oldest saved settings = %+v", got)
	}
	if got := saved(`{"localNets":[],"privateIsLocal":true,"show":"local","retentionDays":3}`); got.Show != ScopeLocal ||
		got.RetentionDays != 3 || got.DetailDays != 3 {
		t.Errorf("settings keeping three days = %+v", got)
	}

	for _, bad := range []Settings{
		{Show: "sideways", RetentionDays: 7, DetailDays: 7},
		{Show: ScopeAll, RetentionDays: 7, DetailDays: 7, LocalNets: []string{"my network"}},
		{Show: ScopeAll, RetentionDays: 7, DetailDays: 7, LocalNets: []string{"0.0.0.0/0"}},
		{Show: ScopeAll, RetentionDays: 0, DetailDays: 1},
		{Show: ScopeAll, RetentionDays: 366, DetailDays: 7},
		{Show: ScopeAll, RetentionDays: 90, DetailDays: 0},
		{Show: ScopeAll, RetentionDays: 90, DetailDays: 31},
		{Show: ScopeAll, RetentionDays: 5, DetailDays: 7},
	} {
		if _, err := bad.Validate(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestPeerDetail(t *testing.T) {
	st, now := seeded(t)
	ctx, since := context.Background(), now.Add(-time.Hour)
	SetLocal([]netip.Prefix{netip.MustParsePrefix("10.20.30.0/24")}, true)
	// A second address and service for the same site, from another machine,
	// and an older visit outside the time range.
	b := now.Unix() / 60 * 60
	old := now.Add(-3 * time.Hour)
	err := st.Apply([]model.FlowDelta{
		{Bucket: b, ClientID: "ct-200", ClientIP: "10.20.30.53", ServerIP: "198.51.100.31", ServerPort: 22, Proto: "tcp",
			Domain: "api.github.com", BytesUp: 7, BytesDown: 9, Packets: 2, Conns: 2, FirstSeen: now.UnixMilli(), LastSeen: now.UnixMilli()},
		{Bucket: old.Unix() / 60 * 60, ClientID: "vm-100", ClientIP: "10.20.30.20", ServerIP: "198.51.100.30", ServerPort: 443, Proto: "tcp",
			Domain: "api.github.com", BytesUp: 50, BytesDown: 60, Packets: 2, Conns: 1, FirstSeen: old.UnixMilli(), LastSeen: old.UnixMilli()},
	})
	if err != nil {
		t.Fatal(err)
	}

	d, err := st.Peer(ctx, since, now, "out", "api.github.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Sent != 107 || d.Received != 1009 || d.Conns != 3 || d.Local || d.PeerID != "" {
		t.Errorf("totals = %+v", d)
	}
	if len(d.IPs) != 2 || len(d.Conversations) != 2 {
		t.Errorf("ips = %v, %d conversations", d.IPs, len(d.Conversations))
	}
	if d.FirstSeen != old.UnixMilli() || d.LastSeen != now.UnixMilli() {
		t.Errorf("first seen %d, last seen %d", d.FirstSeen, d.LastSeen)
	}
	var sent, received int64
	for _, p := range d.Timeline.Points {
		sent += p.Sent
		received += p.Received
	}
	if sent != d.Sent || received != d.Received {
		t.Errorf("timeline adds up to %d/%d, totals are %d/%d", sent, received, d.Sent, d.Received)
	}

	// Limited to one machine.
	one, _ := st.Peer(ctx, since, now, "out", "api.github.com", "ct-200")
	if one.Sent != 7 || len(one.Conversations) != 1 || one.Conversations[0].Port != 22 {
		t.Errorf("one machine = %+v", one)
	}

	// Another machine as the destination, by its address.
	guest, _ := st.Peer(ctx, since, now, "out", "10.20.30.53", "")
	if guest.PeerID != "ct-200" || !guest.Local || guest.Sent != 5 || guest.Received != 7 {
		t.Errorf("guest = %+v", guest)
	}

	// An incoming caller: amounts are from the machine's side.
	in, _ := st.Peer(ctx, since, now, "in", "203.0.113.9", "")
	if in.Sent != 40 || in.Received != 30 || in.Local || len(in.Conversations) != 1 || in.Conversations[0].ServerID != "ct-200" {
		t.Errorf("incoming = %+v", in)
	}

	// Something never seen comes back empty, not as an error.
	none, err := st.Peer(ctx, since, now, "out", "never.example", "")
	if err != nil || none.FirstSeen != 0 || none.Conns != 0 || len(none.Conversations) != 0 || len(none.Timeline.Points) == 0 {
		t.Errorf("unknown = %+v, %v", none, err)
	}
}
