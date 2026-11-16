// Package owners answers "whose address is this?" from a table kept on the
// host. Nothing is looked up over the network.
//
// The table is the public-domain IP-to-network list from iptoasn.com: one
// line per range of addresses, naming the network that announces it.
package owners

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/istorry/tattle/internal/model"
)

// Table maps addresses to the networks they belong to. It is read-only
// once built, so it can be shared freely.
type Table struct {
	v4Start, v4End []uint32
	v4Owner        []uint32
	v6Start, v6End [][16]byte
	v6Owner        []uint32
	owners         []model.Owner
}

// Load reads a gzipped table from a file.
func Load(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s is not a gzip file: %w", path, err)
	}
	defer zr.Close()
	return Parse(zr)
}

// Parse reads a table in iptoasn's format, tab separated:
//
//	range_start  range_end  AS_number  country_code  AS_description
func Parse(r io.Reader) (*Table, error) {
	t := &Table{}
	index := map[model.Owner]uint32{}
	countries := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 5 {
			continue
		}
		asn, err := strconv.ParseUint(f[2], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("line %d: bad network number %q", line, f[2])
		}
		if asn == 0 {
			continue // "Not routed": nobody announces this range
		}
		start, err1 := netip.ParseAddr(f[0])
		end, err2 := netip.ParseAddr(f[1])
		if err1 != nil || err2 != nil || start.Is4() != end.Is4() {
			return nil, fmt.Errorf("line %d: bad address range %q to %q", line, f[0], f[1])
		}
		owner := model.Owner{ASN: uint32(asn), Name: friendlyName(f[4]), Country: countryCode(f[3])}
		id, ok := index[owner]
		if !ok {
			// Copy the text out of the line it was read from, so that the
			// line itself can be freed; share the few country codes.
			owner.Name = strings.Clone(owner.Name)
			if c, seen := countries[owner.Country]; seen {
				owner.Country = c
			} else {
				owner.Country = strings.Clone(owner.Country)
				countries[owner.Country] = owner.Country
			}
			id = uint32(len(t.owners))
			index[owner] = id
			t.owners = append(t.owners, owner)
		}
		if start.Is4() {
			s, e := start.As4(), end.As4()
			t.v4Start = append(t.v4Start, binary.BigEndian.Uint32(s[:]))
			t.v4End = append(t.v4End, binary.BigEndian.Uint32(e[:]))
			t.v4Owner = append(t.v4Owner, id)
		} else {
			t.v6Start = append(t.v6Start, start.As16())
			t.v6End = append(t.v6End, end.As16())
			t.v6Owner = append(t.v6Owner, id)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// Lookups rely on the ranges being in order, as the published file is.
	if !sort.SliceIsSorted(t.v4Start, func(i, j int) bool { return t.v4Start[i] < t.v4Start[j] }) ||
		!sort.SliceIsSorted(t.v6Start, func(i, j int) bool { return bytes.Compare(t.v6Start[i][:], t.v6Start[j][:]) < 0 }) {
		return nil, fmt.Errorf("the address ranges are not in order")
	}
	// Give back the spare room left by growing the lists.
	t.v4Start, t.v4End, t.v4Owner = slices.Clip(t.v4Start), slices.Clip(t.v4End), slices.Clip(t.v4Owner)
	t.v6Start, t.v6End, t.v6Owner = slices.Clip(t.v6Start), slices.Clip(t.v6End), slices.Clip(t.v6Owner)
	t.owners = slices.Clip(t.owners)
	return t, nil
}

// friendlyName turns "CLOUDFLARENET - Cloudflare, Inc." into "Cloudflare, Inc.".
func friendlyName(description string) string {
	if _, name, ok := strings.Cut(description, " - "); ok && strings.TrimSpace(name) != "" {
		description = name
	}
	return strings.TrimSpace(description)
}

// countryCode keeps a two-letter code and drops placeholders such as "None".
func countryCode(s string) string {
	if len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z' {
		return s
	}
	return ""
}

// Len is the number of address ranges in the table.
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return len(t.v4Start) + len(t.v6Start)
}

// Lookup returns the network an address belongs to. A nil table knows nothing.
func (t *Table) Lookup(addr netip.Addr) (model.Owner, bool) {
	if t == nil || !addr.IsValid() {
		return model.Owner{}, false
	}
	addr = addr.Unmap()
	if addr.Is4() {
		a4 := addr.As4()
		ip := binary.BigEndian.Uint32(a4[:])
		// The last range starting at or before the address.
		i := sort.Search(len(t.v4Start), func(i int) bool { return t.v4Start[i] > ip }) - 1
		if i < 0 || ip > t.v4End[i] {
			return model.Owner{}, false
		}
		return t.owners[t.v4Owner[i]], true
	}
	ip := addr.As16()
	i := sort.Search(len(t.v6Start), func(i int) bool { return bytes.Compare(t.v6Start[i][:], ip[:]) > 0 }) - 1
	if i < 0 || bytes.Compare(ip[:], t.v6End[i][:]) > 0 {
		return model.Owner{}, false
	}
	return t.owners[t.v6Owner[i]], true
}

// LookupString is Lookup for an address in text form.
func (t *Table) LookupString(ip string) (model.Owner, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return model.Owner{}, false
	}
	return t.Lookup(addr)
}
