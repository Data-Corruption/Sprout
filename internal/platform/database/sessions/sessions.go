// --- FILE service.https ---

// Package sessions stores UI login sessions in the SQLite sessions table,
// keyed by SHA256 of the cookie token. Keeping them in the DB (instead of a
// process-local map) makes revocation work across processes - the CLI can
// delete rows the running service sees immediately - and lets sessions
// survive restarts without any explicit handoff.
//
// Every authenticated request costs one SELECT. That is nothing at dashboard
// load; if this app ever grows real traffic, put a small in-memory read cache
// (token hash -> Session, short TTL) in front of Get before reaching for
// anything fancier.
//
// Expiry is fixed at creation. There is no renewal path on purpose: the
// middleware sets the cookie MaxAge to the same duration, so a session ends at
// a known time regardless of activity.
package sessions

import (
	"database/sql"
	"errors"
	"fmt"
	"sprout/internal/types"
	"time"
)

// Session is one UI login session. The token itself is never stored, only
// its SHA256 (the table key).
type Session struct {
	Expiry time.Time
	Perms  types.Perm
	// Username of the credential that minted this session; used to revoke all
	// sessions of a removed credential.
	Username string
}

// Create inserts a new session under the given token hash.
func Create(db *sql.DB, tokenHash string, s Session) error {
	// Perm is uint64; store the raw bits as int64 since SQLite/database-sql
	// reject uint64 values with the high bit set (e.g. PermAdmin).
	_, err := db.Exec(
		`INSERT INTO sessions (token_hash, expiry, perms, username) VALUES (?, ?, ?, ?)`,
		tokenHash, s.Expiry.Unix(), int64(s.Perms), s.Username,
	)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// Get returns the session for the given token hash, or nil if there is none
// or it has expired.
func Get(db *sql.DB, tokenHash string) (*Session, error) {
	var expiry, perms int64
	var username string
	err := db.QueryRow(
		`SELECT expiry, perms, username FROM sessions WHERE token_hash = ? AND expiry > ?`,
		tokenHash, time.Now().Unix(),
	).Scan(&expiry, &perms, &username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read session: %w", err)
	}
	return &Session{
		Expiry:   time.Unix(expiry, 0),
		Perms:    types.Perm(uint64(perms)),
		Username: username,
	}, nil
}

// Delete removes the session with the given token hash, reporting whether a
// row was deleted.
func Delete(db *sql.DB, tokenHash string) (bool, error) {
	res, err := db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	if err != nil {
		return false, fmt.Errorf("failed to delete session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to delete session: %w", err)
	}
	return n > 0, nil
}

// DeleteByUsername revokes every session minted by the given credential username,
// returning the number of sessions removed. Call this wherever a credential
// is removed so its live sessions end immediately instead of at expiry.
func DeleteByUsername(db *sql.DB, username string) (int64, error) {
	return deleteByUsername(db, username)
}

// DeleteByUsernameTx revokes every session for username within an existing
// transaction. The caller owns the transaction and must commit or roll it back.
func DeleteByUsernameTx(tx *sql.Tx, username string) (int64, error) {
	if tx == nil {
		return 0, fmt.Errorf("failed to delete sessions by username: transaction is nil")
	}
	return deleteByUsername(tx, username)
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func deleteByUsername(db execer, username string) (int64, error) {
	res, err := db.Exec(`DELETE FROM sessions WHERE username = ?`, username)
	if err != nil {
		return 0, fmt.Errorf("failed to delete sessions by username: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to delete sessions by username: %w", err)
	}
	return n, nil
}

// DeleteExpired prunes expired rows. Called opportunistically on login.
func DeleteExpired(db *sql.DB) error {
	if _, err := db.Exec(
		`DELETE FROM sessions WHERE expiry <= ?`, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("failed to prune expired sessions: %w", err)
	}
	return nil
}
