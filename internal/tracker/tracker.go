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
	maxAnnounced = 5_000
	tcpIdle      = 5 * time.Minute
	otherIdle    = 60 * time.Second
	closeLinger  = 5 * time.Second
	sniffPackets = 6 // client packets inspected for a hostname before giving up

	// Limits on joining a ClientHello that spans several packets.
	maxHelloBytes    = 16 << 10
	maxHelloSegments = 12
	maxHelloPending  = 4    // out-of-order TCP packets held back
	maxQUICPending   = 48   // QUIC clients scatter the handshake on purpose
	maxHellosAtOnce  = 4096 // across all connections, so a flood cannot use much memory
)

type flowKey struct {
	client, server netip.Addr
	cport, sport   uint16
	proto          uint8
}

// view is one place a connection can be observed. A connection the host
// forwards crosses the watched bridges twice, before and after the host,
// so it has two views. Each view sees all of a direction's packets or, when
// address translation makes them look like another connection there, none.
// The true total for a direction is therefore the largest any view counted,
// which stays right whichever view a packet happens to be read from first.
type view struct {
	iface int
	// The host's network card is on this side, but the address on this side
	// is not the host's: the host is passing the traffic along.
	hostForClient, hostForServer bool
}

// viewCount is what one view has seen of a connection since it began.
type viewCount struct {
	view                       view
	up, down, upPkts, downPkts int64
}

const maxViews = 4

type flow struct {
	clientMAC, serverMAC decode.MAC
	views                []viewCount
	direct               int  // most ends shown by a guest's own network card in any view
	fwdClient, fwdServer bool // the host passes this connection along, in some view
	sniffView            int  // the view whose packets are searched for a name, or -1
	domain               string
	named                bool // domain came from the connection itself, not from DNS
	sniffed              int
	hello                *helloBuf // a ClientHello still being joined together, if any
	closing              bool
	isNew                bool
	last                 time.Time

	// What has already been reported, and whether there is more.
	sentUp, sentDown, sentPkts int64
	dirty                      bool
	deltaFirst                 time.Time
}

type addrPair struct{ client, server netip.Addr }

// helloBuf joins the pieces of a TLS ClientHello in order, so a server
// name that falls in the second or third packet can still be read. Pieces
// are placed by TCP sequence number, or by handshake offset for QUIC.
type helloBuf struct {
	start      uint32 // position of data[0]
	data       []byte
	pending    []helloSeg // pieces that arrived ahead of a gap
	packets    int
	maxPending int
	parse      func([]byte) (string, decode.HelloResult)
}

type helloSeg struct {
	seq  uint32
	data []byte
}

// newTCPHello starts from the first data packet of a TCP connection.
func newTCPHello(seq uint32, first []byte) *helloBuf {
	return &helloBuf{start: seq, data: append([]byte(nil), first...), packets: 1,
		maxPending: maxHelloPending, parse: decode.ParseClientHello}
}

// newQUICHello starts empty: QUIC pieces carry their own offsets from zero.
func newQUICHello() *helloBuf {
	return &helloBuf{maxPending: maxQUICPending, parse: decode.ParseHelloMessage}
}

// place adds a piece if it continues the data, and reports whether it was
// at or before the end of it (so not waiting on a gap).
func (h *helloBuf) place(seq uint32, data []byte) bool {
	off := int(int32(seq - h.start)) // signed, so sequence wrap-around works
	if off > len(h.data) {
		return false
	}
	if end := off + len(data); off >= 0 && end > len(h.data) {
		h.data = append(h.data, data[len(h.data)-off:]...)
	}
	return true // a repeat adds nothing
}

// put takes one piece, holding it back if it is ahead of a gap. It reports
// false when there is no room to hold it.
func (h *helloBuf) put(seq uint32, data []byte) bool {
	if h.place(seq, data) {
		return true
	}
	if len(h.pending) >= h.maxPending {
		return false
	}
	h.pending = append(h.pending, helloSeg{seq, append([]byte(nil), data...)})
	return true
}

// check is called once per packet, after its pieces were put. done reports
// that no more packets are wanted, either because the name was found or it
// never will be.
func (h *helloBuf) check(truncated bool) (name string, done bool) {
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
	name, res := h.parse(h.data)
	switch {
	case res == decode.HelloFound:
		return name, true
	case res == decode.HelloNone:
		return "", true
	}
	// A cut-short frame leaves a hole that later packets cannot fill.
	return "", truncated || len(h.data) > maxHelloBytes || h.packets >= maxHelloSegments
}

