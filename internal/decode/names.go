package decode

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"strings"
)

// ParseDNSResponse reads a DNS answer and returns the name that was asked
// for together with the addresses it resolved to. Tattle uses this to put a
// name on connections that are otherwise just an IP address.
func ParseDNSResponse(msg []byte) (name string, addrs []netip.Addr, ok bool) {
	if len(msg) < 12 {
		return "", nil, false
	}
	flags := binary.BigEndian.Uint16(msg[2:4])
	if flags&0x8000 == 0 || flags&0x000f != 0 { // not a response, or an error
		return "", nil, false
	}
	questions := int(binary.BigEndian.Uint16(msg[4:6]))
	answers := int(binary.BigEndian.Uint16(msg[6:8]))
	if questions < 1 || answers < 1 {
		return "", nil, false
	}
	name, off, ok := readName(msg, 12)
	if !ok {
		return "", nil, false
	}
	off += 4 // type and class of the first question
	for i := 1; i < questions; i++ {
		if _, off, ok = readName(msg, off); !ok {
			return "", nil, false
		}
		off += 4
	}
	for i := 0; i < answers; i++ {
		if _, off, ok = readName(msg, off); !ok {
			break
		}
		if off+10 > len(msg) {
			break
		}
		rtype := binary.BigEndian.Uint16(msg[off : off+2])
		rdlen := int(binary.BigEndian.Uint16(msg[off+8 : off+10]))
		off += 10
		if off+rdlen > len(msg) {
			break
		}
		switch {
		case rtype == 1 && rdlen == 4:
			addrs = append(addrs, netip.AddrFrom4([4]byte(msg[off:off+4])))
		case rtype == 28 && rdlen == 16:
			addrs = append(addrs, netip.AddrFrom16([16]byte(msg[off:off+16])))
		}
		off += rdlen
	}
	name = cleanHost(name)
	return name, addrs, name != "" && len(addrs) > 0
}

// readName decodes a possibly compressed DNS name starting at off and
// returns the offset just past it.
func readName(msg []byte, off int) (string, int, bool) {
	var sb strings.Builder
	end := -1
	for hops := 0; hops < 32; {
		if off >= len(msg) {
			return "", 0, false
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return sb.String(), end, true
		case l&0xc0 == 0xc0:
			if off+1 >= len(msg) {
				return "", 0, false
			}
			if end < 0 {
				end = off + 2
			}
			off = int(binary.BigEndian.Uint16(msg[off:off+2]) & 0x3fff)
			hops++
		case l&0xc0 != 0:
			return "", 0, false
		default:
			if off+1+l > len(msg) {
				return "", 0, false
			}
			if sb.Len() > 0 {
				sb.WriteByte('.')
			}
			sb.Write(msg[off+1 : off+1+l])
			off += 1 + l
		}
	}
	return "", 0, false
}

// ParseSNI returns the server name from a TLS ClientHello, which is sent
// unencrypted at the start of every HTTPS connection. It tolerates a
// ClientHello that was cut short, returning "" if the name is not in the
// part that was captured.
func ParseSNI(b []byte) string {
	// Record header: type 22 (handshake), version, length.
	if len(b) < 5 || b[0] != 0x16 || b[1] != 0x03 {
		return ""
	}
	b = b[5:]
	// Handshake header: type 1 (ClientHello), 3-byte length.
	if len(b) < 4 || b[0] != 0x01 {
		return ""
	}
	b = b[4:]
	if len(b) < 34 { // version + random
		return ""
	}
	b = b[34:]
	var ok bool
	if b, ok = skip(b, 1); !ok { // session id
		return ""
	}
	if b, ok = skip(b, 2); !ok { // cipher suites
		return ""
	}
	if b, ok = skip(b, 1); !ok { // compression methods
		return ""
	}
	if len(b) < 2 {
		return ""
	}
	b = b[2:] // extensions length; trust the bytes we actually have instead
	for len(b) >= 4 {
		extType := binary.BigEndian.Uint16(b[0:2])
		extLen := int(binary.BigEndian.Uint16(b[2:4]))
		b = b[4:]
		if extLen > len(b) {
			if extType != 0 {
				return ""
			}
			extLen = len(b)
		}
		if extType == 0 {
			ext := b[:extLen]
			// list length (2), name type (1), name length (2), name
			if len(ext) < 5 || ext[2] != 0 {
				return ""
			}
			n := int(binary.BigEndian.Uint16(ext[3:5]))
			if 5+n > len(ext) {
				return ""
			}
			return cleanHost(string(ext[5 : 5+n]))
		}
		b = b[extLen:]
	}
	return ""
}

// skip steps over a field that is prefixed by a length of the given size.
func skip(b []byte, lenSize int) ([]byte, bool) {
	if len(b) < lenSize {
		return nil, false
	}
	n := int(b[0])
	if lenSize == 2 {
		n = int(binary.BigEndian.Uint16(b[0:2]))
	}
	if len(b) < lenSize+n {
		return nil, false
	}
	return b[lenSize+n:], true
}

var httpMethods = [][]byte{
	[]byte("GET "), []byte("POST "), []byte("PUT "), []byte("HEAD "), []byte("DELETE "),
	[]byte("OPTIONS "), []byte("PATCH "), []byte("CONNECT "),
}

// ParseHTTPHost returns the Host header of an unencrypted HTTP request.
func ParseHTTPHost(b []byte) string {
	isRequest := false
	for _, m := range httpMethods {
		if bytes.HasPrefix(b, m) {
			isRequest = true
			break
		}
	}
	if !isRequest {
		return ""
	}
	for {
		nl := bytes.Index(b, []byte("\r\n"))
		if nl < 0 {
			return ""
		}
		b = b[nl+2:]
		if len(b) < 5 || bytes.HasPrefix(b, []byte("\r\n")) {
			return ""
		}
		if !bytes.EqualFold(b[:5], []byte("host:")) {
			continue
		}
		line := b[5:]
		if end := bytes.Index(line, []byte("\r\n")); end >= 0 {
			line = line[:end]
		}
		host := strings.TrimSpace(string(line))
		if strings.HasPrefix(host, "[") { // IPv6 literal
			return ""
		}
		if i := strings.LastIndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		return cleanHost(host)
	}
}

// cleanHost lower-cases a hostname and rejects anything that is not one, so
// that bytes taken from the network never reach the UI unchecked.
func cleanHost(s string) string {
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 253 {
		return ""
	}
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
		default:
			return ""
		}
		out[i] = c
	}
	return string(out)
}
