package tracker

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/istorry/tattle/internal/decode"
	"github.com/istorry/tattle/internal/decode/quictest"
	"github.com/istorry/tattle/internal/model"
)

var (
	vmMAC     = decode.MAC{0xbc, 0x24, 0x11, 0, 1, 0}
	ctMAC     = decode.MAC{0xbc, 0x24, 0x11, 0, 2, 0}
	routerVM  = decode.MAC{0xbc, 0x24, 0x11, 0, 3, 0}
	gateway   = decode.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	hostMAC   = decode.MAC{0xbc, 0x24, 0x11, 0, 0, 0x10}
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
		{ID: model.HostID, MACs: []string{hostMAC.String()}, IPs: []string{"192.168.1.10", "10.10.10.1"}},
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

// bigHello builds a TLS ClientHello with the server name after `padding`
// bytes of other extensions, as browsers with large key shares send.
func bigHello(host string, padding int) []byte {
	u16 := func(v int) []byte { return binary.BigEndian.AppendUint16(nil, uint16(v)) }
	cat := func(parts ...[]byte) (out []byte) {
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	sni := cat(u16(0), u16(len(host)+5), u16(len(host)+3), []byte{0}, u16(len(host)), []byte(host))
	exts := cat(u16(0x33), u16(padding), make([]byte, padding), sni)
	body := cat([]byte{3, 3}, make([]byte, 32), []byte{0}, u16(2), []byte{0x13, 0x01}, []byte{1, 0}, u16(len(exts)), exts)
	hs := cat([]byte{1, 0}, u16(len(body)), body)
	return cat([]byte{0x16, 3, 1}, u16(len(hs)), hs)
}

// sendHello delivers a hello to a fresh connection as the given byte ranges,
// in the order listed, and returns the name the tracker settled on.
func sendHello(t *testing.T, tr *Tracker, cport uint16, hello []byte, pieces [][2]int) string {
	t.Helper()
	const isn = 0xffffff00 // close to wrap-around, so sequence numbers overflow mid-hello
	send := func(seq uint32, flags uint8, payload []byte) {
		p := pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.30", decode.ProtoTCP, cport, 443, flags, payload)
		p.Seq = seq
		tr.Handle(p, 60+len(payload), t0)
	}
	send(isn, decode.FlagSYN, nil)
	for _, r := range pieces {
		send(isn+1+uint32(r[0]), decode.FlagACK, hello[r[0]:r[1]])
	}
	return only(t, tr.Flush(t0)).Domain
}

func TestNameIsReadFromASplitHandshake(t *testing.T) {
	hello := bigHello("split.example.com", 3600)
	n := len(hello)
	cases := map[string][][2]int{
		"two packets":           {{0, 1400}, {1400, n}},
		"three packets":         {{0, 1400}, {1400, 2800}, {2800, n}},
		"out of order":          {{0, 1400}, {2800, n}, {1400, 2800}},
		"first packet repeated": {{0, 1400}, {0, 1400}, {1400, 2800}, {2800, n}},
		"overlapping repeat":    {{0, 1400}, {1000, 2800}, {2800, n}},
		"tiny first packet":     {{0, 3}, {3, 1400}, {1400, n}},
	}
	port := uint16(52000)
	for name, pieces := range cases {
		tr := newTracker()
		port++
		if got := sendHello(t, tr, port, hello, pieces); got != "split.example.com" {
			t.Errorf("%s: name = %q", name, got)
		}
		if tr.hellos != 0 {
			t.Errorf("%s: %d handshakes still buffered", name, tr.hellos)
		}
	}
}

func TestSplitHandshakeGivesUpCleanly(t *testing.T) {
	hello := bigHello("split.example.com", 3600)

	// The rest never arrives: the buffer is released when the connection is.
	tr := newTracker()
	if got := sendHello(t, tr, 53000, hello, [][2]int{{0, 1400}}); got != "" {
		t.Errorf("partial hello produced %q", got)
	}
	if tr.hellos != 1 {
		t.Fatalf("hellos = %d, want 1", tr.hellos)
	}
	tr.Flush(t0.Add(10 * time.Minute))
	if tr.hellos != 0 || len(tr.flows) != 0 {
		t.Errorf("after expiry: hellos = %d, flows = %d", tr.hellos, len(tr.flows))
	}

	// A frame the capture cut short leaves a hole, so joining is not attempted.
	tr = newTracker()
	p := pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.30", decode.ProtoTCP, 53001, 443, decode.FlagACK, hello[:1400])
	p.Truncated = true
	tr.Handle(p, 9000, t0)
	if tr.hellos != 0 {
		t.Errorf("buffered a truncated frame")
	}

	// Many connections that never finish cannot grow memory without limit.
	tr = newTracker()
	for i := 0; i < maxHellosAtOnce+50; i++ {
		p := pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.30", decode.ProtoTCP, uint16(1024+i), 443, decode.FlagACK, hello[:1400])
		tr.Handle(p, 1460, t0)
	}
	if tr.hellos != maxHellosAtOnce {
		t.Errorf("hellos = %d, want the cap %d", tr.hellos, maxHellosAtOnce)
	}
}

func TestNameIsReadFromHTTP3(t *testing.T) {
	// A handshake too big for one packet: its pieces are spread over two
	// QUIC packets and shuffled, which is what current browsers send.
	hello := quictest.Hello("h3.example.com", 1500)
	n := len(hello)
	dcid := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	first := quictest.ClientInitial(quictest.V1, dcid, 0, []quictest.Frag{
		{Offset: 700, Data: hello[700:1000]}, {Offset: 0, Data: hello[:400]},
	}, 1200)
	second := quictest.ClientInitial(quictest.V1, dcid, 1, []quictest.Frag{
		{Offset: 1000, Data: hello[1000:n]}, {Offset: 400, Data: hello[400:700]},
	}, 1200)

	udp := func(tr *Tracker, cport uint16, payload []byte) {
		tr.Handle(pkt(vmMAC, gateway, "192.168.1.20", "198.51.100.30", decode.ProtoUDP, cport, 443, 0, payload), 42+len(payload), t0)
	}

	tr := newTracker()
	udp(tr, 54000, first)
	if tr.hellos != 1 {
		t.Fatalf("after the first packet: hellos = %d, want 1", tr.hellos)
	}
	udp(tr, 54000, second)
	if d := only(t, tr.Flush(t0)); d.Domain != "h3.example.com" || d.Proto != "udp" {
		t.Errorf("got %+v", d)
	}
	if tr.hellos != 0 {
		t.Errorf("hellos = %d after the name was found", tr.hellos)
	}

	// Packets in the other order work too.
	tr = newTracker()
	udp(tr, 54001, second)
	udp(tr, 54001, first)
	if d := only(t, tr.Flush(t0)); d.Domain != "h3.example.com" {
		t.Errorf("reversed packets: %+v", d)
	}

	// If the second packet never comes, the pieces are let go once the
	// connection has moved on to ordinary data.
	tr = newTracker()
	udp(tr, 54002, first)
	for i := 0; i < sniffPackets; i++ {
		udp(tr, 54002, []byte{0x40, 1, 2, 3, 4, 5})
	}
	if d := only(t, tr.Flush(t0)); d.Domain != "" || tr.hellos != 0 {
		t.Errorf("abandoned handshake: domain %q, hellos %d", d.Domain, tr.hellos)
	}

	// Other UDP traffic to port 443 is left alone.
	tr = newTracker()
	udp(tr, 54003, make([]byte, 1200))
	if tr.hellos != 0 {
		t.Errorf("buffered non-QUIC data")
	}
}

// Interface numbers for a host with an outside bridge and an internal one.
const (
	outside  = 0
	internal = 1
)

// on returns a packet as seen on the given bridge.
func on(iface int, p decode.Packet) decode.Packet {
	p.Iface = iface
	return p
}

func TestRoutedConnectionIsCountedOnce(t *testing.T) {
	// The guest's packet crosses the internal bridge to the host, and the
	// host sends it on, unchanged, across the outside bridge.
	inner := func(flags uint8) decode.Packet {
		return on(internal, pkt(vmMAC, hostMAC, "10.10.10.20", "198.51.100.30", decode.ProtoTCP, 51000, 443, flags, nil))
	}
	outer := func(flags uint8) decode.Packet {
		return on(outside, pkt(hostMAC, gateway, "10.10.10.20", "198.51.100.30", decode.ProtoTCP, 51000, 443, flags, nil))
	}
	replyOuter := on(outside, pkt(gateway, hostMAC, "198.51.100.30", "10.10.10.20", decode.ProtoTCP, 443, 51000, decode.FlagACK, nil))
	replyInner := on(internal, pkt(hostMAC, vmMAC, "198.51.100.30", "10.10.10.20", decode.ProtoTCP, 443, 51000, decode.FlagACK, nil))

	orders := map[string][]decode.Packet{
		"inside first":  {inner(decode.FlagSYN), outer(decode.FlagSYN), replyOuter, replyInner},
		"outside first": {outer(decode.FlagSYN), inner(decode.FlagSYN), replyInner, replyOuter},
	}
	for name, packets := range orders {
		tr := newTracker()
		for _, p := range packets {
			size := 100
			if p.SrcPort == 443 {
				size = 1000
			}
			tr.Handle(p, size, t0)
		}
		d := only(t, tr.Flush(t0))
		if d.ClientID != "vm-100" || d.BytesUp != 100 || d.BytesDown != 1000 || d.Packets != 2 || d.Conns != 1 {
			t.Errorf("%s: %+v", name, d)
		}
	}
}

func TestRoutingBetweenGuestsOnOneBridgeIsCountedOnce(t *testing.T) {
	// Two VLANs on the same bridge: the packet enters the host and leaves
	// again on the same interface.
	tr := newTracker()
	tr.Handle(pkt(vmMAC, hostMAC, "10.10.10.20", "192.168.1.53", decode.ProtoTCP, 51000, 80, decode.FlagSYN, nil), 100, t0)
	tr.Handle(pkt(hostMAC, ctMAC, "10.10.10.20", "192.168.1.53", decode.ProtoTCP, 51000, 80, decode.FlagSYN, nil), 100, t0)
	tr.Handle(pkt(ctMAC, hostMAC, "192.168.1.53", "10.10.10.20", decode.ProtoTCP, 80, 51000, decode.FlagACK, nil), 700, t0)
	tr.Handle(pkt(hostMAC, vmMAC, "192.168.1.53", "10.10.10.20", decode.ProtoTCP, 80, 51000, decode.FlagACK, nil), 700, t0)
	d := only(t, tr.Flush(t0))
	if d.ClientID != "vm-100" || d.ServerID != "ct-200" || d.BytesUp != 100 || d.BytesDown != 700 {
		t.Errorf("got %+v", d)
	}
}

func TestMasqueradedConnectionIsNotAlsoCountedForTheHost(t *testing.T) {
	tr := newTracker()
	// The guest's connection, then the same connection leaving from the
	// host's address with the source port kept.
	tr.Handle(on(internal, pkt(vmMAC, hostMAC, "10.10.10.20", "198.51.100.30", decode.ProtoTCP, 51000, 443, decode.FlagSYN, nil)), 100, t0)
	tr.Handle(on(outside, pkt(hostMAC, gateway, "192.168.1.10", "198.51.100.30", decode.ProtoTCP, 51000, 443, decode.FlagSYN, nil)), 100, t0)
	tr.Handle(on(outside, pkt(gateway, hostMAC, "198.51.100.30", "192.168.1.10", decode.ProtoTCP, 443, 51000, decode.FlagACK, nil)), 900, t0)
	tr.Handle(on(internal, pkt(hostMAC, vmMAC, "198.51.100.30", "10.10.10.20", decode.ProtoTCP, 443, 51000, decode.FlagACK, nil)), 900, t0)
	// Something the host really did itself, to the same place.
	tr.Handle(on(outside, pkt(hostMAC, gateway, "192.168.1.10", "198.51.100.30", decode.ProtoTCP, 40123, 443, decode.FlagSYN, nil)), 60, t0)

	got := map[string]model.FlowDelta{}
	for _, d := range tr.Flush(t0) {
		got[d.ClientID] = d
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if d := got["vm-100"]; d.BytesUp != 100 || d.BytesDown != 900 {
		t.Errorf("guest: %+v", d)
	}
	if d := got[model.HostID]; d.BytesUp != 60 || d.BytesDown != 0 || d.Conns != 1 {
		t.Errorf("host's own connection: %+v", d)
	}

	// The copy stays out on later flushes too.
	tr.Handle(on(outside, pkt(gateway, hostMAC, "198.51.100.30", "192.168.1.10", decode.ProtoTCP, 443, 51000, decode.FlagACK, nil)), 500, t0)
	tr.Handle(on(internal, pkt(hostMAC, vmMAC, "198.51.100.30", "10.10.10.20", decode.ProtoTCP, 443, 51000, decode.FlagACK, nil)), 500, t0)
	if d := only(t, tr.Flush(t0)); d.ClientID != "vm-100" || d.BytesDown != 500 {
		t.Errorf("later flush: %+v", d)
	}
}

func TestPortForwardIsCountedForTheGuestOnly(t *testing.T) {
	tr := newTracker()
	// A laptop connects to port 8080 on the host, which forwards it to
	// port 80 on a container, keeping the laptop's address and port.
	tr.Handle(on(outside, pkt(laptopMAC, hostMAC, "192.168.1.42", "192.168.1.10", decode.ProtoTCP, 60000, 8080, decode.FlagSYN, nil)), 100, t0)
	tr.Handle(on(internal, pkt(hostMAC, ctMAC, "192.168.1.42", "192.168.1.53", decode.ProtoTCP, 60000, 80, decode.FlagSYN, nil)), 100, t0)
	d := only(t, tr.Flush(t0))
	if d.ServerID != "ct-200" || d.ServerPort != 80 || d.ClientIP != "192.168.1.42" || d.BytesUp != 100 {
		t.Errorf("got %+v", d)
	}

	// A connection to the host itself is still the host's.
	tr.Handle(on(outside, pkt(laptopMAC, hostMAC, "192.168.1.42", "192.168.1.10", decode.ProtoTCP, 60001, 8006, decode.FlagSYN, nil)), 100, t0)
	if d := only(t, tr.Flush(t0)); d.ServerID != model.HostID || d.ServerPort != 8006 {
		t.Errorf("host service: %+v", d)
	}
}

// With the kernel's bridge firewall hooks loaded (as on Docker hosts, and on
// Proxmox when its firewall is on), addresses are translated before a
// bridge's packets are captured, so one view can see a connection's two
// directions under different addresses.
func TestTranslationBeforeCapture(t *testing.T) {
	// Masquerade: replies show up on the outside bridge already addressed
	// to the guest, while its requests leave there from the host's address.
	tr := newTracker()
	tr.Handle(on(internal, pkt(vmMAC, hostMAC, "10.10.10.20", "198.51.100.30", decode.ProtoTCP, 51000, 443, decode.FlagSYN, nil)), 100, t0)
	tr.Handle(on(outside, pkt(hostMAC, gateway, "192.168.1.10", "198.51.100.30", decode.ProtoTCP, 51000, 443, decode.FlagSYN, nil)), 100, t0)
	tr.Handle(on(outside, pkt(gateway, hostMAC, "198.51.100.30", "10.10.10.20", decode.ProtoTCP, 443, 51000, decode.FlagACK, nil)), 900, t0)
	tr.Handle(on(internal, pkt(hostMAC, vmMAC, "198.51.100.30", "10.10.10.20", decode.ProtoTCP, 443, 51000, decode.FlagACK, nil)), 900, t0)
	if d := only(t, tr.Flush(t0)); d.ClientID != "vm-100" || d.BytesUp != 100 || d.BytesDown != 900 || d.Packets != 2 {
		t.Errorf("masquerade: %+v", d)
	}

	// Port forward: the guest's request is captured already re-addressed to
	// the container, and the reply already re-addressed from the host.
	const other = 2
	tr = newTracker()
	tr.Handle(on(internal, pkt(vmMAC, hostMAC, "10.10.10.20", "192.168.1.53", decode.ProtoTCP, 60000, 80, decode.FlagSYN, nil)), 100, t0)
	tr.Handle(on(other, pkt(hostMAC, ctMAC, "10.10.10.20", "192.168.1.53", decode.ProtoTCP, 60000, 80, decode.FlagSYN, nil)), 100, t0)
	tr.Handle(on(other, pkt(ctMAC, hostMAC, "192.168.1.53", "10.10.10.20", decode.ProtoTCP, 80, 60000, decode.FlagACK, nil)), 900, t0)
	tr.Handle(on(internal, pkt(hostMAC, vmMAC, "10.10.10.1", "10.10.10.20", decode.ProtoTCP, 8080, 60000, decode.FlagACK, nil)), 900, t0)
	if d := only(t, tr.Flush(t0)); d.ClientID != "vm-100" || d.ServerID != "ct-200" || d.ServerPort != 80 || d.BytesUp != 100 || d.BytesDown != 900 {
		t.Errorf("port forward: %+v", d)
	}
}

func TestPacketsSplitAcrossFlushesAreNotCountedTwice(t *testing.T) {
	// One view's copy of a packet is read just before a flush and the other
	// view's copy just after.
	tr := newTracker()
	tr.Handle(on(outside, pkt(hostMAC, gateway, "10.10.10.20", "198.51.100.30", decode.ProtoTCP, 51000, 443, decode.FlagSYN, nil)), 100, t0)
	first := tr.Flush(t0)
	tr.Handle(on(internal, pkt(vmMAC, hostMAC, "10.10.10.20", "198.51.100.30", decode.ProtoTCP, 51000, 443, decode.FlagSYN, nil)), 100, t0)
	second := tr.Flush(t0)
	var up, conns int64
	for _, d := range append(first, second...) {
		up += d.BytesUp
		conns += d.Conns
	}
	if up != 100 || conns > 1 {
		t.Errorf("sent = %d bytes over %d connections, want 100 over at most 1", up, conns)
	}
}
