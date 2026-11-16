package store

import "time"

// Traffic is recorded per minute, which is what makes the database grow.
// Once it is old enough that nobody will look at it minute by minute, each
// hour's rows are folded into one. Totals are unchanged; only the
// minute-level timing of old traffic is lost.

// Rollup folds per-minute history older than the cutoff into hourly rows.
// It is safe to run repeatedly.
func (s *Store) Rollup(before time.Time) error {
	// Whole hours only, so no hour is ever half folded.
	cutoff := before.Unix() / 3600 * 3600
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Every minute of an hour except its first is added onto a row at the
	// start of the hour, which is either that first minute's own row or a
	// new one.
	if _, err := tx.Exec(`
		INSERT INTO flows (bucket, client_ip, server_ip, server_port, proto, domain, client_id, server_id,
			bytes_up, bytes_down, packets, conns, first_seen, last_seen)
		SELECT bucket / 3600 * 3600, client_ip, server_ip, server_port, proto, domain, MAX(client_id), MAX(server_id),
			SUM(bytes_up), SUM(bytes_down), SUM(packets), SUM(conns), MIN(first_seen), MAX(last_seen)
		FROM flows WHERE bucket < ?1 AND bucket % 3600 != 0
		GROUP BY bucket / 3600, client_ip, server_ip, server_port, proto, domain
		ON CONFLICT (bucket, client_ip, server_ip, server_port, proto, domain) DO UPDATE SET
			bytes_up   = bytes_up + excluded.bytes_up,
			bytes_down = bytes_down + excluded.bytes_down,
			packets    = packets + excluded.packets,
			conns      = conns + excluded.conns,
			first_seen = min(first_seen, excluded.first_seen),
			last_seen  = max(last_seen, excluded.last_seen)`, cutoff); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM flows WHERE bucket < ?1 AND bucket % 3600 != 0`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

// Tidy applies the retention settings: it folds history past the detail
// period into hourly rows and deletes history past the retention period.
func (s *Store) Tidy(now time.Time, settings Settings) error {
	if err := s.Rollup(now.AddDate(0, 0, -settings.DetailDays)); err != nil {
		return err
	}
	return s.Prune(now.AddDate(0, 0, -settings.RetentionDays))
}