// Tracker is safe for concurrent use by several capture goroutines.
type Tracker struct {
	mu    sync.Mutex
	flows map[flowKey]*flow
	// announced holds names devices have given for their addresses since
	// the last time they were collected.
	announced map[netip.Addr]string
	hellos    int // connections currently joining a ClientHello

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

	// Devices say what they are called in broadcasts that reach the bridge.
	if p.Proto == decode.ProtoUDP {
		switch {
		case p.SrcPort == 5353 && p.DstPort == 5353:
			for _, h := range decode.ParseMDNSHosts(p.Payload) {
				t.announce(h)
			}
		case p.SrcPort == 68 && p.DstPort == 67:
			if h, ok := decode.ParseDHCPRequest(p.Payload); ok {
				t.announce(h)
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
			key = rev
		}
	}
	created := f == nil
	if created {
		if len(t.flows) >= maxFlows {
			return
		}
		if fromClient = srcIsClient(p); !fromClient {
			key = flowKey{p.DstIP, p.SrcIP, p.DstPort, p.SrcPort, p.Proto}
		}
		f = &flow{isNew: true, sniffView: -1}
		if name, ok := t.dnsPair[addrPair{key.client, key.server}]; ok {
			f.domain = name
		} else {
			f.domain = t.dnsAny[key.server]
		}
		t.flows[key] = f
	}

	// Count the packet in the view it was seen in.
	clientMAC, serverMAC := p.SrcMAC, p.DstMAC
	if !fromClient {
		clientMAC, serverMAC = p.DstMAC, p.SrcMAC
	}
	v := view{
		iface:         p.Iface,
		hostForClient: t.hostForwards(clientMAC, key.client),
		hostForServer: t.hostForwards(serverMAC, key.server),
	}
	vi := -1
	for i := range f.views {
		if f.views[i].view == v {
			vi = i
			break
		}
	}
	if vi < 0 {
		if len(f.views) >= maxViews {
			return
		}
		f.views = append(f.views, viewCount{view: v})
		vi = len(f.views) - 1
		f.fwdClient = f.fwdClient || v.hostForClient
		f.fwdServer = f.fwdServer || v.hostForServer
		// The view that shows a guest's own network card identifies the
		// guest even before its address is known.
		if direct := t.guestCard(clientMAC) + t.guestCard(serverMAC); vi == 0 || direct > f.direct {
			f.direct, f.clientMAC, f.serverMAC = direct, clientMAC, serverMAC
		}
	}
	if !f.dirty {
		f.dirty, f.deltaFirst = true, now
	}
	f.last = now
	if vc := &f.views[vi]; fromClient {
		vc.up += int64(wireLen)
		vc.upPkts++
	} else {
		vc.down += int64(wireLen)
		vc.downPkts++
	}
	// Search one view's packets for the name, so a packet seen in two views
	// is not fed in twice.
	if fromClient && len(p.Payload) > 0 && f.sniffView < 0 {
		f.sniffView = vi
	}
	sniff := vi == f.sniffView

	if p.Proto == decode.ProtoTCP {
		if p.TCPFlags&(decode.FlagFIN|decode.FlagRST) != 0 {
			f.closing = true
		}
		// The hostname is in the first data the client sends: the TLS
		// ClientHello for HTTPS, or the Host header for plain HTTP.
		if sniff && fromClient && !f.named && len(p.Payload) > 0 {
			name := ""
			switch {
			case f.hello != nil:
				f.hello.packets++
				done := !f.hello.put(p.Seq, p.Payload)
				if !done {
					name, done = f.hello.check(p.Truncated)
				}
				if done {
					t.dropHello(f)
				}
			case f.sniffed < sniffPackets:
				f.sniffed++
				var res decode.HelloResult
				name, res = decode.ParseClientHello(p.Payload)
				switch {
				case res == decode.HelloIncomplete && !p.Truncated && t.hellos < maxHellosAtOnce:
					f.hello = newTCPHello(p.Seq, p.Payload)
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

	// HTTP/3: the same ClientHello, inside the connection's first QUIC
	// packets. Only the client's opening packets are tried.
	if p.Proto == decode.ProtoUDP && sniff && fromClient && !f.named && !p.Truncated && f.sniffed < sniffPackets {
		f.sniffed++
		frags, ok := decode.QUICClientInitial(p.Payload)
		if ok && f.hello == nil && t.hellos < maxHellosAtOnce {
			f.hello = newQUICHello()
			t.hellos++
		}
		if ok && f.hello != nil {
			f.hello.packets++
			done := false
			for _, fr := range frags {
				if !f.hello.put(uint32(fr.Offset), fr.Data) {
					done = true
				}
			}
			name := ""
			if !done {
				name, done = f.hello.check(false)
			}
			if name != "" {
				f.domain, f.named = name, true
			}
			if done {
				t.dropHello(f)
			}
		}
		if f.sniffed == sniffPackets {
			t.dropHello(f) // the name never came; stop holding the pieces
		}
	}
}

func (t *Tracker) dropHello(f *flow) {
	if f.hello != nil {
		f.hello = nil
		t.hellos--
	}
}

func (t *Tracker) announce(h decode.HostAddr) {
	if t.announced == nil {
		t.announced = map[netip.Addr]string{}
	}
	if len(t.announced) < maxAnnounced {
		t.announced[h.Addr.Unmap()] = h.Name
	}
}

// TakeDeviceNames returns the names devices have announced for their
// addresses since the last call.
func (t *Tracker) TakeDeviceNames() map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]string, len(t.announced))
	for addr, name := range t.announced {
		out[addr.String()] = name
	}
	t.announced = nil
	return out
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

// hostForwards reports whether a frame's network card is the host's while
// the address is someone else's, meaning the host is routing the traffic.
func (t *Tracker) hostForwards(mac decode.MAC, ip netip.Addr) bool {
	return t.macOwner[mac] == model.HostID && t.ipOwner[ip] != model.HostID
}

// guestCard is 1 if the network card belongs to a VM or container.
func (t *Tracker) guestCard(mac decode.MAC) int {
	if id := t.macOwner[mac]; id != "" && id != model.HostID {
		return 1
	}
	return 0
}

// natKey identifies a connection by the parts that address translation on
// the host leaves alone.
type natKey struct {
	proto uint8
	ip    netip.Addr
	a, b  uint16
}

// natCopies finds connections that are the host's translated copy of a
// connection already counted for a guest.
//
// Outgoing (masquerade): the guest's connection reaches the host's card on
// its way to the internet, and leaves again from the host's own address
// with the same source port. Incoming (port forward): a connection arrives
// at the host's address and is passed to the guest with the caller's
// address and port unchanged.
func (t *Tracker) natCopies() (out, in map[natKey]bool) {
	out, in = map[natKey]bool{}, map[natKey]bool{}
	for k, f := range t.flows {
		if k.proto != decode.ProtoTCP && k.proto != decode.ProtoUDP {
			continue // without ports there is nothing reliable to match on
		}
		if f.fwdServer {
			out[natKey{k.proto, k.server, k.cport, k.sport}] = true
		}
		if f.fwdClient {
			in[natKey{k.proto, k.client, k.cport, 0}] = true
		}
	}
	return out, in
}

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
	natOut, natIn := t.natCopies()
	for k, f := range t.flows {
		if f.dirty {
			// Per direction, the view that saw the most saw all of it.
			var up, down, upPkts, downPkts int64
			for _, vc := range f.views {
				up, down = max(up, vc.up), max(down, vc.down)
				upPkts, downPkts = max(upPkts, vc.upPkts), max(downPkts, vc.downPkts)
			}
			pkts := upPkts + downPkts
			cid, sid := t.owner(k.client, f.clientMAC), t.owner(k.server, f.serverMAC)
			natCopy := false
			if !f.fwdClient && !f.fwdServer {
				// The guest's connection after masquerading, or a port
				// forward before it reaches the guest.
				natCopy = (cid == model.HostID && natOut[natKey{k.proto, k.server, k.cport, k.sport}]) ||
					(sid == model.HostID && natIn[natKey{k.proto, k.client, k.cport, 0}])
			}
			known := cid != "" || sid != ""
			if known && !natCopy {
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
				d.BytesUp += up - f.sentUp
				d.BytesDown += down - f.sentDown
				d.Packets += pkts - f.sentPkts
				if f.isNew {
					d.Conns++
				}
				d.FirstSeen = min(d.FirstSeen, f.deltaFirst.UnixMilli())
				d.LastSeen = max(d.LastSeen, f.last.UnixMilli())
			}
			// Traffic that cannot be attributed yet is kept, not discarded:
			// the machine's address may be learned a moment later, or a
			// better view of the connection may arrive.
			if known || natCopy {
				f.sentUp, f.sentDown, f.sentPkts = up, down, pkts
				f.dirty, f.isNew = false, false
			}
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
