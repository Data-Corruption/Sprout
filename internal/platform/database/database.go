// Package database provides the embedded SQLite database for the application.
//
// The driver is github.com/ncruces/go-sqlite3: SQLite compiled to Wasm and
// translated to pure Go (wasm2go), so there is no cgo and cross-compiling
// stays a plain GOOS/GOARCH matter. v0.35.3 is a hard minimum: earlier versions
// corrupt data under concurrent WAL access on Windows (upstream issue 404). See
// docs/content/docs/architecture.md before changing the version.
//
// The exposed API is plain database/sql, which keeps the driver swappable
// (e.g. for mattn/go-sqlite3 if you'd rather take the cgo toolchain cost);
// only the DSN below is driver-specific.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sprout/internal/build"
	"sprout/pkg/xlog"

	"github.com/ncruces/go-sqlite3"
	_ "github.com/ncruces/go-sqlite3/driver" // registers the "sqlite3" database/sql driver
)

// ConfigDataKey is the key of the marshaled config struct in the config table.
const ConfigDataKey = "data"

/* Schema layout:

config
    key TEXT PRIMARY KEY -> value TEXT
    "data" -> marshaled Configuration struct (JSON)
sessions
    token_hash TEXT PRIMARY KEY (SHA256 of the cookie token)
    expiry INTEGER (unix seconds), perms INTEGER, username TEXT
    (see internal/platform/database/sessions)
Schema version
    PRAGMA user_version (managed by pkg/migrator)

Add your own tables in migration.go.

*/

// FileName is the SQLite database file name inside the db directory.
// SQLite keeps "-wal" and "-shm" siblings beside it while the DB is open.
const FileName = "app.db"

// New opens (creating if needed) the SQLite database in the given directory
// and enforces the requested migration policy.
//
// Connection behavior, applied per pooled connection via the DSN:
//   - _txlock=immediate: write transactions take the write lock at BEGIN,
//     avoiding read-to-write upgrade failures under cross-process concurrency.
//   - busy_timeout(10000): writers wait up to 10s for a competing process
//     instead of failing with SQLITE_BUSY.
//   - journal_mode(wal): negotiated once below before migration because
//     setting it as a per-connection pragma races on concurrent first start.
//   - synchronous(normal): the standard WAL durability tradeoff (atomicity is
//     preserved on crash; at most the last transactions before an OS crash
//     may roll back).
//   - foreign_keys(on): enforce FK constraints (off by default in SQLite).
func New(
	directory string,
	logger *xlog.Logger,
	buildInfo build.BuildInfo,
	migrationPolicy MigrationPolicy,
) (*sql.DB, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create database dir: %w", err)
	}
	path := filepath.Join(directory, FileName)

	dsn := "file:" + filepath.ToSlash(path) +
		"?_txlock=immediate" +
		"&_pragma=busy_timeout(10000)" +
		"&_pragma=synchronous(normal)" +
		"&_pragma=foreign_keys(on)"

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	// Each connection carries its own sandboxed Wasm memory, so keep the pool
	// modest. SQLite serializes writers anyway; readers scale with the pool.
	db.SetMaxOpenConns(4)

	if err := enableWAL(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable WAL journal mode: %w", err)
	}

	if err := prepareSchema(db, logger, buildInfo, migrationPolicy); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to prepare database schema: %w", err)
	}
	logger.Infof("SQLite initialized at %s", path)

	return db, nil
}

// enableWAL negotiates the persistent journal mode before migration. On a
// fresh database, concurrent processes can briefly contend while one changes
// the mode or starts its migration. Retrying only SQLite lock errors keeps
// first start deterministic without hiding malformed DSNs or I/O failures.
func enableWAL(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for {
		var mode string
		err := db.QueryRowContext(ctx, `PRAGMA journal_mode = wal`).Scan(&mode)
		if err == nil {
			if strings.EqualFold(mode, "wal") {
				return nil
			}
			return fmt.Errorf("SQLite selected journal mode %q, want wal", mode)
		}
		if !errors.Is(err, sqlite3.BUSY) && !errors.Is(err, sqlite3.LOCKED) {
			return err
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("timed out waiting to set WAL journal mode: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
