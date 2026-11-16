package store

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"
	"unicode"
)

// Devices are things on the network that are not VMs or containers: a
// laptop, a phone, a television. Each can have a name its owner chose and
// a name it announced itself; the owner's always wins.

const devicesSchema = `
CREATE TABLE IF NOT EXISTS devices (
	ip           TEXT PRIMARY KEY,
	name         TEXT NOT NULL, -- chosen by the owner, or ''
	announced    TEXT NOT NULL, -- what the device calls itself, or ''
	announced_at INTEGER NOT NULL
) WITHOUT ROWID;
`

// announcedLife is how long an announced name is kept without being heard
// again. Addresses get handed to other devices over time.
const announcedLife = 30 * 24 * time.Hour

// MaxDeviceName is the longest name an owner can give a device.
const MaxDeviceName = 40

type Device struct {
	IP        string `json:"ip"`
	Name      string `json:"name"`
	Announced string `json:"announced"`
}

// Devices lists every address that has a name of either kind.
func (s *Store) Devices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ip, name, announced FROM devices ORDER BY ip`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.IP, &d.Name, &d.Announced); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CleanDeviceName tidies a name typed by the owner. The error is written
// for the person typing.
func CleanDeviceName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if len([]rune(name)) > MaxDeviceName {
		return "", errors.New("that name is too long; keep it to 40 characters")
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return "", errors.New("that name has a character that cannot be shown")
		}
	}
	return name, nil
}

// NameDevice sets the owner's name for an address; an empty name removes it.
func (s *Store) NameDevice(ip, name string) error {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return errors.New("that is not an address")
	}
	ip = addr.Unmap().String()
	if _, err := s.db.Exec(`INSERT INTO devices (ip, name, announced, announced_at) VALUES (?, ?, '', 0)
		ON CONFLICT (ip) DO UPDATE SET name = excluded.name`, ip, name); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM devices WHERE name = '' AND announced = ''`)
	return err
}

// SaveAnnounced records the names devices gave for their addresses.
func (s *Store) SaveAnnounced(names map[string]string, now time.Time) error {
	if len(names) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for ip, name := range names {
		if _, err := tx.Exec(`INSERT INTO devices (ip, name, announced, announced_at) VALUES (?, '', ?, ?)
			ON CONFLICT (ip) DO UPDATE SET announced = excluded.announced, announced_at = excluded.announced_at`,
			ip, name, now.UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// pruneDevices forgets announced names that have not been heard for a long
// time. Names the owner chose are kept.
func (s *Store) pruneDevices(now time.Time) error {
	if _, err := s.db.Exec(`UPDATE devices SET announced = '' WHERE announced != '' AND announced_at < ?`,
		now.Add(-announcedLife).UnixMilli()); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM devices WHERE name = '' AND announced = ''`)
	return err
}
