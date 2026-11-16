package store

import (
	"context"
	"strings"
	"time"
)

// PeerDetail is everything known about one place machines talked to
// (direction "out") or that talked to them (direction "in").
type PeerDetail struct {
	Name      string `json:"name"`
	Direction string `json:"direction"`
	PeerID    string `json:"peerId"` // set when the other end is also a tracked machine
	Local     bool   `json:"local"`
	// IPs are the addresses seen for it in the time range.
	IPs []string `json:"ips"`
	// FirstSeen is the earliest it appears anywhere in the kept history, not
	// just in the time range; zero if it never has.
	FirstSeen int64 `json:"firstSeen"`
	LastSeen  int64 `json:"lastSeen"`
	Sent      int64 `json:"sent"`
	Received  int64 `json:"received"`
	Conns     int64 `json:"conns"`

	Timeline      Timeline       `json:"timeline"`
	Conversations []Conversation `json:"conversations"`
}

// peerWhere selects the flows that make up one row of the Peers list, so
// the detail always adds up to the row that was clicked.
func peerWhere(direction, name, machine string) (string, []any) {
	if direction == "in" {
		if machine != "" {
			return `server_id = ? AND client_ip = ?`, []any{machine, name}
		}
		return `server_id != '' AND client_id = '' AND client_ip = ?`, []any{name}
	}
	where, args := `client_id != '' AND (CASE WHEN domain != '' THEN domain ELSE server_ip END) = ?`, []any{name}
	if machine != "" {
		where += ` AND client_id = ?`
		args = append(args, machine)
	}
	return where, args
}

// Peer returns the detail for one peer since the given time, optionally
// limited to one machine's dealings with it.
func (s *Store) Peer(ctx context.Context, since, now time.Time, direction, name, machine string) (PeerDetail, error) {
	if direction != "in" {
		direction = "out"
	}
	d := PeerDetail{Name: name, Direction: direction, IPs: []string{}, Conversations: []Conversation{}}
	where, args := peerWhere(direction, name, machine)
	// Amounts are from the machines' side: for an incoming connection the
	// machine is the server, so what it sends is the "down" direction.
	sent, received, ip, id := `bytes_up`, `bytes_down`, `server_ip`, `server_id`
	if direction == "in" {
		sent, received, ip, id = `bytes_down`, `bytes_up`, `client_ip`, `client_id`
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(first_seen), 0) FROM flows WHERE `+where, args...).Scan(&d.FirstSeen); err != nil {
		return d, err
	}

	var ips string
	inRange := append([]any{since.Unix()}, args...)
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(`+id+`), ''), COALESCE(GROUP_CONCAT(DISTINCT `+ip+`), ''),
			COALESCE(SUM(`+sent+`), 0), COALESCE(SUM(`+received+`), 0), COALESCE(SUM(conns), 0), COALESCE(MAX(last_seen), 0)
		FROM flows WHERE bucket >= ? AND `+where, inRange...).Scan(&d.PeerID, &ips, &d.Sent, &d.Received, &d.Conns, &d.LastSeen)
	if err != nil {
		return d, err
	}
	if ips != "" {
		d.IPs = strings.Split(ips, ",")
	}
	d.Local = d.PeerID != "" || (len(d.IPs) > 0 && IsLocal(d.IPs[0])) || (len(d.IPs) == 0 && IsLocal(name))

	step := stepFor(since, now, 60)
	start := since.Unix() / step * step
	n := int((now.Unix()-start)/step) + 1
	d.Timeline = Timeline{Step: step, Points: make([]Point, n)}
	for i := range d.Timeline.Points {
		d.Timeline.Points[i].T = start + int64(i)*step
	}
	rows, err := s.db.QueryContext(ctx, `SELECT (bucket - ?) / ?, SUM(`+sent+`), SUM(`+received+`)
		FROM flows WHERE bucket >= ? AND `+where+` GROUP BY 1`, append([]any{start, step, since.Unix()}, args...)...)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var slot, up, down int64
		if err := rows.Scan(&slot, &up, &down); err != nil {
			rows.Close()
			return d, err
		}
		if slot >= 0 && int(slot) < n {
			d.Timeline.Points[slot].Sent, d.Timeline.Points[slot].Received = up, down
		}
	}
	if err := rows.Close(); err != nil {
		return d, err
	}

	// One line per machine, address and service.
	rows, err = s.db.QueryContext(ctx, `
		SELECT MAX(client_id), client_ip, MAX(server_id), server_ip, server_port, proto, MAX(domain),
			SUM(`+sent+`), SUM(`+received+`), SUM(conns), MIN(first_seen), MAX(last_seen) AS last
		FROM flows WHERE bucket >= ? AND `+where+`
		GROUP BY client_ip, server_ip, server_port, proto
		ORDER BY last DESC LIMIT 500`, inRange...)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ClientID, &c.ClientIP, &c.ServerID, &c.ServerIP, &c.Port, &c.Proto, &c.Domain,
			&c.Sent, &c.Received, &c.Conns, &c.FirstSeen, &c.LastSeen); err != nil {
			return d, err
		}
		c.Local = d.Local
		d.Conversations = append(d.Conversations, c)
	}
	return d, rows.Err()
}

// Conversation is one machine's use of one service at one address, totalled
// over the time range. Sent and Received are from the machine's side.
type Conversation struct {
	ClientID  string `json:"clientId"`
	ClientIP  string `json:"clientIp"`
	ServerID  string `json:"serverId"`
	ServerIP  string `json:"serverIp"`
	Port      int    `json:"port"`
	Proto     string `json:"proto"`
	Domain    string `json:"domain"`
	Local     bool   `json:"local"`
	Sent      int64  `json:"sent"`
	Received  int64  `json:"received"`
	Conns     int64  `json:"conns"`
	FirstSeen int64  `json:"firstSeen"`
	LastSeen  int64  `json:"lastSeen"`
}
