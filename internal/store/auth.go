package store

import (
	"database/sql"
	"errors"
	"time"
)

// The dashboard password is kept as a salted hash under this settings key.
const passwordKey = "password"

// PasswordHash returns the stored password hash, or "" if no password has
// been set yet.
func (s *Store) PasswordHash() (string, error) {
	var hash string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, passwordKey).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return hash, err
}

// SetPasswordHash stores a new password hash and signs everyone out, so a
// changed password takes effect everywhere at once.
func (s *Store) SetPasswordHash(hash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, passwordKey, hash); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions`); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearPassword removes the password and signs everyone out. The next
// visit to the dashboard goes through first-time setup again.
func (s *Store) ClearPassword() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, passwordKey); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions`); err != nil {
		return err
	}
	return tx.Commit()
}

// AddSession records a signed-in browser. Only a hash of its token is
// stored, so reading the database does not let anyone sign in.
func (s *Store) AddSession(tokenHash string, expires time.Time) error {
	_, err := s.db.Exec(`INSERT INTO sessions (token_hash, expires) VALUES (?, ?)`, tokenHash, expires.Unix())
	return err
}

// SessionValid reports whether a session exists and has not expired.
func (s *Store) SessionValid(tokenHash string, now time.Time) (bool, error) {
	var expires int64
	err := s.db.QueryRow(`SELECT expires FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && expires > now.Unix(), err
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// PruneSessions removes expired sessions.
func (s *Store) PruneSessions(now time.Time) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires <= ?`, now.Unix())
	return err
}
