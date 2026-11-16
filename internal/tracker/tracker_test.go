package tracker

import (
	"net/netip"
	"testing"
	"time"

	"github.com/istorry/tattle/internal/decode"
	"github.com/istorry/tattle/internal/model"
)

var (
	vmMAC     = decode.MAC{0xbc, 0x24, 0x11, 0, 1, 0}
	ctMAC     = decode.MAC{0xbc, 0x24, 0x11, 0, 2, 0}
	routerVM  = decode.MAC{0xbc, 0x24, 0x11, 0, 3, 0}
	gateway   = decode.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	laptopMAC = decode.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}
	t0        = time.Unix(1_800_000_000, 0)
)

func ip(s string) netip.Addr { return netip.MustParseAddr(s) }

func newTracker() *Tracker {
	tr := New()
	tr.SetMachines([]model.Machine{
		{ID: "vm-100", MACs: []string{vmMAC.String()}},
		{ID: "ct-200", MACs: []string{ctMAC.String()}, IPs: []string{"192.168.1.53"}},
		{ID: "vm-300", MACs: []string{routerVM.String()}},
	})
	return tr
}

func pkt(srcMAC, dstMAC decode.MAC, src, dst string, proto uint8, sport, dport uint16, flags uint8, payload []byte) decode.Packet {
	return decode.Packet{SrcMAC: srcMAC, DstMAC: dstMAC, SrcIP: ip(src), DstIP: ip(dst), Proto: proto,
		SrcPort: sport, DstPort: dport, TCPFlags: flags, Payload: payload}
}

func only(t *testing.T, deltas []model.FlowDelta) model.FlowDelta {
	t.Helper()
	if len(deltas) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(deltas), deltas)
	}
	return deltas[0]
}

