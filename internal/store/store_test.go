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

	log, _ := st.Activity(ctx, since, "", ScopeLocal, "", 100)
	if len(log) != 3 {
		t.Errorf("local log has %d rows", len(log))
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

	in := Settings{LocalNets: []string{" 10.20.30.7/24 ", "10.20.30.0/24", "", "192.0.2.5", "2001:db8::/32"}, Show: ScopeInternet}
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
	if got.Show != ScopeInternet || got.PrivateIsLocal || len(got.LocalNets) != 3 {
		t.Errorf("reloaded = %+v", got)
	}

	for _, bad := range []Settings{
		{Show: "sideways"},
		{Show: ScopeAll, LocalNets: []string{"my network"}},
		{Show: ScopeAll, LocalNets: []string{"0.0.0.0/0"}},
	} {
		if _, err := bad.Validate(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
