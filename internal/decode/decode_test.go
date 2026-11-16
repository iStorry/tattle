package decode

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

var (
	macA = MAC{0xbc, 0x24, 0x11, 0, 0, 1}
	macB = MAC{0xbc, 0x24, 0x11, 0, 0, 2}
)

func u16(v int) []byte { return binary.BigEndian.AppendUint16(nil, uint16(v)) }

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func ethernet(etherType int, payload []byte) []byte {
	return cat(macB[:], macA[:], u16(etherType), payload)
}

func ipv4(proto byte, src, dst string, payload []byte) []byte {
	s, d := netip.MustParseAddr(src).As4(), netip.MustParseAddr(dst).As4()
	h := cat([]byte{0x45, 0}, u16(20+len(payload)), []byte{0, 0, 0, 0, 64, proto, 0, 0}, s[:], d[:])
	return append(h, payload...)
}

func tcp(sport, dport int, flags byte, payload []byte) []byte {
	h := cat(u16(sport), u16(dport), make([]byte, 8), []byte{0x50, flags}, make([]byte, 6))
	return append(h, payload...)
}

func udp(sport, dport int, payload []byte) []byte {
	return cat(u16(sport), u16(dport), u16(8+len(payload)), u16(0), payload)
}

func dnsName(name string) []byte {
	var out []byte
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			out = append(out, byte(i-start))
			out = append(out, name[start:i]...)
			start = i + 1
		}
	}
	return append(out, 0)
}

func clientHello(host string, padding int) []byte {
	sni := cat(u16(0), u16(len(host)+5), u16(len(host)+3), []byte{0}, u16(len(host)), []byte(host))
	// An unrelated extension first, as real clients send them in any order.
	exts := cat(u16(0x0a), u16(padding), make([]byte, padding), sni)
	body := cat([]byte{3, 3}, make([]byte, 32), []byte{0}, u16(2), []byte{0x13, 0x01}, []byte{1, 0}, u16(len(exts)), exts)
	hs := cat([]byte{1, 0}, u16(len(body)), body)
	return cat([]byte{0x16, 3, 1}, u16(len(hs)), hs)
}

func TestDecodeTCP(t *testing.T) {
	frame := ethernet(0x0800, ipv4(ProtoTCP, "192.168.1.20", "198.51.100.30", tcp(51000, 443, FlagSYN, []byte("hi"))))
	p, ok := Decode(frame)
	if !ok {
		t.Fatal("frame was rejected")
	}
	if p.SrcMAC != macA || p.DstMAC != macB {
		t.Errorf("MACs = %v -> %v", p.SrcMAC, p.DstMAC)
	}
	if p.SrcIP.String() != "192.168.1.20" || p.DstIP.String() != "198.51.100.30" {
		t.Errorf("IPs = %v -> %v", p.SrcIP, p.DstIP)
	}
	if p.Proto != ProtoTCP || p.SrcPort != 51000 || p.DstPort != 443 || p.TCPFlags != FlagSYN {
		t.Errorf("transport = proto %d, %d -> %d, flags %#x", p.Proto, p.SrcPort, p.DstPort, p.TCPFlags)
	}
	if string(p.Payload) != "hi" {
		t.Errorf("payload = %q", p.Payload)
	}
}

func TestDecodeVLANAndPadding(t *testing.T) {
	ip := ipv4(ProtoUDP, "10.0.0.1", "10.0.0.2", udp(1234, 53, []byte("q")))
	tagged := ethernet(0x8100, cat(u16(100), u16(0x0800), ip, make([]byte, 20))) // trailing Ethernet padding
	p, ok := Decode(tagged)
	if !ok || p.DstPort != 53 || string(p.Payload) != "q" {
		t.Fatalf("got ok=%v port=%d payload=%q", ok, p.DstPort, p.Payload)
	}
}

func TestDecodeARPAnnounce(t *testing.T) {
	arp := cat(u16(1), u16(0x0800), []byte{6, 4}, u16(2), macA[:], []byte{192, 168, 1, 20}, macB[:], []byte{192, 168, 1, 1})
	p, ok := Decode(ethernet(0x0806, arp))
	if !ok || p.Announce.String() != "192.168.1.20" || p.SrcMAC != macA || p.SrcIP.IsValid() {
		t.Fatalf("got ok=%v announce=%v mac=%v", ok, p.Announce, p.SrcMAC)
	}
}

func TestDecodeIPv6NeighbourAdvertisement(t *testing.T) {
	src := netip.MustParseAddr("fe80::1").As16()
	dst := netip.MustParseAddr("ff02::1").As16()
	target := netip.MustParseAddr("2001:db8::20").As16()
	icmp := cat([]byte{136, 0, 0, 0, 0x20, 0, 0, 0}, target[:])
	ip6 := cat([]byte{0x60, 0, 0, 0}, u16(len(icmp)), []byte{ProtoICMPv6, 255}, src[:], dst[:], icmp)
	p, ok := Decode(ethernet(0x86dd, ip6))
	if !ok || p.Announce.String() != "2001:db8::20" {
		t.Fatalf("got ok=%v announce=%v", ok, p.Announce)
	}
}