func TestOutgoingConnectionIsAttributedAndCounted(t *testing.T) {
	tr := newTracker()
	tr.Handle(pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.30", decode.ProtoTCP, 51000, 443, decode.FlagSYN, nil), 74, t0)
	tr.Handle(pkt(gateway, vmMAC, "198.51.100.30", "192.168.1.20", decode.ProtoTCP, 443, 51000, decode.FlagSYN|decode.FlagACK, nil), 74, t0)
	tr.Handle(pkt(gateway, vmMAC, "198.51.100.30", "192.168.1.20", decode.ProtoTCP, 443, 51000, decode.FlagACK, nil), 1500, t0)

	d := only(t, tr.Flush(t0.Add(time.Second)))
	if d.ClientID != "vm-100" || d.ServerID != "" || d.ServerIP != "198.51.100.30" || d.ServerPort != 443 || d.Proto != "tcp" {
		t.Errorf("wrong attribution: %+v", d)
	}
	if d.BytesUp != 74 || d.BytesDown != 1574 || d.Packets != 3 || d.Conns != 1 {
		t.Errorf("wrong totals: %+v", d)
	}

	// Nothing new since the flush means nothing to report, and more traffic
	// on the same connection is not a new connection.
	if again := tr.Flush(t0.Add(2 * time.Second)); len(again) != 0 {
		t.Errorf("idle flush returned %+v", again)
	}
	tr.Handle(pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.30", decode.ProtoTCP, 51000, 443, decode.FlagACK, nil), 60, t0.Add(3*time.Second))
	if d := only(t, tr.Flush(t0.Add(4*time.Second))); d.Conns != 0 || d.BytesUp != 60 {
		t.Errorf("continued connection: %+v", d)
	}
}

func TestDirectionIsGuessedWhenTheStartWasMissed(t *testing.T) {
	tr := newTracker()
	// First packet seen is a reply from the server, mid-connection.
	tr.Handle(pkt(gateway, vmMAC, "198.51.100.30", "192.168.1.20", decode.ProtoTCP, 443, 51000, decode.FlagACK, nil), 1000, t0)
	d := only(t, tr.Flush(t0))
	if d.ClientID != "vm-100" || d.ServerPort != 443 || d.BytesDown != 1000 || d.BytesUp != 0 {
		t.Errorf("got %+v", d)
	}
}

func TestIncomingConnection(t *testing.T) {
	tr := newTracker()
	tr.Handle(pkt(laptopMAC, vmMAC, "192.168.1.42", "192.168.1.20", decode.ProtoTCP, 60000, 8123, decode.FlagSYN, nil), 74, t0)
	d := only(t, tr.Flush(t0))
	if d.ClientID != "" || d.ClientIP != "192.168.1.42" || d.ServerID != "vm-100" || d.ServerPort != 8123 {
		t.Errorf("got %+v", d)
	}
}

func TestGuestToGuestIsOneRecord(t *testing.T) {
	tr := newTracker()
	tr.Handle(pkt(vmMAC, ctMAC, "192.168.1.20", "192.168.1.53", decode.ProtoUDP, 40000, 53, 0, nil), 80, t0)
	tr.Handle(pkt(ctMAC, vmMAC, "192.168.1.53", "192.168.1.20", decode.ProtoUDP, 53, 40000, 0, nil), 120, t0)
	d := only(t, tr.Flush(t0))
	if d.ClientID != "vm-100" || d.ServerID != "ct-200" || d.BytesUp != 80 || d.BytesDown != 120 {
		t.Errorf("got %+v", d)
	}
}

func TestNamesComeFromDNSThenFromTheConnection(t *testing.T) {
	tr := newTracker()
	// The resolver tells the VM that example.com is 198.51.100.7.
	answer := []byte{0, 1, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0,
		7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1,
		0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 198, 51, 100, 7}
	tr.Handle(pkt(gateway, vmMAC, "192.168.1.1", "192.168.1.20", decode.ProtoUDP, 53, 40000, 0, answer), 100, t0)
	tr.Flush(t0)

	tr.Handle(pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.7", decode.ProtoTCP, 51001, 443, decode.FlagSYN, nil), 74, t0)
	if d := only(t, tr.Flush(t0)); d.Domain != "example.com" {
		t.Errorf("DNS name not applied: %+v", d)
	}

	// A plain HTTP request names the site itself, which wins over DNS.
	req := []byte("GET / HTTP/1.1\r\nHost: other.example.org\r\n\r\n")
	tr.Handle(pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.7", decode.ProtoTCP, 51002, 80, decode.FlagSYN, nil), 74, t0)
	tr.Handle(pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.7", decode.ProtoTCP, 51002, 80, decode.FlagACK, req), 120, t0)
	if d := only(t, tr.Flush(t0)); d.Domain != "other.example.org" {
		t.Errorf("Host header not applied: %+v", d)
	}
}

func TestTrafficBetweenStrangersIsIgnored(t *testing.T) {
	tr := newTracker()
	tr.Handle(pkt(laptopMAC, gateway, "192.168.1.42", "198.51.100.30", decode.ProtoTCP, 50000, 443, decode.FlagSYN, nil), 74, t0)
	broadcast := decode.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	tr.Handle(pkt(vmMAC, broadcast, "192.168.1.20", "192.168.1.255", decode.ProtoUDP, 137, 137, 0, nil), 90, t0)
	if d := tr.Flush(t0); len(d) != 0 {
		t.Errorf("got %+v", d)
	}
}

func TestRouterVMIsNotBlamedForForwardedTraffic(t *testing.T) {
	tr := newTracker()
	// The router VM announces its own address...
	tr.Handle(decode.Packet{SrcMAC: routerVM, Announce: ip("192.168.1.254")}, 60, t0)
	// ...then forwards a laptop's connection out through its network card.
	tr.Handle(pkt(laptopMAC, routerVM, "192.168.1.42", "198.51.100.30", decode.ProtoTCP, 50000, 443, decode.FlagSYN, nil), 74, t0)
	if d := tr.Flush(t0); len(d) != 0 {
		t.Errorf("forwarded traffic was attributed: %+v", d)
	}
	// Its own traffic still is.
	tr.Handle(pkt(routerVM, gateway, "192.168.1.254", "198.51.100.99", decode.ProtoTCP, 50001, 443, decode.FlagSYN, nil), 74, t0)
	if d := only(t, tr.Flush(t0)); d.ClientID != "vm-300" {
		t.Errorf("got %+v", d)
	}
	if got := tr.MachineIPs()["vm-300"]; len(got) != 1 || got[0] != "192.168.1.254" {
		t.Errorf("learned addresses = %v", got)
	}
}

func TestFlowsExpire(t *testing.T) {
	tr := newTracker()
	tr.Handle(pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.30", decode.ProtoTCP, 51000, 443, decode.FlagSYN, nil), 74, t0)
	tr.Handle(pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.30", decode.ProtoTCP, 51000, 443, decode.FlagFIN|decode.FlagACK, nil), 60, t0)
	tr.Handle(pkt(vmMAC, gateway, "192.168.1.20", "9.9.9.9", decode.ProtoUDP, 40000, 53, 0, nil), 80, t0)
	tr.Flush(t0)
	if len(tr.flows) != 2 {
		t.Fatalf("flows = %d, want 2", len(tr.flows))
	}
	tr.Flush(t0.Add(10 * time.Second)) // closed TCP connection is forgotten quickly
	if len(tr.flows) != 1 {
		t.Fatalf("flows = %d, want 1", len(tr.flows))
	}
	tr.Flush(t0.Add(2 * time.Minute))
	if len(tr.flows) != 0 {
		t.Fatalf("flows = %d, want 0", len(tr.flows))
	}
}
