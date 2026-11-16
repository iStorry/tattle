// Package tracker turns a stream of packets into per-connection totals and
// works out which machine each connection belongs to.
package tracker

import (
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/istorry/tattle/internal/decode"
	"github.com/istorry/tattle/internal/model"
)

const (
	maxFlows     = 200_000
	maxDNSNames  = 100_000
	tcpIdle      = 5 * time.Minute
	otherIdle    = 60 * time.Second
	closeLinger  = 5 * time.Second
	sniffPackets = 6 // client packets inspected for a hostname before giving up

	// Limits on joining a ClientHello that spans several packets.
	maxHelloBytes    = 16 << 10
	maxHelloSegments = 12
	maxHelloPending  = 4
	maxHellosAtOnce  = 4096 // across all connections, so a flood cannot use much memory
)

type flowKey struct {
	client, server netip.Addr
	cport, sport   uint16
	proto          uint8
}

type flow struct {
	clientMAC, serverMAC decode.MAC
	domain               string
	named                bool // domain came from the connection itself, not from DNS
	sniffed              int
	hello                *helloBuf // a ClientHello still being joined together, if any
	closing              bool
	isNew                bool
	last                 time.Time

	// Totals since the last flush.
	up, down, pkts int64
	deltaFirst     time.Time
}

type addrPair struct{ client, server netip.Addr }

// helloBuf joins the first packets of a connection in order, so a server
// name that falls in the second or third packet can still be read.
type helloBuf struct {
	start   uint32 // sequence number of data[0]
	data    []byte
	pending []helloSeg // packets that arrived ahead of a gap
	segs    int
}

type helloSeg struct {
	seq  uint32
	data []byte
}

func newHello(seq uint32, first []byte) *helloBuf {
	return &helloBuf{start: seq, data: append([]byte(nil), first...), segs: 1}
}

// place adds a packet's bytes if they continue the data, and reports
// whether they were at or before the end of it (so not waiting on a gap).
func (h *helloBuf) place(seq uint32, data []byte) bool {
	off := int(int32(seq - h.start)) // signed, so sequence wrap-around works
	if off > len(h.data) {
		return false
	}
	if end := off + len(data); off >= 0 && end > len(h.data) {
		h.data = append(h.data, data[len(h.data)-off:]...)
	}
	return true // a retransmission adds nothing
}

// add takes the connection's next client packet. done reports that no more
// packets are wanted, either because the name was found or it never will be.
func (h *helloBuf) add(seq uint32, data []byte, truncated bool) (name string, done bool) {
	h.segs++
	if !h.place(seq, data) {
		if len(h.pending) >= maxHelloPending {
			return "", true
		}
		h.pending = append(h.pending, helloSeg{seq, append([]byte(nil), data...)})
	}
	for placed := true; placed; {
		placed = false
		for i, s := range h.pending {
			if h.place(s.seq, s.data) {
				h.pending = append(h.pending[:i], h.pending[i+1:]...)
				placed = true
				break
			}
		}
	}
	name, res := decode.ParseClientHello(h.data)
	switch {
	case res == decode.HelloFound:
		return name, true
	case res == decode.HelloNone:
		return "", true
	}
	// A cut-short frame leaves a hole that later packets cannot fill.
	return "", truncated || len(h.data) > maxHelloBytes || h.segs >= maxHelloSegments
}

// Tracker is safe for concurrent use by several capture goroutines.
type Tracker struct {
	mu     sync.Mutex
	flows  map[flowKey]*flow
	hellos int // connections currently joining a ClientHello

	macOwner map[decode.MAC]string   // MAC -> machine id
	ipOwner  map[netip.Addr]string   // address -> machine id
	hasV4    map[string]bool         // machine id -> an IPv4 address is known
	hasV6    map[string]bool         // machine id -> an IPv6 address is known
	dnsPair  map[addrPair]string     // what this client was told this address is called
	dnsAny   map[netip.Addr]string   // what anyone was told this address is called
	learned  map[netip.Addr]struct{} // addresses learned from ARP/NDP, kept across reloads
}

func New() *Tracker {
	return &Tracker{
		flows:    map[flowKey]*flow{},
		macOwner: map[decode.MAC]string{},
		ipOwner:  map[netip.Addr]string{},
		hasV4:    map[string]bool{},
		hasV6:    map[string]bool{},
		dnsPair:  map[addrPair]string{},
		dnsAny:   map[netip.Addr]string{},
		learned:  map[netip.Addr]struct{}{},
	}
}

