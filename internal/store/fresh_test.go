package store

import (
	"context"
	"testing"
	"time"

	"github.com/istorry/tattle/internal/model"
)

// visit is one connection at a given time before now.
func visit(now time.Time, ago time.Duration, clientID, clientIP, serverID, serverIP, domain string) model.FlowDelta {
	at := now.Add(-ago)
	return model.FlowDelta{Bucket: at.Unix() / 60 * 60, ClientID: clientID, ClientIP: clientIP, ServerID: serverID, ServerIP: serverIP,
		ServerPort: 443, Proto: "tcp", Domain: domain, BytesUp: 10, BytesDown: 20, Packets: 2, Conns: 1,
		FirstSeen: at.UnixMilli(), LastSeen: at.UnixMilli()}
}

func freshStore(t *testing.T) (*Store, time.Time) {
	t.Helper()
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(); SetLocal(nil, true) })
	now := time.Now()
	h := time.Hour
	err = st.Apply([]model.FlowDelta{
		// vm-100 has been around for 30 hours.
		visit(now, 30*h, "vm-100", "10.0.0.2", "", "198.51.100.1", "usual.example"),
		visit(now, 20*h, "vm-100", "10.0.0.2", "", "198.51.100.2", "while-learning.example"),
		visit(now, 4*h, "vm-100", "10.0.0.2", "", "198.51.100.3", "new.example"),
		visit(now, 1*h, "vm-100", "10.0.0.2", "", "198.51.100.1", "usual.example"),
		visit(now, 1*h, "", "203.0.113.9", "vm-100", "10.0.0.2", ""),
		visit(now, 2*h, "vm-100", "10.0.0.2", "", "10.0.0.77", ""),
		// ct-9 only appeared two hours ago.
		visit(now, 2*h, "ct-9", "10.0.0.9", "", "198.51.100.4", "brand-new-machine.example"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, now
}

func firstNames(f Fresh) []string {
	var out []string
	for _, x := range f.Firsts {
		out = append(out, x.Direction+" "+x.Name)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWhatsNew(t *testing.T) {
	st, now := freshStore(t)
	ctx := context.Background()

	f, err := st.Fresh(ctx, now.Add(-6*time.Hour), now, "", ScopeAll, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Newest first. Not the usual site, not what was seen while learning,
	// and nothing from the machine that is still learning.
	if got := firstNames(f); !equal(got, []string{"in 203.0.113.9", "out 10.0.0.77", "out new.example"}) {
		t.Errorf("firsts = %v", got)
	}
	in := f.Firsts[0]
	if in.Machine != "vm-100" || in.Local || in.IP != "203.0.113.9" || in.Port != 443 {
		t.Errorf("incoming first = %+v", in)
	}
	if !f.Firsts[1].Local || f.Firsts[2].IP != "198.51.100.3" {
		t.Errorf("firsts = %+v", f.Firsts)
	}
	if len(f.Learning) != 1 || f.Learning[0].Machine != "ct-9" {
		t.Fatalf("learning = %+v", f.Learning)
	}
	if until := time.UnixMilli(f.Learning[0].Until); until.Sub(now).Round(time.Hour) != 22*time.Hour {
		t.Errorf("ct-9 learns until %v from now", until.Sub(now))
	}

	// The time range, the machine, hiding a side and the limit all apply.
	if f, _ := st.Fresh(ctx, now.Add(-90*time.Minute), now, "", ScopeAll, 10); !equal(firstNames(f), []string{"in 203.0.113.9"}) {
		t.Errorf("last 90 minutes = %v", firstNames(f))
	}
	if f, _ := st.Fresh(ctx, now.Add(-6*time.Hour), now, "ct-9", ScopeAll, 10); len(f.Firsts) != 0 || len(f.Learning) != 1 {
		t.Errorf("ct-9 = %+v", f)
	}
	if f, _ := st.Fresh(ctx, now.Add(-6*time.Hour), now, "", ScopeInternet, 10); !equal(firstNames(f), []string{"in 203.0.113.9", "out new.example"}) {
		t.Errorf("internet only = %v", firstNames(f))
	}
	if f, _ := st.Fresh(ctx, now.Add(-6*time.Hour), now, "", ScopeAll, 2); len(f.Firsts) != 2 || f.More != 1 {
		t.Errorf("limited: %d listed, %d more", len(f.Firsts), f.More)
	}

	// The lists carry the same judgement.
	peers, err := st.Peers(ctx, now.Add(-6*time.Hour), "", ScopeAll, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range peers {
		want := p.Name == "new.example" || p.Name == "203.0.113.9" || p.Name == "10.0.0.77"
		if p.New != want {
			t.Errorf("%s %s: new = %v", p.Direction, p.Name, p.New)
		}
	}
}

func TestSightingsOutliveTheHistory(t *testing.T) {
	st, now := freshStore(t)
	ctx := context.Background()
	// All traffic history is deleted, as happens when it ages out.
	if err := st.Prune(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if peers, _ := st.Peers(ctx, now.Add(-48*time.Hour), "", ScopeAll, 100); len(peers) != 0 {
		t.Fatalf("history was not pruned: %d peers", len(peers))
	}
	// The usual site comes back and is not announced as new.
	later := now.Add(time.Minute)
	if err := st.Apply([]model.FlowDelta{visit(later, 0, "vm-100", "10.0.0.2", "", "198.51.100.1", "usual.example")}); err != nil {
		t.Fatal(err)
	}
	if f, _ := st.Fresh(ctx, now.Add(-10*time.Minute), later, "vm-100", ScopeAll, 10); len(f.Firsts) != 0 {
		t.Errorf("a familiar place was called new: %v", firstNames(f))
	}
}

func TestSightingsAreBuiltFromOlderDatabases(t *testing.T) {
	st, now := freshStore(t)
	ctx := context.Background()
	// A database from before sightings were recorded has traffic but no record.
	if _, err := st.db.Exec(`DELETE FROM seen; DELETE FROM learning`); err != nil {
		t.Fatal(err)
	}
	if err := backfillSightings(st.db); err != nil {
		t.Fatal(err)
	}
	f, err := st.Fresh(ctx, now.Add(-6*time.Hour), now, "", ScopeAll, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := firstNames(f); !equal(got, []string{"in 203.0.113.9", "out 10.0.0.77", "out new.example"}) {
		t.Errorf("after rebuilding: %v", got)
	}
	if len(f.Learning) != 1 || f.Learning[0].Machine != "ct-9" {
		t.Errorf("learning = %+v", f.Learning)
	}
	// Running it again changes nothing.
	if err := backfillSightings(st.db); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.Fresh(ctx, now.Add(-6*time.Hour), now, "", ScopeAll, 10); len(again.Firsts) != 3 {
		t.Errorf("second run changed the result: %v", firstNames(again))
	}
}

func TestMachineToMachineIsOneSighting(t *testing.T) {
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	h := time.Hour
	err = st.Apply([]model.FlowDelta{
		visit(now, 30*h, "vm-1", "10.0.0.1", "", "198.51.100.1", "usual.example"),
		visit(now, 30*h, "vm-2", "10.0.0.2", "", "198.51.100.1", "usual.example"),
		visit(now, 1*h, "vm-1", "10.0.0.1", "vm-2", "10.0.0.2", ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := st.Fresh(context.Background(), now.Add(-6*h), now, "", ScopeAll, 10)
	if len(f.Firsts) != 1 || f.Firsts[0].Machine != "vm-1" || f.Firsts[0].PeerID != "vm-2" || !f.Firsts[0].Local {
		t.Errorf("firsts = %+v", f.Firsts)
	}
}
