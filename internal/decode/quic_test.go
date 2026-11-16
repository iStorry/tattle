package decode_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/istorry/tattle/internal/decode"
	"github.com/istorry/tattle/internal/decode/quictest"
)

var dcid, _ = hex.DecodeString("8394c8f03e515708") // the connection ID used in RFC 9001's examples

func TestQUICClientInitial(t *testing.T) {
	hello := quictest.Hello("h3.example.com", 300)
	// The handshake in three pieces, deliberately out of order, as Chrome sends it.
	frags := []quictest.Frag{
		{Offset: 200, Data: hello[200:]},
		{Offset: 0, Data: hello[:90]},
		{Offset: 90, Data: hello[90:200]},
	}
	for name, version := range map[string]uint32{"version 1": quictest.V1, "version 2": quictest.V2} {
		packet := quictest.ClientInitial(version, dcid, 2, frags, 1200)
		if len(packet) != 1200 {
			t.Fatalf("%s: test packet is %d bytes", name, len(packet))
		}
		got, ok := decode.QUICClientInitial(packet)
		if !ok || len(got) != 3 {
			t.Fatalf("%s: ok=%v, %d pieces", name, ok, len(got))
		}
		joined := make([]byte, len(hello))
		for _, f := range got {
			copy(joined[f.Offset:], f.Data)
		}
		if !bytes.Equal(joined, hello) {
			t.Errorf("%s: handshake bytes differ", name)
		}
		if host, res := decode.ParseHelloMessage(joined); res != decode.HelloFound || host != "h3.example.com" {
			t.Errorf("%s: name = %q, %v", name, host, res)
		}

		// A packet altered in transit, or one that is not what it claims,
		// fails authentication and yields nothing.
		for _, at := range []int{0, 7, 30, 600, 1199} {
			bad := append([]byte(nil), packet...)
			bad[at] ^= 0x01
			if _, ok := decode.QUICClientInitial(bad); ok {
				t.Errorf("%s: accepted a packet with byte %d changed", name, at)
			}
		}
		for cut := 0; cut < len(packet); cut += 37 {
			if _, ok := decode.QUICClientInitial(packet[:cut]); ok {
				t.Errorf("%s: accepted a packet cut to %d bytes", name, cut)
			}
		}
	}
	for _, other := range [][]byte{nil, {0x40, 1, 2, 3}, bytes.Repeat([]byte{0xc0}, 1200), bytes.Repeat([]byte{0xff}, 40)} {
		if _, ok := decode.QUICClientInitial(other); ok {
			t.Errorf("accepted %d bytes of non-QUIC data", len(other))
		}
	}
}

func TestParseHelloMessage(t *testing.T) {
	hello := quictest.Hello("h3.example.com", 300)
	for cut := 0; cut < len(hello)-len("h3.example.com"); cut++ {
		if name, res := decode.ParseHelloMessage(hello[:cut]); res != decode.HelloIncomplete || name != "" {
			t.Fatalf("prefix of %d bytes: %q, %v", cut, name, res)
		}
	}
	if _, res := decode.ParseHelloMessage([]byte{2, 0, 0, 1, 0}); res != decode.HelloNone {
		t.Errorf("a ServerHello was not rejected: %v", res)
	}
}
