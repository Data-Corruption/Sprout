// --- FILE service ---

// Package hashrequests implements the deliberately small SQLite IPC example
// shared by the hash CLI command and service worker.
package hashrequests

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	MaxTextBytes = 4096
	MaxPending   = 128
)

var (
	ErrQueueFull   = errors.New("service request queue is full")
	ErrRequestGone = errors.New("service request expired or was removed")
)

type Request struct {
	ID    int64
	Input string
}

func Create(ctx context.Context, db *sql.DB, input string, expires time.Time) (int64, error) {
	if len(input) == 0 {
		return 0, fmt.Errorf("text cannot be empty")
	}
	if len(input) > MaxTextBytes {
		return 0, fmt.Errorf("text is %d bytes; maximum is %d", len(input), MaxTextBytes)
	}
	now := time.Now()
	if !expires.After(now) {
		return 0, fmt.Errorf("request expiry must be in the future")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin hash request: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM hash_requests WHERE expires_at <= ?`, now.UnixMilli(),
	); err != nil {
		return 0, fmt.Errorf("clean expired hash requests: %w", err)
	}

	var pending int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM hash_requests WHERE result IS NULL AND expires_at > ?`,
		now.UnixMilli(),
	).Scan(&pending); err != nil {
		return 0, fmt.Errorf("count pending hash requests: %w", err)
	}
	if pending >= MaxPending {
		return 0, ErrQueueFull
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO hash_requests (input, created_at, expires_at)
		VALUES (?, ?, ?)
	`, input, now.UnixMilli(), expires.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("insert hash request: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read hash request ID: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit hash request: %w", err)
	}
	return id, nil
}

func NextPending(ctx context.Context, db *sql.DB) (*Request, error) {
	var request Request
	err := db.QueryRowContext(ctx, `
		SELECT id, input
		FROM hash_requests
		WHERE result IS NULL AND expires_at > ?
		ORDER BY id
		LIMIT 1
	`, time.Now().UnixMilli()).Scan(&request.ID, &request.Input)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pending hash request: %w", err)
	}
	return &request, nil
}

func Complete(ctx context.Context, db *sql.DB, id int64, result string) error {
	now := time.Now().UnixMilli()
	if _, err := db.ExecContext(ctx, `
		UPDATE hash_requests
		SET result = ?, completed_at = ?
		WHERE id = ? AND result IS NULL AND expires_at > ?
	`, result, now, id, now); err != nil {
		return fmt.Errorf("complete hash request: %w", err)
	}
	return nil
}

func Result(ctx context.Context, db *sql.DB, id int64) (string, bool, error) {
	var result sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT result FROM hash_requests WHERE id = ? AND expires_at > ?`,
		id, time.Now().UnixMilli(),
	).Scan(&result)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrRequestGone
	}
	if err != nil {
		return "", false, fmt.Errorf("read hash request result: %w", err)
	}
	return result.String, result.Valid, nil
}

func Delete(ctx context.Context, db *sql.DB, id int64) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM hash_requests WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete hash request: %w", err)
	}
	return nil
}

func Cleanup(ctx context.Context, db *sql.DB, completedBefore time.Time) error {
	if _, err := db.ExecContext(ctx, `
		DELETE FROM hash_requests
		WHERE expires_at <= ?
		   OR (completed_at IS NOT NULL AND completed_at <= ?)
	`, time.Now().UnixMilli(), completedBefore.UnixMilli()); err != nil {
		return fmt.Errorf("clean hash requests: %w", err)
	}
	return nil
}