func TestDecodeGarbage(t *testing.T) {
	for _, frame := range [][]byte{nil, {1, 2, 3}, ethernet(0x0800, []byte{0x45}), ethernet(0x86dd, make([]byte, 10)), ethernet(0x1234, make([]byte, 60))} {
		if _, ok := Decode(frame); ok {
			t.Errorf("accepted %x", frame)
		}
	}
}

func TestParseDNSResponse(t *testing.T) {
	// One question, then a CNAME and two address records using name compression.
	q := dnsName("www.Example.com")
	msg := cat(u16(0x1234), u16(0x8180), u16(1), u16(3), u16(0), u16(0), q, u16(1), u16(1))
	cname := dnsName("edge.example.net")
	msg = cat(msg, []byte{0xc0, 12}, u16(5), u16(1), make([]byte, 4), u16(len(cname)), cname)
	cnameAt := len(msg) - len(cname)
	ptr := []byte{0xc0 | byte(cnameAt>>8), byte(cnameAt)}
	msg = cat(msg, ptr, u16(1), u16(1), make([]byte, 4), u16(4), []byte{198, 51, 100, 7})
	v6 := netip.MustParseAddr("2001:db8::7").As16()
	msg = cat(msg, ptr, u16(28), u16(1), make([]byte, 4), u16(16), v6[:])

	name, addrs, ok := ParseDNSResponse(msg)
	if !ok || name != "www.example.com" {
		t.Fatalf("name = %q, ok = %v", name, ok)
	}
	if len(addrs) != 2 || addrs[0].String() != "198.51.100.7" || addrs[1].String() != "2001:db8::7" {
		t.Fatalf("addrs = %v", addrs)
	}

	if _, _, ok := ParseDNSResponse(msg[:20]); ok {
		t.Error("accepted a truncated message")
	}
	query := append([]byte{}, msg...)
	query[2] = 0x01 // clear the response bit
	if _, _, ok := ParseDNSResponse(query); ok {
		t.Error("accepted a query as a response")
	}
	loop := cat(u16(1), u16(0x8180), u16(1), u16(1), u16(0), u16(0), []byte{0xc0, 12})
	if _, _, ok := ParseDNSResponse(loop); ok {
		t.Error("accepted a name that points at itself")
	}
}

func TestParseSNI(t *testing.T) {
	hello := clientHello("api.GitHub.com", 40)
	if got := ParseSNI(hello); got != "api.github.com" {
		t.Errorf("SNI = %q", got)
	}
	// A name that the capture cut off is reported as unknown, never as garbage.
	for cut := 0; cut < len(hello)-1; cut++ {
		if got := ParseSNI(hello[:cut]); got != "" {
			t.Fatalf("cut at %d returned %q", cut, got)
		}
	}
	if got := ParseSNI([]byte("GET / HTTP/1.1\r\n")); got != "" {
		t.Errorf("non-TLS data returned %q", got)
	}
	if got := ParseSNI(clientHello("bad host\x00", 4)); got != "" {
		t.Errorf("invalid name returned %q", got)
	}
}

func TestParseHTTPHost(t *testing.T) {
	cases := map[string]string{
		"GET /x HTTP/1.1\r\nUser-Agent: a\r\nHost: Example.com:8080\r\n\r\n": "example.com",
		"POST / HTTP/1.1\r\nhost:plain.test\r\n\r\n":                         "plain.test",
		"GET / HTTP/1.1\r\nAccept: */*\r\n\r\nHost: body.test\r\n":           "",
		"GET / HTTP/1.1\r\nHost: [::1]:80\r\n\r\n":                           "",
		"HTTP/1.1 200 OK\r\nHost: response.test\r\n\r\n":                     "",
		"GET / HTTP/1.1\r\nHost: trunc":                                      "trunc",
	}
	for in, want := range cases {
		if got := ParseHTTPHost([]byte(in)); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestParseClientHelloAcrossPackets(t *testing.T) {
	// 1,800 bytes of other extensions put the name well past the first packet.
	hello := clientHello("split.example.com", 1800)
	if len(hello) < 1800 {
		t.Fatalf("test hello is only %d bytes", len(hello))
	}
	if name, res := ParseClientHello(hello); res != HelloFound || name != "split.example.com" {
		t.Fatalf("whole hello: %q, %v", name, res)
	}
	// Every shorter prefix must ask for more, never guess and never give up.
	for cut := 1; cut < len(hello)-len("split.example.com"); cut++ {
		if name, res := ParseClientHello(hello[:cut]); res != HelloIncomplete || name != "" {
			t.Fatalf("prefix of %d bytes: %q, %v", cut, name, res)
		}
	}

	noName := clientHello("x", 20)
	noName[len(noName)-len("x")-9] = 0xff // turn the server name extension into an unknown one
	if _, res := ParseClientHello(noName); res != HelloNone {
		t.Errorf("complete hello without a name: %v", res)
	}
	for _, other := range [][]byte{nil, []byte("GET / HTTP/1.1\r\n"), {0x17, 3, 3, 0, 5, 1, 2, 3, 4, 5}, {0x16, 3, 3, 0, 5, 2, 0, 0, 1, 0}} {
		if _, res := ParseClientHello(other); res != HelloNone {
			t.Errorf("%x: %v", other, res)
		}
	}
}
