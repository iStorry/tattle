// Package store keeps Tattle's history in a single SQLite file.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/istorry/tattle/internal/model"
)

const schema = `
CREATE TABLE IF NOT EXISTS flows (
	bucket      INTEGER NOT NULL,
	client_ip   TEXT NOT NULL,
	server_ip   TEXT NOT NULL,
	server_port INTEGER NOT NULL,
	proto       TEXT NOT NULL,
	domain      TEXT NOT NULL,
	client_id   TEXT NOT NULL,
	server_id   TEXT NOT NULL,
	bytes_up    INTEGER NOT NULL,
	bytes_down  INTEGER NOT NULL,
	packets     INTEGER NOT NULL,
	conns       INTEGER NOT NULL,
	first_seen  INTEGER NOT NULL,
	last_seen   INTEGER NOT NULL,
	PRIMARY KEY (bucket, client_ip, server_ip, server_port, proto, domain)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS flows_client ON flows (client_id, bucket);
CREATE INDEX IF NOT EXISTS flows_server ON flows (server_id, bucket);
CREATE TABLE IF NOT EXISTS machines (
	id      TEXT PRIMARY KEY,
	kind    TEXT NOT NULL,
	vmid    INTEGER NOT NULL,
	name    TEXT NOT NULL,
	node    TEXT NOT NULL,
	macs    TEXT NOT NULL,
	ips     TEXT NOT NULL,
	present INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	expires    INTEGER NOT NULL
) WITHOUT ROWID;
`

type Store struct{ db *sql.DB }

