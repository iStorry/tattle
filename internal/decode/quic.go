package decode

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
)

// HTTP/3 runs over QUIC, which encrypts even its first packet. That packet
// is not secret, though: its key is derived from a constant published in
// the standard and the connection ID in the packet's own header, precisely
// so that observers can read it (RFC 9001, section 5.2). Inside is the same
// TLS ClientHello that HTTPS over TCP sends in the clear.

const (
	quicV1 = 0x00000001
	quicV2 = 0x6b3343cf
)

var (
	quicSaltV1 = []byte{0x38, 0x76, 0x2c, 0xf7, 0xf5, 0x59, 0x34, 0xb3, 0x4d, 0x17,
		0x9a, 0xe6, 0xa4, 0xc8, 0x0c, 0xad, 0xcc, 0xbb, 0x7f, 0x0a}
	quicSaltV2 = []byte{0x0d, 0xed, 0xe3, 0xde, 0xf7, 0x00, 0xa6, 0xdb, 0x81, 0x93,
		0x81, 0xbe, 0x6e, 0x26, 0x9d, 0xcb, 0xf9, 0xbd, 0x2e, 0xd9}
)

// CryptoFrag is a piece of the TLS handshake carried by a QUIC packet.
// Clients deliberately send the pieces out of order and across packets.
type CryptoFrag struct {
	Offset int
	Data   []byte
}

// quicKeys are the keys protecting a client's Initial packets.
type quicKeys struct{ key, iv, hp []byte }

// expandLabel is HKDF-Expand-Label from TLS 1.3 with an empty context.
func expandLabel(secret []byte, label string, length int) []byte {
	full := "tls13 " + label
	info := make([]byte, 0, 4+len(full))
	info = binary.BigEndian.AppendUint16(info, uint16(length))
	info = append(info, byte(len(full)))
	info = append(info, full...)
	info = append(info, 0)
	out, err := hkdf.Expand(sha256.New, secret, string(info), length)
	if err != nil {
		panic(err) // only possible for an absurd length
	}
	return out
}

// quicInitialKeys derives the client's Initial keys for a connection ID.
func quicInitialKeys(version uint32, dcid []byte) (quicKeys, bool) {
	salt, prefix := quicSaltV1, "quic "
	switch version {
	case quicV1:
	case quicV2:
		salt, prefix = quicSaltV2, "quicv2 "
	default:
		return quicKeys{}, false
	}
	initial, err := hkdf.Extract(sha256.New, dcid, salt)
	if err != nil {
		return quicKeys{}, false
	}
	client := expandLabel(initial, "client in", 32)
	return quicKeys{
		key: expandLabel(client, prefix+"key", 16),
		iv:  expandLabel(client, prefix+"iv", 12),
		hp:  expandLabel(client, prefix+"hp", 16),
	}, true
}

// quicVarint reads a QUIC variable-length integer and returns its size.
func quicVarint(b []byte) (value, size int, ok bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	size = 1 << (b[0] >> 6)
	if len(b) < size {
		return 0, 0, false
	}
	v := uint64(b[0] & 0x3f)
	for _, c := range b[1:size] {
		v = v<<8 | uint64(c)
	}
	if v > 1<<30 {
		return 0, 0, false // far larger than any packet; not worth representing
	}
	return int(v), size, true
}

