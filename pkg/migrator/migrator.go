// Package migrator is a minimal, ordered schema migration runner for SQLite
// (or any database/sql driver that supports SQLite's PRAGMA user_version).
//
// All pending steps run inside a single write transaction, keyed on
// PRAGMA user_version: whichever process grabs the write lock first migrates;
// everyone else sees the final version and no-ops. For this to be safe under
// concurrency, connections should begin write transactions immediately
// (e.g. the ncruces driver's `_txlock=immediate` DSN parameter) so the
// transaction can't fail a read-to-write lock upgrade mid-migration.
package migrator

import (
	"context"
	"database/sql"
	"fmt"

	"sprout/pkg/xlog"
)

// Operation defines the actual database modification.
type Operation func(ctx context.Context, tx *sql.Tx) error

// Migration represents a single version step.
type Migration struct {
	Version int    // schema version after this step (PRAGMA user_version)
	Desc    string // Human readable description for logs
	Up      Operation
}

// Migrator manages the execution of migrations.
type Migrator struct {
	steps []Migration
}

// New creates a Migrator instance with an empty migration list.
func New() *Migrator {
	return &Migrator{
		steps: make([]Migration, 0),
	}
}

// Add registers a new migration step.
// Order matters! The first step registered takes a fresh database to schema
// version 1, the next to version 2, and so on.
func (m *Migrator) Add(desc string, op Operation) {
	m.steps = append(m.steps, Migration{
		Version: len(m.steps) + 1,
		Desc:    desc,
		Up:      op,
	})
}

// Version returns the schema version produced by all registered migrations.
func (m *Migrator) Version() int {
	return len(m.steps)
}

// Run executes all pending migrations based on the current PRAGMA user_version
// and updates it to the final version. It returns the schema version the
// database ends up at.
func (m *Migrator) Run(ctx context.Context, db *sql.DB, logger *xlog.Logger) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin migration transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	// 1. Determine where to start
	var current int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return 0, fmt.Errorf("failed to read user_version: %w", err)
	}
	if current > len(m.steps) {
		return current, fmt.Errorf("database schema version %d is newer than known migrations (%d); database state is unknown", current, len(m.steps))
	}
	if current == len(m.steps) {
		return current, nil // up-to-date
	}

	// 2. Apply pending migrations
	for _, step := range m.steps[current:] {
		logger.Infof("Applying migration %d: %s", step.Version, step.Desc)
		if err := step.Up(ctx, tx); err != nil {
			return current, fmt.Errorf("failed to apply migration %d (%s): %w", step.Version, step.Desc, err)
		}
	}

	// 3. Persist the new version (part of the same transaction; PRAGMA
	// doesn't support parameters, but the value is our own step count).
	final := len(m.steps)
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", final)); err != nil {
		return current, fmt.Errorf("failed to set user_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return current, fmt.Errorf("failed to commit migrations: %w", err)
	}
	return final, nil
}
