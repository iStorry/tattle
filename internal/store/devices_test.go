package store

import (
	"context"
	"testing"
	"time"
)

func TestDeviceNames(t *testing.T) {
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()
	byIP := func() map[string]Device {
		list, err := st.Devices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]Device{}
		for _, d := range list {
			out[d.IP] = d
		}
		return out
	}

	if err := st.SaveAnnounced(map[string]string{"192.168.1.42": "macbook-pro", "192.168.1.80": "living-room-tv"}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.NameDevice("192.168.1.42", "Laptop"); err != nil {
		t.Fatal(err)
	}
	if err := st.NameDevice("192.168.1.91", "Hallway sensor"); err != nil {
		t.Fatal(err)
	}
	got := byIP()
	if len(got) != 3 || got["192.168.1.42"] != (Device{"192.168.1.42", "Laptop", "macbook-pro"}) ||
		got["192.168.1.80"].Announced != "living-room-tv" || got["192.168.1.91"].Name != "Hallway sensor" {
		t.Fatalf("devices = %+v", got)
	}

	// A new announcement does not replace the owner's name.
	if err := st.SaveAnnounced(map[string]string{"192.168.1.42": "someone-elses-phone"}, now); err != nil {
		t.Fatal(err)
	}
	if d := byIP()["192.168.1.42"]; d.Name != "Laptop" || d.Announced != "someone-elses-phone" {
		t.Errorf("after a new announcement: %+v", d)
	}

	// Removing the owner's name falls back to the announced one, and an
	// address with neither is forgotten.
	st.NameDevice("192.168.1.42", "")
	st.NameDevice("192.168.1.91", "")
	got = byIP()
	if len(got) != 2 || got["192.168.1.42"].Name != "" || got["192.168.1.42"].Announced == "" {
		t.Errorf("after removing names: %+v", got)
	}

	// Announced names lapse when not heard for a long time; chosen names do not.
	st.NameDevice("192.168.1.80", "TV")
	if err := st.pruneDevices(now.Add(announcedLife + time.Hour)); err != nil {
		t.Fatal(err)
	}
	got = byIP()
	if len(got) != 1 || got["192.168.1.80"] != (Device{"192.168.1.80", "TV", ""}) {
		t.Errorf("after a long silence: %+v", got)
	}

	if err := st.NameDevice("not an address", "x"); err == nil {
		t.Error("accepted a name for something that is not an address")
	}
	for in, want := range map[string]string{"  Living   room  TV ": "Living room TV", "Büro-Drucker": "Büro-Drucker", "": ""} {
		if got, err := CleanDeviceName(in); err != nil || got != want {
			t.Errorf("CleanDeviceName(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"this name goes on for far too long to be useful to anyone", "bell\x07"} {
		if _, err := CleanDeviceName(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
