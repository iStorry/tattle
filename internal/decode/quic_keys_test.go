package decode

import (
	"encoding/hex"
	"testing"
)

// The client Initial keys published in RFC 9001, appendix A.1.
func TestQUICInitialKeysMatchTheStandard(t *testing.T) {
	dcid, _ := hex.DecodeString("8394c8f03e515708")
	keys, ok := quicInitialKeys(quicV1, dcid)
	if !ok {
		t.Fatal("no keys for version 1")
	}
	for name, pair := range map[string][2]string{
		"key": {hex.EncodeToString(keys.key), "1f369613dd76d5467730efcbe3b1a22d"},
		"iv":  {hex.EncodeToString(keys.iv), "fa044b2f42a3fd3b46fb255c"},
		"hp":  {hex.EncodeToString(keys.hp), "9f50449e04a0e810283a1e9933adedd2"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %s, want %s", name, pair[0], pair[1])
		}
	}
	if _, ok := quicInitialKeys(0x1a2a3a4a, dcid); ok {
		t.Error("derived keys for an unknown version")
	}
}
