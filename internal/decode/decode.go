// Package decode turns raw Ethernet frames into the few fields Tattle needs.
// It is deliberately small and allocation-free: every packet on the bridge
// goes through it.
package decode

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
)

// MAC is an Ethernet hardware address.
type MAC [6]byte

func (m MAC) String() string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", m[0], m[1], m[2], m[3], m[4], m[5])
}

// IsMulticast reports whether the address is a broadcast or multicast address.
func (m MAC) IsMulticast() bool { return m[0]&1 == 1 }

// ParseMAC parses the usual colon or dash separated form.
func ParseMAC(s string) (MAC, bool) {
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return MAC{}, false
	}
	var m MAC
	copy(m[:], hw)
	return m, true
}

// IP protocol numbers.
const (
	ProtoICMP   = 1
	ProtoTCP    = 6
	ProtoUDP    = 17
	ProtoICMPv6 = 58
)

// TCP flags.
const (
	FlagFIN = 0x01
	FlagSYN = 0x02
	FlagRST = 0x04
	FlagACK = 0x10
)

// Packet is one decoded frame.
type Packet struct {
	SrcMAC, DstMAC   MAC
	SrcIP, DstIP     netip.Addr // invalid for non-IP frames such as ARP
	Proto            uint8
	SrcPort, DstPort uint16
	TCPFlags         uint8
	Seq              uint32 // TCP sequence number of the first payload byte
	Payload          []byte // transport payload; may be cut short by the capture length
	Truncated        bool   // the capture did not keep the whole frame; set by the caller
	Iface            int    // which capture the frame came from; set by the caller

	// Announce is an address that SrcMAC claims to own, taken from ARP or
	// IPv6 neighbour discovery. It is the reliable way to learn which IP
	// belongs to which machine, because a router VM forwards packets with
	// other people's source addresses.
	Announce netip.Addr
}

// Decode parses an Ethernet frame. ok is false for frames Tattle has no use for.
func Decode(frame []byte) (p Packet, ok bool) {
	if len(frame) < 14 {
		return p, false
	}
	copy(p.DstMAC[:], frame[0:6])
	copy(p.SrcMAC[:], frame[6:12])
	etherType := binary.BigEndian.Uint16(frame[12:14])
	b := frame[14:]
	for i := 0; i < 2 && (etherType == 0x8100 || etherType == 0x88a8); i++ { // VLAN tags
		if len(b) < 4 {
			return p, false
		}
		etherType = binary.BigEndian.Uint16(b[2:4])
		b = b[4:]
	}
	switch etherType {
	case 0x0806:
		return decodeARP(p, b)
	case 0x0800:
		return decodeIPv4(p, b)
	case 0x86dd:
		return decodeIPv6(p, b)
	}
	return p, false
}

func decodeARP(p Packet, b []byte) (Packet, bool) {
	if len(b) < 28 {
		return p, false
	}
	if binary.BigEndian.Uint16(b[2:4]) != 0x0800 || b[4] != 6 || b[5] != 4 {
		return p, false
	}
	copy(p.SrcMAC[:], b[8:14])
	ip := netip.AddrFrom4([4]byte(b[14:18]))
	if ip.IsUnspecified() {
		return p, false
	}
	p.Announce = ip
	return p, true
}

func decodeIPv4(p Packet, b []byte) (Packet, bool) {
	if len(b) < 20 || b[0]>>4 != 4 {
		return p, false
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || len(b) < ihl {
		return p, false
	}
	// Short frames are padded to the Ethernet minimum; trim the padding.
	if total := int(binary.BigEndian.Uint16(b[2:4])); total >= ihl && total < len(b) {
		b = b[:total]
	}
	p.Proto = b[9]
	p.SrcIP = netip.AddrFrom4([4]byte(b[12:16]))
	p.DstIP = netip.AddrFrom4([4]byte(b[16:20]))
	if binary.BigEndian.Uint16(b[6:8])&0x1fff != 0 {
		return p, true // later fragment: no transport header
	}
	return decodeTransport(p, b[ihl:]), true
}

func decodeIPv6(p Packet, b []byte) (Packet, bool) {
	if len(b) < 40 || b[0]>>4 != 6 {
		return p, false
	}
	next := b[6]
	p.SrcIP = netip.AddrFrom16([16]byte(b[8:24]))
	p.DstIP = netip.AddrFrom16([16]byte(b[24:40]))
	rest := b[40:]
	for i := 0; i < 8; i++ {
		switch next {
		case 0, 43, 60: // hop-by-hop, routing, destination options
			if len(rest) < 8 {
				p.Proto = next
				return p, true
			}
			n := (int(rest[1]) + 1) * 8
			if len(rest) < n {
				p.Proto = next
				return p, true
			}
			next, rest = rest[0], rest[n:]
			continue
		case 44: // fragment
			if len(rest) < 8 {
				p.Proto = next
				return p, true
			}
			later := binary.BigEndian.Uint16(rest[2:4])&0xfff8 != 0
			next, rest = rest[0], rest[8:]
			if later {
				p.Proto = next
				return p, true
			}
			continue
		}
		break
	}
	p.Proto = next
	return decodeTransport(p, rest), true
}

func decodeTransport(p Packet, b []byte) Packet {
	switch p.Proto {
	case ProtoTCP:
		if len(b) < 20 {
			return p
		}
		p.SrcPort = binary.BigEndian.Uint16(b[0:2])
		p.DstPort = binary.BigEndian.Uint16(b[2:4])
		p.TCPFlags = b[13]
		p.Seq = binary.BigEndian.Uint32(b[4:8])
		if off := int(b[12]>>4) * 4; off >= 20 && off <= len(b) {
			p.Payload = b[off:]
		}
	case ProtoUDP:
		if len(b) < 8 {
			return p
		}
		p.SrcPort = binary.BigEndian.Uint16(b[0:2])
		p.DstPort = binary.BigEndian.Uint16(b[2:4])
		p.Payload = b[8:]
	case ProtoICMPv6:
		// Neighbour solicitation (135) and advertisement (136) carry the
		// address being claimed at offset 8.
		if len(b) < 24 || (b[0] != 135 && b[0] != 136) {
			return p
		}
		target := netip.AddrFrom16([16]byte(b[8:24]))
		switch {
		case b[0] == 136:
			p.Announce = target
		case p.SrcIP.IsUnspecified():
			p.Announce = target // duplicate address detection for a new address
		default:
			p.Announce = p.SrcIP
		}
	}
	return p
}
