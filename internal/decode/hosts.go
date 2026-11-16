package decode

import (
	"encoding/binary"
	"net/netip"
	"strings"
)

// Devices tell the network what they are called in two common ways, both
// sent to everyone on the network and so visible on the bridge: mDNS
// (Bonjour) announcements, and the hostname in a DHCP request.

// HostAddr is a name a device announced for one of its addresses.
type HostAddr struct {
	Name string
	Addr netip.Addr
}

// ParseMDNSHosts returns the "name.local" address records in an mDNS
// message, with ".local" removed.
func ParseMDNSHosts(msg []byte) []HostAddr {
	if len(msg) < 12 || binary.BigEndian.Uint16(msg[2:4])&0x8000 == 0 { // responses only
		return nil
	}
	questions := int(binary.BigEndian.Uint16(msg[4:6]))
	records := int(binary.BigEndian.Uint16(msg[6:8])) + int(binary.BigEndian.Uint16(msg[8:10])) + int(binary.BigEndian.Uint16(msg[10:12]))
	off := 12
	var ok bool
	for i := 0; i < questions; i++ {
		if _, off, ok = readName(msg, off); !ok {
			return nil
		}
		off += 4
	}
	var out []HostAddr
	for i := 0; i < records && i < 64; i++ {
		var owner string
		if owner, off, ok = readName(msg, off); !ok || off+10 > len(msg) {
			break
		}
		rtype := binary.BigEndian.Uint16(msg[off : off+2])
		rdlen := int(binary.BigEndian.Uint16(msg[off+8 : off+10]))
		off += 10
		if off+rdlen > len(msg) {
			break
		}
		var addr netip.Addr
		switch {
		case rtype == 1 && rdlen == 4:
			addr = netip.AddrFrom4([4]byte(msg[off : off+4]))
		case rtype == 28 && rdlen == 16:
			addr = netip.AddrFrom16([16]byte(msg[off : off+16]))
		}
		off += rdlen
		if !addr.IsValid() || addr.IsLinkLocalUnicast() {
			continue
		}
		host, isLocal := strings.CutSuffix(strings.ToLower(owner), ".local")
		if name := cleanDeviceName(owner[:len(host)]); isLocal && name != "" {
			out = append(out, HostAddr{Name: name, Addr: addr})
		}
	}
	return out
}

// ParseDHCPRequest returns the hostname a device gave when asking for an
// address, and the address it asked for.
func ParseDHCPRequest(msg []byte) (HostAddr, bool) {
	// Fixed BOOTP header, then the magic cookie, then options.
	if len(msg) < 240 || msg[0] != 1 || binary.BigEndian.Uint32(msg[236:240]) != 0x63825363 {
		return HostAddr{}, false
	}
	addr := netip.AddrFrom4([4]byte(msg[12:16])) // the address it already has, when renewing
	var name string
	var msgType byte
	for opts := msg[240:]; len(opts) >= 2; {
		code := opts[0]
		if code == 255 {
			break
		}
		if code == 0 { // padding
			opts = opts[1:]
			continue
		}
		n := int(opts[1])
		if len(opts) < 2+n {
			break
		}
		val := opts[2 : 2+n]
		switch {
		case code == 53 && n == 1:
			msgType = val[0]
		case code == 12:
			name = cleanDeviceName(string(val))
		case code == 50 && n == 4:
			addr = netip.AddrFrom4([4]byte(val))
		}
		opts = opts[2+n:]
	}
	// Only a REQUEST (3) or INFORM (8) names an address the device will use.
	if (msgType != 3 && msgType != 8) || name == "" || addr.IsUnspecified() {
		return HostAddr{}, false
	}
	return HostAddr{Name: name, Addr: addr}, true
}

// cleanDeviceName accepts a hostname as a device would announce it, keeping
// its capitalisation, and rejects anything else. Announcements come from
// any device on the network, so nothing is taken on trust.
func cleanDeviceName(s string) string {
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 63 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return ""
		}
	}
	return s
}
