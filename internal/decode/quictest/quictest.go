// Package quictest builds QUIC Initial packets for tests. It derives keys
// and protects packets with its own code, following RFC 9001 from the
// sender's side, so that it checks the decoder rather than mirroring it.
package quictest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// Frag is a piece of handshake data to place in a CRYPTO frame.
type Frag struct {
	Offset int
	Data   []byte
}

const (
	V1 = 0x00000001
	V2 = 0x6b3343cf
)

func label(secret []byte, name string, n int) []byte {
	info := []byte{byte(n >> 8), byte(n), byte(6 + len(name))}
	info = append(info, "tls13 "+name...)
	info = append(info, 0)
	out, err := hkdf.Expand(sha256.New, secret, string(info), n)
	if err != nil {
		panic(err)
	}
	return out
}

func varint(v int) []byte { return []byte{0x40 | byte(v>>8), byte(v)} } // 2-byte form, values below 16384

// ClientInitial returns a UDP payload holding one client Initial packet,
// padded to size bytes as real clients pad theirs.
func ClientInitial(version uint32, dcid []byte, pn uint16, frags []Frag, size int) []byte {
	salt, prefix, typ := "38762cf7f55934b34d179ae6a4c80cadccbb7f0a", "quic ", byte(0)
	if version == V2 {
		salt, prefix, typ = "0dede3def700a6db819381be6e269dcbf9bd2ed9", "quicv2 ", 1
	}
	saltBytes, _ := hex.DecodeString(salt)
	initial, err := hkdf.Extract(sha256.New, dcid, saltBytes)
	if err != nil {
		panic(err)
	}
	client := label(initial, "client in", 32)
	key, iv, hp := label(client, prefix+"key", 16), label(client, prefix+"iv", 12), label(client, prefix+"hp", 16)

	const pnLen = 2
	header := []byte{0xc0 | typ<<4 | (pnLen - 1)}
	header = binary.BigEndian.AppendUint32(header, version)
	header = append(header, byte(len(dcid)))
	header = append(header, dcid...)
	header = append(header, 4, 0xaa, 0xbb, 0xcc, 0xdd) // source connection ID
	header = append(header, 0)                         // no token

	var payload []byte
	for _, f := range frags {
		payload = append(payload, 0x06)
		payload = append(payload, varint(f.Offset)...)
		payload = append(payload, varint(len(f.Data))...)
		payload = append(payload, f.Data...)
	}
	if pad := size - (len(header) + 2 + pnLen + len(payload) + 16); pad > 0 {
		payload = append(payload, make([]byte, pad)...) // PADDING frames
	}
	header = append(header, varint(pnLen+len(payload)+16)...)
	pnAt := len(header)
	header = binary.BigEndian.AppendUint16(header, pn)

	nonce := append([]byte(nil), iv...)
	nonce[10] ^= byte(pn >> 8)
	nonce[11] ^= byte(pn)
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	sealed := aead.Seal(nil, nonce, payload, header)

	// Header protection: mask the low flag bits and the packet number with
	// AES of a sample taken 4 bytes after the packet number starts.
	hpBlock, _ := aes.NewCipher(hp)
	var mask [16]byte
	hpBlock.Encrypt(mask[:], sealed[4-pnLen:4-pnLen+16])
	header[0] ^= mask[0] & 0x0f
	header[pnAt] ^= mask[1]
	header[pnAt+1] ^= mask[2]
	return append(header, sealed...)
}

// Hello returns a TLS ClientHello handshake message (no record header)
// with the server name after padding bytes of another extension.
func Hello(host string, padding int) []byte {
	u16 := func(v int) []byte { return []byte{byte(v >> 8), byte(v)} }
	cat := func(parts ...[]byte) (out []byte) {
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	sni := cat(u16(0), u16(len(host)+5), u16(len(host)+3), []byte{0}, u16(len(host)), []byte(host))
	exts := cat(u16(0x33), u16(padding), make([]byte, padding), sni)
	body := cat([]byte{3, 3}, make([]byte, 32), []byte{0}, u16(2), []byte{0x13, 0x01}, []byte{1, 0}, u16(len(exts)), exts)
	return cat([]byte{1, 0}, u16(len(body)), body)
}