// Open opens the database at path, creating it if needed. An empty path
// keeps everything in memory, which demo mode uses.
func Open(path string) (*Store, error) {
	dsn := ":memory:"
	if path != "" {
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps writes serialised and makes the in-memory
	// database (which is per-connection) behave like a file.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema + freshSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening database %s: %w", path, err)
	}
	if err := backfillSightings(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("preparing database %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Apply adds traffic totals to the history.
func (s *Store) Apply(deltas []model.FlowDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO flows (bucket, client_ip, server_ip, server_port, proto, domain, client_id, server_id,
			bytes_up, bytes_down, packets, conns, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (bucket, client_ip, server_ip, server_port, proto, domain) DO UPDATE SET
			client_id  = excluded.client_id,
			server_id  = excluded.server_id,
			bytes_up   = bytes_up + excluded.bytes_up,
			bytes_down = bytes_down + excluded.bytes_down,
			packets    = packets + excluded.packets,
			conns      = conns + excluded.conns,
			first_seen = min(first_seen, excluded.first_seen),
			last_seen  = max(last_seen, excluded.last_seen)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, d := range deltas {
		if _, err := stmt.Exec(d.Bucket, d.ClientIP, d.ServerIP, d.ServerPort, d.Proto, d.Domain,
			d.ClientID, d.ServerID, d.BytesUp, d.BytesDown, d.Packets, d.Conns, d.FirstSeen, d.LastSeen); err != nil {
			return err
		}
	}
	if err := recordSightings(tx, deltas); err != nil {
		return err
	}
	return tx.Commit()
}

// Prune deletes history older than the cutoff, and forgets places that
// have not been seen for much longer than that.
func (s *Store) Prune(before time.Time) error {
	if _, err := s.db.Exec(`DELETE FROM flows WHERE bucket < ?`, before.Unix()); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM seen WHERE last_seen < ?`, time.Now().Add(-sightingLife).UnixMilli())
	return err
}

// SaveMachines records the machines that exist right now. Machines that
// have been deleted from Proxmox are kept, marked absent, so their history
// still has a name.
func (s *Store) SaveMachines(machines []model.Machine) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE machines SET present = 0`); err != nil {
		return err
	}
	for _, m := range machines {
		macs, _ := json.Marshal(m.MACs)
		ips, _ := json.Marshal(m.IPs)
		if _, err := tx.Exec(`
			INSERT INTO machines (id, kind, vmid, name, node, macs, ips, present) VALUES (?, ?, ?, ?, ?, ?, ?, 1)
			ON CONFLICT (id) DO UPDATE SET kind = excluded.kind, vmid = excluded.vmid, name = excluded.name,
				node = excluded.node, macs = excluded.macs, ips = excluded.ips, present = 1`,
			m.ID, m.Kind, m.VMID, m.Name, m.Node, string(macs), string(ips)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MachineStats is a machine together with what it did in the time range.
type MachineStats struct {
	model.Machine
	Present      bool    `json:"present"`
	Sent         int64   `json:"sent"`
	Received     int64   `json:"received"`
	Conns        int64   `json:"conns"`
	Destinations int64   `json:"destinations"`
	LastSeen     int64   `json:"lastSeen"`
	Spark        []int64 `json:"spark"`
}

const sparkPoints = 30

// Machines lists every machine with its totals since the given time.
func (s *Store) Machines(ctx context.Context, since, now time.Time, scope string) ([]MachineStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, vmid, name, node, macs, ips, present FROM machines
		ORDER BY CASE kind WHEN 'host' THEN 0 ELSE 1 END, vmid`)
	if err != nil {
		return nil, err
	}
	var out []MachineStats
	index := map[string]int{}
	for rows.Next() {
		var m MachineStats
		var macs, ips string
		if err := rows.Scan(&m.ID, &m.Kind, &m.VMID, &m.Name, &m.Node, &macs, &ips, &m.Present); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(macs), &m.MACs)
		_ = json.Unmarshal([]byte(ips), &m.IPs)
		m.Spark = make([]int64, sparkPoints)
		index[m.ID] = len(out)
		out = append(out, m)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// A machine's view of a connection depends on which end it is: what the
	// client sends is what the server receives.
	perMachine := `
		SELECT client_id AS id, bucket, bytes_up AS sent, bytes_down AS received, conns, last_seen
			FROM flows WHERE bucket >= ?1 AND client_id != ''` + scopeSQL(scope) + `
		UNION ALL
		SELECT server_id, bucket, bytes_down, bytes_up, conns, last_seen
			FROM flows WHERE bucket >= ?1 AND server_id != ''` + scopeSQL(scope)

	rows, err = s.db.QueryContext(ctx, `SELECT id, SUM(sent), SUM(received), SUM(conns), MAX(last_seen)
		FROM (`+perMachine+`) GROUP BY id`, since.Unix())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var sent, received, conns, last int64
		if err := rows.Scan(&id, &sent, &received, &conns, &last); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := index[id]; ok {
			out[i].Sent, out[i].Received, out[i].Conns, out[i].LastSeen = sent, received, conns, last
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT client_id, COUNT(DISTINCT CASE WHEN domain != '' THEN domain ELSE server_ip END)
		FROM flows WHERE bucket >= ? AND client_id != ''`+scopeSQL(scope)+` GROUP BY client_id`, since.Unix())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := index[id]; ok {
			out[i].Destinations = n
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	step := stepFor(since, now, sparkPoints)
	start := since.Unix() / step * step
	rows, err = s.db.QueryContext(ctx, `SELECT id, (bucket - ?2) / ?3, SUM(sent + received)
		FROM (`+perMachine+`) GROUP BY 1, 2`, since.Unix(), start, step)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var slot, total int64
		if err := rows.Scan(&id, &slot, &total); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := index[id]; ok && slot >= 0 {
			out[i].Spark[min(slot, sparkPoints-1)] += total
		}
	}
	return out, rows.Close()
}

// stepFor picks a bucket width, in seconds, that splits the range into
// roughly the requested number of points and is a whole number of minutes.
func stepFor(since, now time.Time, points int) int64 {
	span := now.Unix() - since.Unix()
	step := (span/int64(points) + 59) / 60 * 60
	return max(step, 60)
}

// Point is one bar of the traffic chart.
type Point struct {
	T        int64 `json:"t"` // unix seconds, start of the interval
	Sent     int64 `json:"sent"`
	Received int64 `json:"received"`
}

// Timeline is traffic over time, from the point of view of one machine or,
// when machine is empty, of all tracked machines together.
type Timeline struct {
	Step   int64   `json:"step"`
	Points []Point `json:"points"`
}

func (s *Store) Timeline(ctx context.Context, since, now time.Time, machine, scope string) (Timeline, error) {
	step := stepFor(since, now, 60)
	start := since.Unix() / step * step
	q := `SELECT (bucket - ?2) / ?3,
			SUM(CASE WHEN client_id != '' THEN bytes_up ELSE bytes_down END),
			SUM(CASE WHEN client_id != '' THEN bytes_down ELSE bytes_up END)
		FROM flows WHERE bucket >= ?1` + scopeSQL(scope) + ` GROUP BY 1`
	args := []any{since.Unix(), start, step}
	if machine != "" {
		q = `SELECT (bucket - ?2) / ?3,
				SUM(CASE WHEN client_id = ?4 THEN bytes_up ELSE bytes_down END),
				SUM(CASE WHEN client_id = ?4 THEN bytes_down ELSE bytes_up END)
			FROM flows WHERE bucket >= ?1 AND (client_id = ?4 OR server_id = ?4)` + scopeSQL(scope) + ` GROUP BY 1`
		args = append(args, machine)
	}
	n := int((now.Unix()-start)/step) + 1
	tl := Timeline{Step: step, Points: make([]Point, n)}
	for i := range tl.Points {
		tl.Points[i].T = start + int64(i)*step
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return tl, err
	}
	for rows.Next() {
		var slot, sent, received int64
		if err := rows.Scan(&slot, &sent, &received); err != nil {
			rows.Close()
			return tl, err
		}
		if slot >= 0 && int(slot) < n {
			tl.Points[slot].Sent, tl.Points[slot].Received = sent, received
		}
	}
	return tl, rows.Close()
}

// Peer is one place that machines talked to (direction "out") or that
// talked to them (direction "in"), with totals from the machines' side.
type Peer struct {
	Name      string   `json:"name"`      // domain if known, otherwise the address
	Direction string   `json:"direction"` // "out" or "in"
	PeerID    string   `json:"peerId"`    // set when the other end is also a tracked machine
	Local     bool     `json:"local"`     // the other end is inside the home network
	New       bool     `json:"new"`       // a machine dealt with it for the first time in the range
	IP        string   `json:"ip"`        // one of its addresses
	IPCount   int64    `json:"ipCount"`
	Machines  []string `json:"machines"`
	Services  []string `json:"services"` // "443/tcp"
	Sent      int64    `json:"sent"`
	Received  int64    `json:"received"`
	Conns     int64    `json:"conns"`
	FirstSeen int64    `json:"firstSeen"`
	LastSeen  int64    `json:"lastSeen"`
}

// Peers lists who was talked to since the given time, most recent first.
func (s *Store) Peers(ctx context.Context, since time.Time, machine, scope string, limit int) ([]Peer, error) {
	outWhere, inWhere := `client_id != ''`, `server_id != '' AND client_id = ''`
	args := []any{since.Unix()}
	if machine != "" {
		outWhere, inWhere = `client_id = ?2`, `server_id = ?2`
		args = append(args, machine)
	}
	outWhere += scopeSQL(scope)
	inWhere += scopeSQL(scope)
	q := `
		SELECT CASE WHEN domain != '' THEN domain ELSE server_ip END AS name, 'out', MAX(server_id), MIN(server_ip),
			COUNT(DISTINCT server_ip), GROUP_CONCAT(DISTINCT client_id), GROUP_CONCAT(DISTINCT server_port || '/' || proto),
			SUM(bytes_up), SUM(bytes_down), SUM(conns), MIN(first_seen), MAX(last_seen) AS last
		FROM flows WHERE bucket >= ?1 AND ` + outWhere + ` GROUP BY name
		UNION ALL
		SELECT client_ip AS name, 'in', MAX(client_id), client_ip,
			1, GROUP_CONCAT(DISTINCT server_id), GROUP_CONCAT(DISTINCT server_port || '/' || proto),
			SUM(bytes_down), SUM(bytes_up), SUM(conns), MIN(first_seen), MAX(last_seen) AS last
		FROM flows WHERE bucket >= ?1 AND ` + inWhere + ` GROUP BY name
		ORDER BY last DESC LIMIT ` + fmt.Sprint(limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Peer{}
	for rows.Next() {
		var p Peer
		var machines, services string
		if err := rows.Scan(&p.Name, &p.Direction, &p.PeerID, &p.IP, &p.IPCount, &machines, &services,
			&p.Sent, &p.Received, &p.Conns, &p.FirstSeen, &p.LastSeen); err != nil {
			return nil, err
		}
		p.Local = p.PeerID != "" || IsLocal(p.IP)
		p.Machines = strings.Split(machines, ",")
		p.Services = strings.Split(services, ",")
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // before the next query: there is one connection
	return out, s.markNew(ctx, out, since, machine)
}
