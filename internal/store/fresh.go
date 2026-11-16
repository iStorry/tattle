package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/istorry/tattle/internal/model"
)

// "What's new" rests on a record of the first time each machine dealt with
// each place. It is kept apart from the traffic history and for much
// longer, so a monthly backup target is not announced as new every month
// just because the history only goes back a week.

const (
	// LearningPeriod is how long a machine is watched before anything it
	// does is called new. Without it, everything a freshly installed Tattle
	// or a freshly created machine does would be.
	LearningPeriod = 24 * time.Hour
	// sightingLife is how long a place is remembered after it was last seen.
	sightingLife = 400 * 24 * time.Hour
)

const freshSchema = `
CREATE TABLE IF NOT EXISTS seen (
	machine    TEXT NOT NULL,
	direction  TEXT NOT NULL, -- 'out': the machine contacted it; 'in': it contacted the machine
	name       TEXT NOT NULL, -- domain if known, otherwise the address
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL,
	ip         TEXT NOT NULL, -- as of the first sighting, like the three below
	peer_id    TEXT NOT NULL,
	port       INTEGER NOT NULL,
	proto      TEXT NOT NULL,
	PRIMARY KEY (machine, direction, name)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS seen_first ON seen (first_seen);
CREATE TABLE IF NOT EXISTS learning (
	machine TEXT PRIMARY KEY,
	since   INTEGER NOT NULL -- when the machine was first seen doing anything
) WITHOUT ROWID;
`

// sighting is what a flow says about who dealt with whom.
type sighting struct {
	machine, direction, name, ip, peerID string
}

// sightingOf returns the sighting a flow represents. Traffic between two
// machines is recorded once, for the one that started it.
func sightingOf(d model.FlowDelta) (sighting, bool) {
	switch {
	case d.ClientID != "":
		name := d.Domain
		if name == "" {
			name = d.ServerIP
		}
		return sighting{d.ClientID, "out", name, d.ServerIP, d.ServerID}, true
	case d.ServerID != "":
		return sighting{d.ServerID, "in", d.ClientIP, d.ClientIP, ""}, true
	}
	return sighting{}, false
}

// recordSightings notes first and latest sightings as part of saving traffic.
func recordSightings(tx *sql.Tx, deltas []model.FlowDelta) error {
	seen, err := tx.Prepare(`
		INSERT INTO seen (machine, direction, name, first_seen, last_seen, ip, peer_id, port, proto)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (machine, direction, name) DO UPDATE SET
			first_seen = min(first_seen, excluded.first_seen),
			last_seen  = max(last_seen, excluded.last_seen)`)
	if err != nil {
		return err
	}
	defer seen.Close()
	learning, err := tx.Prepare(`INSERT INTO learning (machine, since) VALUES (?, ?)
		ON CONFLICT (machine) DO UPDATE SET since = min(since, excluded.since)`)
	if err != nil {
		return err
	}
	defer learning.Close()
	for _, d := range deltas {
		s, ok := sightingOf(d)
		if !ok {
			continue
		}
		if _, err := seen.Exec(s.machine, s.direction, s.name, d.FirstSeen, d.LastSeen, s.ip, s.peerID, d.ServerPort, d.Proto); err != nil {
			return err
		}
		if _, err := learning.Exec(s.machine, d.FirstSeen); err != nil {
			return err
		}
		// The machine at the receiving end of machine-to-machine traffic
		// has been seen too, even though the sighting is not its own.
		if d.ClientID != "" && d.ServerID != "" {
			if _, err := learning.Exec(d.ServerID, d.FirstSeen); err != nil {
				return err
			}
		}
	}
	return nil
}

