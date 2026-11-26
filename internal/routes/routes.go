// Package routes reads which address the host sends internet traffic
// through, from the kernel's own table. Nothing is sent to find out.
package routes

import (
	"bufio"
	"encoding/hex"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// Gateways returns the host's default IPv4 gateways, or nil when they
// cannot be read (as on anything that is not Linux).
func Gateways() []string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return nil
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads the format of /proc/net/route:
//
//	Iface  Destination  Gateway   Flags ...
//	vmbr0  00000000     0101A8C0  0003  ...
//
// Addresses are hexadecimal with the first byte last.
func Parse(r io.Reader) []string {
	const up, gateway = 0x1, 0x2
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&up == 0 || flags&gateway == 0 {
			continue
		}
		b, err := hex.DecodeString(f[2])
		if err != nil || len(b) != 4 {
			continue
		}
		ip := netip.AddrFrom4([4]byte{b[3], b[2], b[1], b[0]}).String()
		dup := false
		for _, o := range out {
			dup = dup || o == ip
		}
		if !dup {
			out = append(out, ip)
		}
	}
	return out
}
