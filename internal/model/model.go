// Package model holds the types shared between the collector, the store and the API.
package model

// HostID is the machine ID of the Proxmox host itself.
const HostID = "host"

// Machine is something on the Proxmox host whose traffic Tattle attributes:
// a VM, a container, or the host itself.
type Machine struct {
	ID   string   `json:"id"`   // "vm-100", "ct-101", "host"
	Kind string   `json:"kind"` // "vm", "ct", "host"
	VMID int      `json:"vmid"`
	Name string   `json:"name"`
	Node string   `json:"node"`
	MACs []string `json:"macs"`
	IPs  []string `json:"ips"`
	// Bridges are the bridges its network cards are attached to.
	Bridges []string `json:"bridges"`
}

// Sighting is a device that is not a machine, heard saying on a bridge that
// an address is its own.
type Sighting struct {
	MAC   string
	IP    string
	Iface string // the bridge it was heard on
	Seen  int64  // unix milliseconds
}

// FlowDelta is traffic seen since the last flush for one
// (client, server, port, protocol, domain) combination.
// "Up" is client-to-server, "down" is server-to-client.
type FlowDelta struct {
	Bucket     int64 // start of the minute, unix seconds
	ClientID   string
	ClientIP   string
	ServerID   string
	ServerIP   string
	ServerPort uint16
	Proto      string // "tcp", "udp", "icmp"
	Domain     string
	BytesUp    int64
	BytesDown  int64
	Packets    int64
	Conns      int64
	FirstSeen  int64 // unix milliseconds
	LastSeen   int64 // unix milliseconds
}

// Owner is the network an internet address belongs to.
type Owner struct {
	Name string `json:"name"` // the organisation, such as "Cloudflare, Inc."
	ASN  uint32 `json:"asn"`  // its network number
	// Country is where the network is registered, as a two-letter code, or
	// "". It is not where the server answering is located.
	Country string `json:"country"`
}
