package routes

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	table := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"vmbr0\t00000000\t0101A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"vmbr0\t0001A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" + // the local network, not a way out
		"vmbr1\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" + // the same gateway again
		"eth9\t00000000\tFE00000A\t0002\t0\t0\t0\t00000000\t0\t0\t0\n" // a route that is down
	got := Parse(strings.NewReader(table))
	if len(got) != 1 || got[0] != "192.168.1.1" {
		t.Errorf("gateways = %v, want [192.168.1.1]", got)
	}
}
