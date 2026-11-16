package decode

import (
	"net/netip"
	"testing"
)

// rr builds a DNS resource record.
func rr(name string, rtype int, data []byte) []byte {
	return cat(dnsName(name), u16(rtype), u16(0x8001), make([]byte, 4), u16(len(data)), data)
}

func TestParseMDNSHosts(t *testing.T) {
	v6 := netip.MustParseAddr("2001:db8::42").As16()
	linkLocal := netip.MustParseAddr("fe80::1").As16()
	// An announcement as a laptop sends it: no question, an unrelated record
	// first, then its addresses among the answers and additional records.
	msg := cat(u16(0), u16(0x8400), u16(0), u16(2), u16(0), u16(3),
		rr("_airplay._tcp.local", 12, dnsName("Living Room._airplay._tcp.local")),
		rr("Macbook-Pro.local", 1, []byte{192, 168, 1, 42}),
		rr("Macbook-Pro.local", 28, v6[:]),
		rr("Macbook-Pro.local", 28, linkLocal[:]),
		rr("printer.example.com", 1, []byte{192, 168, 1, 60}),
	)
	got := ParseMDNSHosts(msg)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "Macbook-Pro" || got[0].Addr.String() != "192.168.1.42" || got[1].Addr.String() != "2001:db8::42" {
		t.Errorf("got %+v", got)
	}

	query := append([]byte{}, msg...)
	query[2] = 0
	if hosts := ParseMDNSHosts(query); hosts != nil {
		t.Errorf("a query produced names: %+v", hosts)
	}
	for cut := 0; cut < len(msg); cut += 7 {
		ParseMDNSHosts(msg[:cut]) // must not panic on cut-off messages
	}
	bad := cat(u16(0), u16(0x8400), u16(0), u16(1), u16(0), u16(0), rr("bad name!.local", 1, []byte{192, 168, 1, 9}))
	if hosts := ParseMDNSHosts(bad); len(hosts) != 0 {
		t.Errorf("accepted an invalid name: %+v", hosts)
	}
}

// dhcp builds a DHCP message from a client.
func dhcp(msgType byte, ciaddr [4]byte, options ...[]byte) []byte {
	msg := make([]byte, 240)
	msg[0] = 1
	copy(msg[12:16], ciaddr[:])
	copy(msg[236:240], []byte{0x63, 0x82, 0x53, 0x63})
	msg = append(msg, 53, 1, msgType)
	for _, o := range options {
		msg = append(msg, o...)
	}
	return append(msg, 255)
}

func opt(code byte, data []byte) []byte { return append([]byte{code, byte(len(data))}, data...) }

func TestParseDHCPRequest(t *testing.T) {
	hostname := opt(12, []byte("living-room-tv"))
	wants := opt(50, []byte{192, 168, 1, 80})

	// Joining the network: asks for an address by option.
	got, ok := ParseDHCPRequest(dhcp(3, [4]byte{}, []byte{0}, hostname, wants))
	if !ok || got.Name != "living-room-tv" || got.Addr.String() != "192.168.1.80" {
		t.Errorf("request: %+v, %v", got, ok)
	}
	// Renewing: already has the address.
	got, ok = ParseDHCPRequest(dhcp(3, [4]byte{192, 168, 1, 80}, hostname))
	if !ok || got.Addr.String() != "192.168.1.80" {
		t.Errorf("renewal: %+v, %v", got, ok)
	}

	for name, msg := range map[string][]byte{
		"a discover, before any address is settled": dhcp(1, [4]byte{}, hostname, wants),
		"no hostname":                 dhcp(3, [4]byte{}, wants),
		"no address":                  dhcp(3, [4]byte{}, hostname),
		"a hostname with odd content": dhcp(3, [4]byte{}, opt(12, []byte("<script>")), wants),
		"not DHCP":                    make([]byte, 300),
		"too short":                   {1, 2, 3},
	} {
		if got, ok := ParseDHCPRequest(msg); ok {
			t.Errorf("%s: accepted as %+v", name, got)
		}
	}
	full := dhcp(3, [4]byte{}, hostname, wants)
	for cut := 0; cut < len(full); cut += 5 {
		ParseDHCPRequest(full[:cut]) // must not panic on cut-off messages
	}
}