// backfillSightings builds the record from existing history, for a database
// created before sightings were kept.
func backfillSightings(db *sql.DB) error {
	var have, flows int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM seen), (SELECT COUNT(*) FROM flows)`).Scan(&have, &flows); err != nil {
		return err
	}
	if have > 0 || flows == 0 {
		return nil
	}
	_, err := db.Exec(`
		INSERT INTO seen (machine, direction, name, first_seen, last_seen, ip, peer_id, port, proto)
		SELECT client_id, 'out', CASE WHEN domain != '' THEN domain ELSE server_ip END AS name,
			MIN(first_seen), MAX(last_seen), MIN(server_ip), MAX(server_id), MIN(server_port), MIN(proto)
		FROM flows WHERE client_id != '' GROUP BY client_id, name;
		INSERT INTO seen (machine, direction, name, first_seen, last_seen, ip, peer_id, port, proto)
		SELECT server_id, 'in', client_ip, MIN(first_seen), MAX(last_seen), client_ip, '', MIN(server_port), MIN(proto)
		FROM flows WHERE server_id != '' AND client_id = '' GROUP BY server_id, client_ip;
		INSERT INTO learning (machine, since)
		SELECT id, MIN(t) FROM (
			SELECT client_id AS id, first_seen AS t FROM flows WHERE client_id != ''
			UNION ALL SELECT server_id, first_seen FROM flows WHERE server_id != ''
		) GROUP BY id;`)
	return err
}

// First is the first time a machine dealt with a place.
type First struct {
	Machine   string `json:"machine"`
	Direction string `json:"direction"` // "out" or "in"
	Name      string `json:"name"`
	FirstSeen int64  `json:"firstSeen"`
	IP        string `json:"ip"`
	PeerID    string `json:"peerId"`
	Port      int    `json:"port"`
	Proto     string `json:"proto"`
	Local     bool   `json:"local"`
	// Owner is the network its address belongs to, when known; set by the API.
	Owner *model.Owner `json:"owner,omitempty"`
}

// Learning says a machine is still inside its learning period.
type Learning struct {
	Machine string `json:"machine"`
	Until   int64  `json:"until"`
}

// Fresh is the answer to "what's new?".
type Fresh struct {
	Firsts []First `json:"firsts"`
	// More is how many further firsts there are beyond the ones listed.
	More     int        `json:"more"`
	Learning []Learning `json:"learning"`
}

// maxFirsts bounds how many firsts are considered at all.
const maxFirsts = 2000

// firsts returns the first sightings since the given time, newest first,
// leaving out machines' learning periods.
func (s *Store) firsts(ctx context.Context, since time.Time, machine, scope string) ([]First, error) {
	q := `SELECT s.machine, s.direction, s.name, s.first_seen, s.ip, s.peer_id, s.port, s.proto
		FROM seen s JOIN learning l ON l.machine = s.machine
		WHERE s.first_seen >= ? AND s.first_seen >= l.since + ?`
	args := []any{since.UnixMilli(), LearningPeriod.Milliseconds()}
	if machine != "" {
		q += ` AND s.machine = ?`
		args = append(args, machine)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY s.first_seen DESC LIMIT ?`, append(args, maxFirsts)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []First{}
	for rows.Next() {
		var f First
		if err := rows.Scan(&f.Machine, &f.Direction, &f.Name, &f.FirstSeen, &f.IP, &f.PeerID, &f.Port, &f.Proto); err != nil {
			return nil, err
		}
		f.Local = f.PeerID != "" || IsLocal(f.IP)
		if (scope == ScopeInternet && f.Local) || (scope == ScopeLocal && !f.Local) {
			continue
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Fresh lists what machines did for the first time since the given time.
func (s *Store) Fresh(ctx context.Context, since, now time.Time, machine, scope string, limit int) (Fresh, error) {
	out := Fresh{Learning: []Learning{}}
	all, err := s.firsts(ctx, since, machine, scope)
	if err != nil {
		return out, err
	}
	out.Firsts = all
	if len(all) > limit {
		out.Firsts, out.More = all[:limit], len(all)-limit
	}

	q, args := `SELECT machine, since FROM learning WHERE since > ?`, []any{now.Add(-LearningPeriod).UnixMilli()}
	if machine != "" {
		q += ` AND machine = ?`
		args = append(args, machine)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY since`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var l Learning
		if err := rows.Scan(&l.Machine, &l.Until); err != nil {
			return out, err
		}
		l.Until += LearningPeriod.Milliseconds()
		out.Learning = append(out.Learning, l)
	}
	return out, rows.Err()
}

// markNew flags the peers that include a first sighting since the given time.
func (s *Store) markNew(ctx context.Context, peers []Peer, since time.Time, machine string) error {
	all, err := s.firsts(ctx, since, machine, ScopeAll)
	if err != nil {
		return err
	}
	type key struct{ direction, name string }
	fresh := make(map[key]bool, len(all))
	for _, f := range all {
		fresh[key{f.Direction, f.Name}] = true
	}
	for i := range peers {
		peers[i].New = fresh[key{peers[i].Direction, peers[i].Name}]
	}
	return nil
}