// QUICClientInitial decrypts a client's QUIC Initial packet from a UDP
// payload and returns the handshake pieces it carries. ok is false for
// anything else, including packets that fail authentication.
func QUICClientInitial(d []byte) (frags []CryptoFrag, ok bool) {
	// Long header: flags, version, connection IDs, token, length.
	if len(d) < 7 || d[0]&0xc0 != 0xc0 {
		return nil, false
	}
	version := binary.BigEndian.Uint32(d[1:5])
	initialType := byte(0)
	if version == quicV2 {
		initialType = 1
	}
	if (d[0]>>4)&3 != initialType {
		return nil, false
	}
	pos := 5
	dcidLen := int(d[pos])
	pos++
	if dcidLen > 20 || len(d) < pos+dcidLen+1 {
		return nil, false
	}
	dcid := d[pos : pos+dcidLen]
	pos += dcidLen
	scidLen := int(d[pos])
	pos++
	if scidLen > 20 || len(d) < pos+scidLen {
		return nil, false
	}
	pos += scidLen
	tokenLen, n, vok := quicVarint(d[pos:])
	if !vok || len(d) < pos+n+tokenLen {
		return nil, false
	}
	pos += n + tokenLen
	length, n, vok := quicVarint(d[pos:])
	if !vok {
		return nil, false
	}
	pos += n
	// The rest is a 1-4 byte packet number and the encrypted payload. Header
	// protection hides the number's size, so sample from where a 4-byte
	// number would end, as the standard specifies.
	if length < 20 || len(d) < pos+length {
		return nil, false
	}
	keys, kok := quicInitialKeys(version, dcid)
	if !kok {
		return nil, false
	}
	hp, err := aes.NewCipher(keys.hp)
	if err != nil {
		return nil, false
	}
	var mask [16]byte
	hp.Encrypt(mask[:], d[pos+4:pos+20])

	first := d[0] ^ mask[0]&0x0f
	pnLen := int(first&3) + 1
	header := make([]byte, pos+pnLen)
	copy(header, d)
	header[0] = first
	nonce := append([]byte(nil), keys.iv...)
	for i := 0; i < pnLen; i++ {
		header[pos+i] ^= mask[1+i]
		nonce[len(nonce)-pnLen+i] ^= header[pos+i]
	}

	block, err := aes.NewCipher(keys.key)
	if err != nil {
		return nil, false
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false
	}
	plain, err := aead.Open(nil, nonce, d[pos+pnLen:pos+length], header)
	if err != nil {
		return nil, false
	}
	return quicCryptoFrames(plain), true
}

// quicCryptoFrames collects the CRYPTO frames from a decrypted Initial
// packet, skipping the few other frame types a client may put beside them.
func quicCryptoFrames(p []byte) []CryptoFrag {
	var frags []CryptoFrag
	varints := func(count int) bool {
		for i := 0; i < count; i++ {
			_, n, ok := quicVarint(p)
			if !ok {
				return false
			}
			p = p[n:]
		}
		return true
	}
	for len(p) > 0 {
		frameType := p[0]
		p = p[1:]
		switch frameType {
		case 0x00, 0x01: // PADDING, PING
		case 0x02, 0x03: // ACK: largest, delay, range count, first range, then ranges
			_, n1, ok1 := quicVarint(p)
			if !ok1 {
				return frags
			}
			p = p[n1:]
			_, n2, ok2 := quicVarint(p)
			if !ok2 {
				return frags
			}
			p = p[n2:]
			ranges, n3, ok3 := quicVarint(p)
			if !ok3 || ranges > 64 {
				return frags
			}
			p = p[n3:]
			extra := 1 + 2*ranges
			if frameType == 0x03 {
				extra += 3 // ECN counts
			}
			if !varints(extra) {
				return frags
			}
		case 0x06: // CRYPTO: offset, length, data
			off, n, ok := quicVarint(p)
			if !ok {
				return frags
			}
			p = p[n:]
			size, n, ok := quicVarint(p)
			if !ok || len(p) < n+size {
				return frags
			}
			frags = append(frags, CryptoFrag{Offset: off, Data: p[n : n+size]})
			p = p[n+size:]
		default:
			return frags // nothing else carries the handshake
		}
	}
	return frags
}

// ParseHelloMessage reads the server name from a TLS ClientHello handshake
// message without a record header, which is how QUIC carries it. Like
// ParseClientHello, it reports HelloIncomplete when more bytes are needed.
func ParseHelloMessage(b []byte) (string, HelloResult) {
	if len(b) == 0 {
		return "", HelloIncomplete
	}
	if b[0] != 0x01 {
		return "", HelloNone
	}
	if len(b) < 4 {
		return "", HelloIncomplete
	}
	total := 4 + int(b[1])<<16 + int(b[2])<<8 + int(b[3])
	name, ranOut := helloServerName(b[4:])
	switch {
	case name != "":
		return name, HelloFound
	case ranOut && len(b) < total:
		return "", HelloIncomplete
	}
	return "", HelloNone
}
