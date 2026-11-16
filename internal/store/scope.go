package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
)

// Scope limits what is shown to one side of the home network's edge.
const (
	ScopeAll      = "all"      // everything
	ScopeInternet = "internet" // hide traffic that stayed inside the home network
	ScopeLocal    = "local"    // hide traffic to and from the internet
)

// classifier decides whether an address is inside the home network.
type classifier struct {
	nets    []netip.Prefix
	private bool // private address ranges count as local
}

// current is shared by every Store because SQLite functions are registered
// per process, not per database.
var current atomic.Pointer[classifier]

var carrierNAT = netip.MustParsePrefix("100.64.0.0/10") // also used by Tailscale

func init() {
	current.Store(&classifier{private: true})
	// is_local lets queries filter on the current rules, so changing the
	// settings re-labels history as well as new traffic.
	err := sqlite.RegisterScalarFunction("is_local", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		ip, _ := args[0].(string)
		if IsLocal(ip) {
			return int64(1), nil
		}
		return int64(0), nil
	})
	if err != nil {
		panic(err)
	}
}

// SetLocal replaces the rules for what counts as inside the home network.
func SetLocal(nets []netip.Prefix, privateIsLocal bool) {
	current.Store(&classifier{nets: nets, private: privateIsLocal})
}

// IsLocal reports whether an address is inside the home network rather than
// out on the internet.
func IsLocal(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	if a.IsLoopback() || a.IsLinkLocalUnicast() {
		return true
	}
	c := current.Load()
	if c.private && (a.IsPrivate() || carrierNAT.Contains(a)) {
		return true
	}
	for _, n := range c.nets {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

// localExpr is true for a flow whose other end is inside the home network:
// either both ends are tracked machines, or the end that is not is local.
const localExpr = `((client_id != '' AND server_id != '') OR is_local(CASE WHEN client_id != '' THEN server_ip ELSE client_ip END))`

// scopeSQL returns a condition to append to a WHERE clause on flows.
func scopeSQL(scope string) string {
	switch scope {
	case ScopeInternet:
		return ` AND NOT ` + localExpr
	case ScopeLocal:
		return ` AND ` + localExpr
	}
	return ``
}

// Settings are the choices made on the dashboard's settings page.
type Settings struct {
	// LocalNets are networks to count as inside the home network, on top of
	// the ones detected from the host's own addresses.
	LocalNets []string `json:"localNets"`
	// PrivateIsLocal counts every private address (192.168.x.x, 10.x.x.x,
	// 172.16-31.x.x) as inside the home network.
	PrivateIsLocal bool `json:"privateIsLocal"`
	// Show is the scope the dashboard opens with.
	Show string `json:"show"`
	// RetentionDays is how many days of history are kept.
	RetentionDays int `json:"retentionDays"`
}

// Limits on RetentionDays.
const (
	MinRetentionDays     = 1
	MaxRetentionDays     = 90
	DefaultRetentionDays = 7
)

func DefaultSettings() Settings {
	return Settings{LocalNets: []string{}, PrivateIsLocal: true, Show: ScopeAll, RetentionDays: DefaultRetentionDays}
}

// Validate tidies the settings and returns LocalNets parsed. The error is
// written for the person filling in the settings page.
func (st *Settings) Validate() ([]netip.Prefix, error) {
	switch st.Show {
	case ScopeAll, ScopeInternet, ScopeLocal:
	default:
		return nil, fmt.Errorf("unknown choice %q for what to show", st.Show)
	}
	if st.RetentionDays < MinRetentionDays || st.RetentionDays > MaxRetentionDays {
		return nil, fmt.Errorf("history can be kept for %d to %d days", MinRetentionDays, MaxRetentionDays)
	}
	if len(st.LocalNets) > 64 {
		return nil, errors.New("too many networks; 64 is the limit")
	}
	nets := make([]netip.Prefix, 0, len(st.LocalNets))
	clean := make([]string, 0, len(st.LocalNets))
	seen := map[netip.Prefix]bool{}
	for _, raw := range st.LocalNets {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			// A bare address means just that one device.
			a, aerr := netip.ParseAddr(s)
			if aerr != nil {
				return nil, fmt.Errorf("%q is not a network; write it like 10.20.30.0/24", s)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("%q would count the whole internet as your network", s)
		}
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()).Masked()
		if !seen[p] {
			seen[p] = true
			nets = append(nets, p)
			clean = append(clean, p.String())
		}
	}
	st.LocalNets = clean
	return nets, nil
}

// LoadSettings returns the saved settings, or the defaults if none were saved.
func (s *Store) LoadSettings() (Settings, error) {
	st := DefaultSettings()
	var raw string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'settings'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return DefaultSettings(), nil // unreadable settings fall back to the defaults
	}
	if _, err := st.Validate(); err != nil {
		return DefaultSettings(), nil
	}
	return st, nil
}

func (s *Store) SaveSettings(st Settings) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings (key, value) VALUES ('settings', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, string(raw))
	return err
}

// Share is one side of the internet/home-network divide.
type Share struct {
	Bytes  int64 `json:"bytes"`
	Places int64 `json:"places"`
}

// Split says how the traffic divides between the internet and the home network.
type Split struct {
	Internet Share `json:"internet"`
	Local    Share `json:"local"`
}

// Split is never limited by scope: it is what explains the scopes.
func (s *Store) Split(ctx context.Context, since time.Time, machine string) (Split, error) {
	where, args := `bucket >= ?`, []any{since.Unix()}
	if machine != "" {
		where += ` AND (client_id = ? OR server_id = ?)`
		args = append(args, machine, machine)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT local, SUM(total), COUNT(DISTINCT place) FROM (
			SELECT `+localExpr+` AS local, bytes_up + bytes_down AS total,
				CASE WHEN client_id = '' THEN client_ip WHEN domain != '' THEN domain ELSE server_ip END AS place
			FROM flows WHERE `+where+`
		) GROUP BY local`, args...)
	var out Split
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var local bool
		var sh Share
		if err := rows.Scan(&local, &sh.Bytes, &sh.Places); err != nil {
			return out, err
		}
		if local {
			out.Local = sh
		} else {
			out.Internet = sh
		}
	}
	return out, rows.Err()
}
