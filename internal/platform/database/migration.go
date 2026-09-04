package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sprout/internal/build"
	"sprout/internal/types"
	"sprout/pkg/migrator"

	"sprout/pkg/xlog"
)

// MigrationPolicy controls whether opening a database may change its schema.
type MigrationPolicy uint8

const (
	// RequireCurrentSchema rejects any schema version mismatch.
	RequireCurrentSchema MigrationPolicy = iota
	// ApplyPendingMigrations applies every pending migration.
	ApplyPendingMigrations
	// InitializeFreshSchema initializes version zero, but rejects upgrades of
	// an existing schema. It is intended only for isolated development data.
	InitializeFreshSchema
)

func newMigrator(buildInfo build.BuildInfo) *migrator.Migrator {
	m := migrator.New()

	// Add steps here. Order matters! The first step takes a fresh database to
	// schema version 1 (PRAGMA user_version), the next to 2, and so on.

	m.Add("Initial Schema", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE config (
				key   TEXT PRIMARY KEY,
				value TEXT NOT NULL
			) STRICT;
		`); err != nil {
			return fmt.Errorf("failed to create config table: %w", err)
		}

		// --- BEGIN update.notifications ---
		// A single renewable lease coordinates periodic update checks across
		// concurrent processes. Manual checks do not use this table.
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE update_check_lease (
				id          INTEGER PRIMARY KEY CHECK (id = 1),
				owner_token TEXT NOT NULL,
				expires_at  INTEGER NOT NULL -- unix milliseconds
			) STRICT;
		`); err != nil {
			return fmt.Errorf("failed to create update-check lease table: %w", err)
		}
		// --- END update.notifications ---

		// --- BEGIN service.https ---
		// UI sessions, keyed by SHA256 of the cookie token. Living in the DB
		// (not memory) makes revocation work across processes (CLI vs service)
		// and lets sessions survive restarts without any config handoff. Just
		// in general keeps things simple and predictable.
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE sessions (
				token_hash TEXT PRIMARY KEY,
				expiry     INTEGER NOT NULL, -- unix seconds
				perms      INTEGER NOT NULL, -- types.Perm bitmask (as int64)
				username   TEXT NOT NULL     -- credential username that minted it
			) STRICT;
		`); err != nil {
			return fmt.Errorf("failed to create sessions table: %w", err)
		}
		// --- END service.https ---

		// --- BEGIN service ---
		// Small SQLite IPC example used by `app hash` and the service worker.
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE hash_requests (
				id           INTEGER PRIMARY KEY,
				input        TEXT NOT NULL,
				result       TEXT,
				created_at   INTEGER NOT NULL, -- unix milliseconds
				expires_at   INTEGER NOT NULL, -- unix milliseconds
				completed_at INTEGER,          -- unix milliseconds
				CHECK (length(CAST(input AS BLOB)) BETWEEN 1 AND 4096)
			) STRICT;

			CREATE INDEX hash_requests_pending
			ON hash_requests (id)
			WHERE result IS NULL;
		`); err != nil {
			return fmt.Errorf("failed to create hash requests table: %w", err)
		}
		// --- END service ---

		// Store config with default values
		cfg := types.DefaultConfig(buildInfo)
		data, err := json.Marshal(&cfg)
		if err != nil {
			return fmt.Errorf("failed to marshal initial config: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO config (key, value) VALUES (?, ?)`,
			ConfigDataKey, string(data),
		); err != nil {
			return fmt.Errorf("failed to store initial config: %w", err)
		}

		return nil
	})

	/* Example version bump
	m.Add("Add jobs table", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE jobs (
				id         INTEGER PRIMARY KEY,
				name       TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ'))
			) STRICT;
		`)
		return err
	})
	*/

	return m
}

func Migrate(db *sql.DB, logger *xlog.Logger, buildInfo build.BuildInfo) error {
	return runMigrations(db, logger, newMigrator(buildInfo))
}

func prepareSchema(db *sql.DB, logger *xlog.Logger, buildInfo build.BuildInfo, policy MigrationPolicy) error {
	switch policy {
	case RequireCurrentSchema, ApplyPendingMigrations, InitializeFreshSchema:
	default:
		return fmt.Errorf("invalid migration policy %d", policy)
	}

	ctx := context.Background()
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("failed to read database schema version: %w", err)
	}

	m := newMigrator(buildInfo)
	required := m.Version()
	if current > required {
		return fmt.Errorf(
			"database schema version %d is newer than known migrations (%d); database state is unknown",
			current,
			required,
		)
	}
	if current == required {
		logger.Infof("Database schema at version %d", current)
		return nil
	}

	if policy != ApplyPendingMigrations && !(policy == InitializeFreshSchema && current == 0) {
		return fmt.Errorf(
			"database schema version %d is behind required version %d; explicit migration required",
			current,
			required,
		)
	}

	return runMigrations(db, logger, m)
}

func runMigrations(db *sql.DB, logger *xlog.Logger, m *migrator.Migrator) error {
	version, err := m.Run(context.Background(), db, logger)
	if err != nil {
		return err
	}
	logger.Infof("Database schema at version %d", version)
	return nil
}