// SetMachines replaces the list of known machines. Addresses already learned
// from the network are kept for machines that still exist.
func (t *Tracker) SetMachines(machines []model.Machine) {
	t.mu.Lock()
	defer t.mu.Unlock()

	macOwner := map[decode.MAC]string{}
	ids := map[string]bool{}
	for _, m := range machines {
		ids[m.ID] = true
		for _, s := range m.MACs {
			if mac, ok := decode.ParseMAC(s); ok {
				macOwner[mac] = m.ID
			}
		}
	}
	t.macOwner = macOwner

	for ip, id := range t.ipOwner {
		if _, wasLearned := t.learned[ip]; !ids[id] || !wasLearned {
			delete(t.ipOwner, ip)
			delete(t.learned, ip)
		}
	}
	t.hasV4, t.hasV6 = map[string]bool{}, map[string]bool{}
	for ip, id := range t.ipOwner {
		t.markFamily(id, ip)
	}
	for _, m := range machines {
		for _, s := range m.IPs {
			if ip, err := netip.ParseAddr(s); err == nil {
				ip = ip.Unmap()
				t.ipOwner[ip] = m.ID
				t.markFamily(m.ID, ip)
			}
		}
	}
}

func (t *Tracker) markFamily(id string, ip netip.Addr) {
	if ip.Is4() {
		t.hasV4[id] = true
	} else {
		t.hasV6[id] = true
	}
}

// MachineIPs returns the addresses currently attributed to each machine.
func (t *Tracker) MachineIPs() map[string][]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string][]string{}
	for ip, id := range t.ipOwner {
		if ip.IsLinkLocalUnicast() {
			continue
		}
		out[id] = append(out[id], ip.String())
	}
	for _, ips := range out {
		sort.Strings(ips)
	}
	return out
}

// Handle records one packet. wireLen is the frame's real size on the wire.
func (t *Tracker) Handle(p decode.Packet, wireLen int, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if p.Announce.IsValid() {
		if id := t.macOwner[p.SrcMAC]; id != "" {
			t.ipOwner[p.Announce] = id
			t.learned[p.Announce] = struct{}{}
			t.markFamily(id, p.Announce)
		}
	}
	if !p.SrcIP.IsValid() {
		return
	}

	// Learn names from every DNS answer on the bridge, including answers to
	// devices Tattle does not track, so later connections can be labelled.
	if p.Proto == decode.ProtoUDP && p.SrcPort == 53 {
		if name, addrs, ok := decode.ParseDNSResponse(p.Payload); ok {
			if len(t.dnsAny) > maxDNSNames {
				t.dnsAny = map[netip.Addr]string{}
				t.dnsPair = map[addrPair]string{}
			}
			for _, a := range addrs {
				t.dnsAny[a] = name
				t.dnsPair[addrPair{p.DstIP, a}] = name
			}
		}
	}

	if p.DstMAC.IsMulticast() || p.DstIP.IsMulticast() {
		return
	}
	if t.macOwner[p.SrcMAC] == "" && t.macOwner[p.DstMAC] == "" {
		return
	}
	switch p.Proto {
	case decode.ProtoTCP, decode.ProtoUDP, decode.ProtoICMP, decode.ProtoICMPv6:
	default:
		return
	}

	fromClient := true
	key := flowKey{p.SrcIP, p.DstIP, p.SrcPort, p.DstPort, p.Proto}
	f := t.flows[key]
	if f == nil {
		rev := flowKey{p.DstIP, p.SrcIP, p.DstPort, p.SrcPort, p.Proto}
		if f = t.flows[rev]; f != nil {
			fromClient = false
		}
	}
	if f == nil {
		if len(t.flows) >= maxFlows {
			return
		}
		fromClient = srcIsClient(p)
		f = &flow{isNew: true}
		if fromClient {
			f.clientMAC, f.serverMAC = p.SrcMAC, p.DstMAC
		} else {
			key = flowKey{p.DstIP, p.SrcIP, p.DstPort, p.SrcPort, p.Proto}
			f.clientMAC, f.serverMAC = p.DstMAC, p.SrcMAC
		}
		if name, ok := t.dnsPair[addrPair{key.client, key.server}]; ok {
			f.domain = name
		} else {
			f.domain = t.dnsAny[key.server]
		}
		t.flows[key] = f
	}

	if f.pkts == 0 {
		f.deltaFirst = now
	}
	f.pkts++
	f.last = now
	if fromClient {
		f.up += int64(wireLen)
	} else {
		f.down += int64(wireLen)
	}

	if p.Proto == decode.ProtoTCP {
		if p.TCPFlags&(decode.FlagFIN|decode.FlagRST) != 0 {
			f.closing = true
		}
		// The hostname is in the first data the client sends: the TLS
		// ClientHello for HTTPS, or the Host header for plain HTTP.
		if fromClient && !f.named && len(p.Payload) > 0 {
			name := ""
			switch {
			case f.hello != nil:
				var done bool
				if name, done = f.hello.add(p.Seq, p.Payload, p.Truncated); done {
					t.dropHello(f)
				}
			case f.sniffed < sniffPackets:
				f.sniffed++
				var res decode.HelloResult
				name, res = decode.ParseClientHello(p.Payload)
				switch {
				case res == decode.HelloIncomplete && !p.Truncated && t.hellos < maxHellosAtOnce:
					f.hello = newHello(p.Seq, p.Payload)
					t.hellos++
				case res == decode.HelloNone:
					name = decode.ParseHTTPHost(p.Payload)
				}
			}
			if name != "" {
				f.domain, f.named = name, true
			}
		}
	}
}

