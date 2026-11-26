package store

import (
	"context"
	"database/sql"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/istorry/tattle/internal/model"
)

// Presence is the record of which devices are on the network: every laptop,
// phone or television heard saying on a bridge that an address is its own.
// It holds that a device was there and when, never what it did.

const presenceSchema = `
CREATE TABLE IF NOT EXISTS presence (
	mac        TEXT NOT NULL,
	ip         TEXT NOT NULL,
	iface      TEXT NOT NULL, -- the bridge it was last heard on
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL,
	PRIMARY KEY (mac, ip)
) WITHOUT ROWID;
`

// addMachineBridges adds the column for a database created before machines
// recorded their bridges.
func addMachineBridges(db *sql.DB) error {
	var have int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('machines') WHERE name = 'bridges'`).Scan(&have); err != nil || have > 0 {
		return err
	}
	_, err := db.Exec(`ALTER TABLE machines ADD COLUMN bridges TEXT NOT NULL DEFAULT '[]'`)
	return err
}

// SavePresence records the devices heard on the network.
func (s *Store) SavePresence(sightings []model.Sighting) error {
	if len(sightings) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range sightings {
		if _, err := tx.Exec(`INSERT INTO presence (mac, ip, iface, first_seen, last_seen) VALUES (?1, ?2, ?3, ?4, ?4)
			ON CONFLICT (mac, ip) DO UPDATE SET iface = excluded.iface,
				first_seen = MIN(first_seen, excluded.first_seen), last_seen = MAX(last_seen, excluded.last_seen)`,
			v.MAC, v.IP, v.Iface, v.Seen); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InternetID is the map's node for everything outside the home network.
const InternetID = "internet"

// MapNode is one thing on the network map.
type MapNode struct {
	ID   string `json:"id"`   // a machine ID, or "dev-" and the hardware address
	Kind string `json:"kind"` // "host", "vm", "ct", "router" or "device"
	// Name is the machine's name. For a device it is the name its owner
	// chose, or "" if there is none.
	Name      string   `json:"name"`
	Announced string   `json:"announced"` // what a device calls itself, or ""
	MAC       string   `json:"mac"`
	IPs       []string `json:"ips"`     // the one to show first comes first
	Bridges   []string `json:"bridges"` // empty when it is not on a watched bridge
	FirstSeen int64    `json:"firstSeen"`
	LastSeen  int64    `json:"lastSeen"` // unix milliseconds; 0 if not heard from in the range
}

// MapLink is the traffic between two nodes, or between a node and the
// internet, in the time range.
type MapLink struct {
	A     string `json:"a"`
	B     string `json:"b"`
	Bytes int64  `json:"bytes"`
}

// Map is who is on the network and how much they exchanged.
type Map struct {
	Nodes []MapNode `json:"nodes"`
	Links []MapLink `json:"links"`
}

// Map returns the machines that exist, the devices heard since the given
// time, and the traffic between them. gateways are the addresses the host
// sends internet traffic through; the device holding one is the router.
func (s *Store) Map(ctx context.Context, since, now time.Time, gateways []string) (Map, error) {
	out := Map{Nodes: []MapNode{}, Links: []MapLink{}}
	machines, err := s.Machines(ctx, since, now, ScopeAll)
	if err != nil {
		return out, err
	}
	ids := map[string]bool{}
	machineMACs := map[string]bool{}
	for _, m := range machines {
		for _, mac := range m.MACs {
			machineMACs[strings.ToLower(mac)] = true
		}
		if !m.Present {
			continue
		}
		ids[m.ID] = true
		out.Nodes = append(out.Nodes, MapNode{ID: m.ID, Kind: m.Kind, Name: m.Name, MAC: strings.Join(m.MACs, ", "),
			IPs: orEmpty(m.IPs), Bridges: orEmpty(m.Bridges), LastSeen: m.LastSeen})
	}

	names := map[string]Device{}
	devices, err := s.Devices(ctx)
	if err != nil {
		return out, err
	}
	for _, d := range devices {
		names[d.IP] = d
	}

	// Oldest first, so that when an address has passed from one device to
	// another, the one that held it last is the one traffic is drawn to.
	rows, err := s.db.QueryContext(ctx, `SELECT mac, ip, iface, first_seen, last_seen FROM presence
		WHERE last_seen >= ? ORDER BY last_seen`, since.UnixMilli())
	if err != nil {
		return out, err
	}
	byMAC := map[string]*MapNode{}
	ipNode := map[string]string{}
	var order []string
	for rows.Next() {
		var mac, ip, iface string
		var first, last int64
		if err := rows.Scan(&mac, &ip, &iface, &first, &last); err != nil {
			rows.Close()
			return out, err
		}
		if machineMACs[mac] {
			continue // heard before Tattle knew it was a machine
		}
		n := byMAC[mac]
		if n == nil {
			n = &MapNode{ID: "dev-" + mac, Kind: "device", MAC: mac, IPs: []string{}, Bridges: []string{}, FirstSeen: first}
			byMAC[mac] = n
			order = append(order, mac)
		}
		n.IPs = append(n.IPs, ip)
		n.FirstSeen, n.LastSeen = min(n.FirstSeen, first), max(n.LastSeen, last)
		if iface != "" && !slices.Contains(n.Bridges, iface) {
			n.Bridges = append(n.Bridges, iface)
		}
		if slices.Contains(gateways, ip) {
			n.Kind = "router"
		}
		ipNode[ip] = n.ID
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	sort.Strings(order)
	for _, mac := range order {
		n := byMAC[mac]
		// The address to show: one with a name, then IPv4, then the most
		// recently heard.
		rank := func(ip string) int {
			d := names[ip]
			a, _ := netip.ParseAddr(ip)
			switch {
			case d.Name != "":
				return 0
			case d.Announced != "":
				return 1
			case a.Is4():
				return 2
			}
			return 3
		}
		slices.Reverse(n.IPs)
		sort.SliceStable(n.IPs, func(i, j int) bool { return rank(n.IPs[i]) < rank(n.IPs[j]) })
		for _, ip := range n.IPs {
			if ipNode[ip] != n.ID {
				continue // the address, and its name, have passed to another device
			}
			if d := names[ip]; n.Name == "" && d.Name != "" {
				n.Name = d.Name
			}
			if d := names[ip]; n.Announced == "" && d.Announced != "" {
				n.Announced = d.Announced
			}
		}
		out.Nodes = append(out.Nodes, *n)
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT client_id, client_ip, server_id, server_ip, SUM(bytes_up + bytes_down) FROM flows WHERE bucket >= ?
		GROUP BY CASE WHEN client_id != '' THEN client_id ELSE client_ip END,
			CASE WHEN server_id != '' THEN server_id ELSE server_ip END`, since.Unix())
	if err != nil {
		return out, err
	}
	end := func(id, ip string) string {
		switch {
		case id != "":
			if ids[id] {
				return id
			}
			return ""
		case ipNode[ip] != "":
			return ipNode[ip]
		case !IsLocal(ip):
			return InternetID
		}
		return "" // somewhere in the home network that is not on the map
	}
	totals := map[[2]string]int64{}
	for rows.Next() {
		var cid, cip, sid, sip string
		var bytes int64
		if err := rows.Scan(&cid, &cip, &sid, &sip, &bytes); err != nil {
			rows.Close()
			return out, err
		}
		a, b := end(cid, cip), end(sid, sip)
		if a == "" || b == "" || a == b {
			continue
		}
		if a > b {
			a, b = b, a
		}
		totals[[2]string{a, b}] += bytes
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	for k, v := range totals {
		out.Links = append(out.Links, MapLink{A: k[0], B: k[1], Bytes: v})
	}
	sort.Slice(out.Links, func(i, j int) bool {
		a, b := out.Links[i], out.Links[j]
		if a.Bytes != b.Bytes {
			return a.Bytes > b.Bytes
		}
		return a.A+a.B < b.A+b.B
	})
	return out, nil
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
