// --- FILE update ---

// Package updatelease coordinates periodic update checks across processes.
package updatelease

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	leaseID    = 1
	tokenBytes = 32
)

// Lease identifies ownership of the single periodic update-check slot.
type Lease struct {
	OwnerToken string
	ExpiresAt  time.Time
}

// Claim attempts to own the periodic update-check slot until now+duration.
// The database DSN makes BeginTx an immediate transaction, so contenders
// serialize without keeping a transaction open during the network request.
func Claim(ctx context.Context, db *sql.DB, now time.Time, duration time.Duration) (Lease, bool, error) {
	if db == nil {
		return Lease{}, false, fmt.Errorf("claim update-check lease: database is nil")
	}
	if duration <= 0 {
		return Lease{}, false, fmt.Errorf("claim update-check lease: duration must be positive")
	}

	token := make([]byte, tokenBytes)
	if _, err := rand.Read(token); err != nil {
		return Lease{}, false, fmt.Errorf("generate update-check lease owner: %w", err)
	}
	lease := Lease{
		OwnerToken: hex.EncodeToString(token),
		ExpiresAt:  now.Add(duration),
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Lease{}, false, fmt.Errorf("begin update-check lease claim: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	result, err := tx.ExecContext(ctx, `
		INSERT INTO update_check_lease (id, owner_token, expires_at)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			owner_token = excluded.owner_token,
			expires_at = excluded.expires_at
		WHERE update_check_lease.expires_at <= ?
	`, leaseID, lease.OwnerToken, lease.ExpiresAt.UnixMilli(), now.UnixMilli())
	if err != nil {
		return Lease{}, false, fmt.Errorf("claim update-check lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Lease{}, false, fmt.Errorf("inspect update-check lease claim: %w", err)
	}
	if affected != 0 && affected != 1 {
		return Lease{}, false, fmt.Errorf("claim update-check lease affected %d rows", affected)
	}
	if err := tx.Commit(); err != nil {
		return Lease{}, false, fmt.Errorf("commit update-check lease claim: %w", err)
	}
	if affected == 0 {
		return Lease{}, false, nil
	}
	return lease, true, nil
}

// Complete runs persist in a short transaction after verifying that lease is
// still owned and unexpired. It removes the lease in the same commit. A false
// result means ownership was lost and persist was not called.
func Complete(
	ctx context.Context,
	db *sql.DB,
	lease Lease,
	now time.Time,
	persist func(*sql.Tx) error,
) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("complete update-check lease: database is nil")
	}
	if lease.OwnerToken == "" {
		return false, fmt.Errorf("complete update-check lease: owner token is empty")
	}
	if persist == nil {
		return false, fmt.Errorf("complete update-check lease: persist function is nil")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin update-check lease completion: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	var owned int
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM update_check_lease
			WHERE id = ? AND owner_token = ? AND expires_at > ?
		)
	`, leaseID, lease.OwnerToken, now.UnixMilli()).Scan(&owned); err != nil {
		return false, fmt.Errorf("verify update-check lease owner: %w", err)
	}
	if owned == 0 {
		return false, nil
	}
	if err := persist(tx); err != nil {
		return false, fmt.Errorf("persist update-check result: %w", err)
	}

	result, err := tx.ExecContext(ctx,
		`DELETE FROM update_check_lease WHERE id = ? AND owner_token = ?`,
		leaseID,
		lease.OwnerToken,
	)
	if err != nil {
		return false, fmt.Errorf("remove completed update-check lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect completed update-check lease removal: %w", err)
	}
	if affected != 1 {
		return false, fmt.Errorf("completed update-check lease removal affected %d rows", affected)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit update-check lease completion: %w", err)
	}
	return true, nil
}

// Release removes an owned lease when a post-claim eligibility check shows
// that no network request is needed.
func Release(ctx context.Context, db *sql.DB, lease Lease) error {
	if db == nil {
		return fmt.Errorf("release update-check lease: database is nil")
	}
	if lease.OwnerToken == "" {
		return fmt.Errorf("release update-check lease: owner token is empty")
	}
	if _, err := db.ExecContext(ctx,
		`DELETE FROM update_check_lease WHERE id = ? AND owner_token = ?`,
		leaseID,
		lease.OwnerToken,
	); err != nil {
		return fmt.Errorf("release update-check lease: %w", err)
	}
	return nil
}