func (t *Tracker) dropHello(f *flow) {
	if f.hello != nil {
		f.hello = nil
		t.hellos--
	}
}

// srcIsClient guesses which end started a connection when Tattle did not
// see it start.
func srcIsClient(p decode.Packet) bool {
	if p.Proto == decode.ProtoTCP {
		syn, ack := p.TCPFlags&decode.FlagSYN != 0, p.TCPFlags&decode.FlagACK != 0
		if syn && !ack {
			return true
		}
		if syn && ack {
			return false
		}
	}
	if p.Proto == decode.ProtoTCP || p.Proto == decode.ProtoUDP {
		srcKnown, dstKnown := servicePort(p.SrcPort), servicePort(p.DstPort)
		if srcKnown != dstKnown {
			return dstKnown
		}
		if p.SrcPort != p.DstPort {
			return p.SrcPort > p.DstPort // clients use high, random ports
		}
	}
	return true
}

var commonServicePorts = map[uint16]bool{
	1194: true, 1883: true, 2049: true, 3000: true, 3306: true, 3389: true, 5000: true,
	5353: true, 5432: true, 6379: true, 8000: true, 8006: true, 8080: true, 8096: true,
	8123: true, 8443: true, 8883: true, 9000: true, 9090: true, 27017: true, 32400: true,
	51820: true,
}

func servicePort(p uint16) bool { return (p > 0 && p < 1024) || commonServicePorts[p] }

// owner returns the machine an endpoint belongs to, or "".
func (t *Tracker) owner(ip netip.Addr, mac decode.MAC) string {
	if id, ok := t.ipOwner[ip]; ok {
		return id
	}
	id := t.macOwner[mac]
	if id == "" {
		return ""
	}
	// The frame came from this machine's network card, but the address is
	// not one it has announced. If we already know its addresses, this is
	// traffic it is forwarding for someone else (a router or NAT VM), so it
	// is not attributed. If we know none yet, Tattle probably started after
	// the machine announced itself, so the card is the best evidence.
	if (ip.Is4() && t.hasV4[id]) || (!ip.Is4() && t.hasV6[id]) {
		return ""
	}
	return id
}

type aggKey struct {
	bucket         int64
	client, server netip.Addr
	sport          uint16
	proto          uint8
	domain         string
}

// Flush returns the traffic seen since the previous call and forgets
// connections that have ended or gone quiet.
func (t *Tracker) Flush(now time.Time) []model.FlowDelta {
	t.mu.Lock()
	defer t.mu.Unlock()

	agg := map[aggKey]*model.FlowDelta{}
	for k, f := range t.flows {
		if f.pkts > 0 {
			cid, sid := t.owner(k.client, f.clientMAC), t.owner(k.server, f.serverMAC)
			if cid != "" || sid != "" {
				bucket := f.last.Unix() / 60 * 60
				ak := aggKey{bucket, k.client, k.server, k.sport, k.proto, f.domain}
				d := agg[ak]
				if d == nil {
					d = &model.FlowDelta{
						Bucket: bucket, ClientID: cid, ClientIP: k.client.String(),
						ServerID: sid, ServerIP: k.server.String(), ServerPort: k.sport,
						Proto: protoName(k.proto), Domain: f.domain,
						FirstSeen: f.deltaFirst.UnixMilli(), LastSeen: f.last.UnixMilli(),
					}
					agg[ak] = d
				}
				d.BytesUp += f.up
				d.BytesDown += f.down
				d.Packets += f.pkts
				if f.isNew {
					d.Conns++
				}
				d.FirstSeen = min(d.FirstSeen, f.deltaFirst.UnixMilli())
				d.LastSeen = max(d.LastSeen, f.last.UnixMilli())
			}
			f.up, f.down, f.pkts, f.isNew = 0, 0, 0, false
		}
		idle := otherIdle
		if k.proto == decode.ProtoTCP {
			idle = tcpIdle
			if f.closing {
				idle = closeLinger
			}
		}
		if now.Sub(f.last) > idle {
			t.dropHello(f)
			delete(t.flows, k)
		}
	}
	out := make([]model.FlowDelta, 0, len(agg))
	for _, d := range agg {
		out = append(out, *d)
	}
	return out
}

func protoName(p uint8) string {
	switch p {
	case decode.ProtoTCP:
		return "tcp"
	case decode.ProtoUDP:
		return "udp"
	default:
		return "icmp"
	}
}
