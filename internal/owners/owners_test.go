package owners

import (
	"net/netip"
	"os"
	"strings"
	"testing"
)

const sample = "1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET - Cloudflare, Inc.\n" +
	"1.0.1.0\t1.0.3.255\t0\tNone\tNot routed\n" +
	"1.0.4.0\t1.0.7.255\t38803\tAU\tGTELECOM-AS-AP - Gtelecom Pty Ltd\n" +
	"1.1.1.0\t1.1.1.255\t13335\tUS\tCLOUDFLARENET - Cloudflare, Inc.\n" +
	"8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE - Google LLC\n" +
	"9.9.9.0\t9.9.9.255\t19281\tNone\tQUAD9-AS-1\n" +
	"2606:4700::\t2606:4700:1:ffff:ffff:ffff:ffff:ffff\t13335\tUS\tCLOUDFLARENET - Cloudflare, Inc.\n"

func TestLookup(t *testing.T) {
	table, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if table.Len() != 6 || len(table.owners) != 4 {
		t.Fatalf("%d ranges, %d owners", table.Len(), len(table.owners))
	}
	for ip, want := range map[string]string{
		"1.0.0.0":         "Cloudflare, Inc. AS13335 US",
		"1.0.0.255":       "Cloudflare, Inc. AS13335 US",
		"1.1.1.1":         "Cloudflare, Inc. AS13335 US",
		"1.0.5.9":         "Gtelecom Pty Ltd AS38803 AU",
		"8.8.8.8":         "Google LLC AS15169 US",
		"9.9.9.9":         "QUAD9-AS-1 AS19281 ", // no company name or country given
		"2606:4700::1111": "Cloudflare, Inc. AS13335 US",
		"::ffff:8.8.8.8":  "Google LLC AS15169 US",
		"1.0.2.1":         "", // not routed
		"0.0.0.1":         "", // before the first range
		"1.0.8.0":         "", // between ranges
		"255.255.255.255": "",
		"2606:4700:2::1":  "",
		"2001:db8::1":     "",
	} {
		o, ok := table.Lookup(netip.MustParseAddr(ip))
		got := ""
		if ok {
			got = o.Name + " AS" + itoa(o.ASN) + " " + o.Country
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", ip, got, want)
		}
	}
	if _, ok := table.LookupString("not an address"); ok {
		t.Error("found an owner for nonsense")
	}
	var none *Table
	if _, ok := none.LookupString("1.1.1.1"); ok || none.Len() != 0 {
		t.Error("a missing table answered")
	}
}

func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestParseRejectsBrokenTables(t *testing.T) {
	for name, text := range map[string]string{
		"out of order":       "8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n1.1.1.0\t1.1.1.255\t13335\tUS\tCLOUDFLARENET\n",
		"bad address":        "one\ttwo\t15169\tUS\tGOOGLE\n",
		"bad network number": "1.1.1.0\t1.1.1.255\tabc\tUS\tX\n",
		"mixed families":     "1.1.1.0\t2606:4700::\t13335\tUS\tX\n",
	} {
		if _, err := Parse(strings.NewReader(text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if table, err := Parse(strings.NewReader("a comment line\n\n")); err != nil || table.Len() != 0 {
		t.Errorf("short lines should be skipped: %v", err)
	}
	if _, err := Load("does-not-exist.tsv.gz"); err == nil {
		t.Error("loaded a missing file")
	}
}

// TestRealTable runs against the published file when it has been fetched
// with "make data"; it is skipped otherwise.
func TestRealTable(t *testing.T) {
	const path = "../../data/ip2asn.tsv.gz"
	if _, err := os.Stat(path); err != nil {
		t.Skip("no data file; run 'make data' to include this test")
	}
	table, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if table.Len() < 300_000 {
		t.Fatalf("only %d ranges", table.Len())
	}
	for ip, want := range map[string]string{"1.1.1.1": "Cloudflare", "8.8.8.8": "Google", "2606:4700:4700::1111": "Cloudflare"} {
		if o, ok := table.LookupString(ip); !ok || !strings.Contains(o.Name, want) {
			t.Errorf("%s: got %+v", ip, o)
		}
	}
	for _, ip := range []string{"192.168.1.1", "10.0.0.1", "127.0.0.1"} {
		if o, ok := table.LookupString(ip); ok {
			t.Errorf("%s is private but has owner %+v", ip, o)
		}
	}
	t.Logf("%d ranges, %d distinct owners", table.Len(), len(table.owners))
}
