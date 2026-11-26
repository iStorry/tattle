package store

import (
	"context"
	"testing"
	"time"

	"github.com/istorry/tattle/internal/model"
)

func TestMap(t *testing.T) {
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()
	ms := func(ago time.Duration) int64 { return now.Add(-ago).UnixMilli() }

	if err := st.SaveMachines([]model.Machine{
		{ID: "host", Kind: "host", Name: "pve", MACs: []string{"bc:24:11:00:00:10"}, IPs: []string{"192.168.1.10"}, Bridges: []string{"vmbr0", "vmbr1"}},
		{ID: "vm-100", Kind: "vm", VMID: 100, Name: "web", MACs: []string{"BC:24:11:00:01:00"}, IPs: []string{"192.168.1.20"}, Bridges: []string{"vmbr0"}},
	}); err != nil {
		t.Fatal(err)
	}
	const router, laptop, old, vm = "3c:52:a1:00:00:01", "a4:83:e7:00:00:42", "5e:0f:c2:00:00:77", "bc:24:11:00:01:00"
	if err := st.SavePresence([]model.Sighting{
		{MAC: router, IP: "192.168.1.1", Iface: "vmbr0", Seen: ms(time.Minute)},
		{MAC: laptop, IP: "192.168.1.42", Iface: "vmbr0", Seen: ms(20 * 24 * time.Hour)},
		{MAC: laptop, IP: "192.168.1.42", Iface: "vmbr0", Seen: ms(2 * time.Minute)},
		{MAC: laptop, IP: "2001:db8::42", Iface: "vmbr0", Seen: ms(time.Minute)},
		// This address used to be someone else's.
		{MAC: old, IP: "192.168.1.42", Iface: "vmbr0", Seen: ms(30 * time.Minute)},
		// Heard before the VM was known to be one.
		{MAC: vm, IP: "192.168.1.20", Iface: "vmbr0", Seen: ms(time.Minute)},
		// Not heard from inside the range.
		{MAC: "f4:39:09:00:00:60", IP: "192.168.1.60", Iface: "vmbr0", Seen: ms(3 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAnnounced(map[string]string{"192.168.1.42": "macbook-pro"}, now); err != nil {
		t.Fatal(err)
	}
	bucket := now.Add(-5*time.Minute).Unix() / 60 * 60
	flow := func(cid, cip, sid, sip string, bytes int64) model.FlowDelta {
		return model.FlowDelta{Bucket: bucket, ClientID: cid, ClientIP: cip, ServerID: sid, ServerIP: sip, ServerPort: 443,
			Proto: "tcp", BytesUp: bytes, Conns: 1, FirstSeen: bucket * 1000, LastSeen: bucket * 1000}
	}
	if err := st.Apply([]model.FlowDelta{
		flow("vm-100", "192.168.1.20", "", "198.51.100.1", 1000),
		flow("vm-100", "192.168.1.20", "", "198.51.100.2", 500),
		flow("", "192.168.1.42", "vm-100", "192.168.1.20", 300),
		flow("vm-100", "192.168.1.20", "host", "192.168.1.10", 70),
		flow("vm-100", "192.168.1.20", "", "192.168.9.9", 9), // local, but not on the map
	}); err != nil {
		t.Fatal(err)
	}

	m, err := st.Map(ctx, now.Add(-time.Hour), now, []string{"192.168.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]MapNode{}
	for _, n := range m.Nodes {
		nodes[n.ID] = n
	}
	if len(nodes) != 5 {
		t.Fatalf("nodes = %+v", m.Nodes)
	}
	if n := nodes["dev-"+router]; n.Kind != "router" || len(n.Bridges) != 1 || n.Bridges[0] != "vmbr0" {
		t.Errorf("router = %+v", n)
	}
	lap := nodes["dev-"+laptop]
	if lap.Kind != "device" || lap.Announced != "macbook-pro" || len(lap.IPs) != 2 || lap.IPs[0] != "192.168.1.42" ||
		lap.FirstSeen != ms(20*24*time.Hour) || lap.LastSeen != ms(time.Minute) {
		t.Errorf("laptop = %+v", lap)
	}
	if n := nodes["dev-"+old]; n.Announced != "" {
		t.Errorf("a device kept the name of an address it no longer holds: %+v", n)
	}
	if n := nodes["host"]; len(n.Bridges) != 2 {
		t.Errorf("host = %+v", n)
	}
	if _, ok := nodes["dev-"+vm]; ok {
		t.Error("a machine was also listed as a device")
	}

	links := map[[2]string]int64{}
	for _, l := range m.Links {
		links[[2]string{l.A, l.B}] = l.Bytes
	}
	want := map[[2]string]int64{
		{InternetID, "vm-100"}:      1500,
		{"dev-" + laptop, "vm-100"}: 300, // drawn to whoever held the address last
		{"host", "vm-100"}:          70,
	}
	if len(links) != len(want) {
		t.Fatalf("links = %+v", m.Links)
	}
	for k, v := range want {
		if links[k] != v {
			t.Errorf("link %v = %d, want %d", k, links[k], v)
		}
	}

	// Devices are forgotten with the rest of the history.
	if err := st.Prune(now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM presence`).Scan(&left); err != nil || left != 5 {
		t.Errorf("after pruning, %d sightings are left (%v), want 5", left, err)
	}
}

func TestMachinesFromBeforeBridgesWereRecorded(t *testing.T) {
	path := t.TempDir() + "/tattle.db"
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`ALTER TABLE machines DROP COLUMN bridges;
		INSERT INTO machines (id, kind, vmid, name, node, macs, ips, present) VALUES ('vm-100', 'vm', 100, 'web', 'pve', '[]', '[]', 1)`); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	m, err := st.Machines(context.Background(), now.Add(-time.Hour), now, ScopeAll)
	if err != nil || len(m) != 1 || m[0].Bridges == nil || len(m[0].Bridges) != 0 {
		t.Fatalf("machines = %+v, %v", m, err)
	}
}
