package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sprout/internal/build"
	"sprout/internal/types"
	"testing"

	"sprout/pkg/xlog"
)

func TestMigrate(t *testing.T) {
	// Setup temporary directory for DB and Logs
	tmpDir := t.TempDir()
	dbDir := filepath.Join(tmpDir, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("Failed to create db dir: %v", err)
	}
	dbPath := filepath.Join(dbDir, FileName)
	logPath := filepath.Join(tmpDir, "logs")

	// Initialize Logger
	logger, err := xlog.New(logPath, "debug")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()
	buildInfo := build.BuildInfo{
		DefaultLogLevel:    "warn",
		ServiceDefaultPort: 8484,
	}

	// Helper to open the DB without applying a migration policy.
	// We want to test Migrate() explicitly.
	openRawDB := func() *sql.DB {
		db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(dbPath)+"?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(wal)")
		if err != nil {
			t.Fatalf("Failed to open raw DB: %v", err)
		}
		return db
	}

	readConfig := func(db *sql.DB) types.Configuration {
		var data string
		if err := db.QueryRow(`SELECT value FROM config WHERE key = ?`, ConfigDataKey).Scan(&data); err != nil {
			t.Fatalf("Failed to read config: %v", err)
		}
		var cfg types.Configuration
		if err := json.Unmarshal([]byte(data), &cfg); err != nil {
			t.Fatalf("Failed to unmarshal config: %v", err)
		}
		return cfg
	}

	readVersion := func(db *sql.DB) int {
		var version int
		if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
			t.Fatalf("Failed to read user_version: %v", err)
		}
		return version
	}

	const wantVersion = 1 // bump when adding migration steps

	t.Run("Initial Schema", func(t *testing.T) {
		db := openRawDB()
		defer db.Close()

		// Run Migrate
		if err := Migrate(db, logger, buildInfo); err != nil {
			t.Fatalf("Migrate() failed: %v", err)
		}

		// Verify Config Exists with Default Values
		cfg := readConfig(db)
		// --- BEGIN service.https ---
		if want := fmt.Sprintf("127.0.0.1:%d", buildInfo.ServiceDefaultPort); cfg.UIBind != want {
			t.Errorf("Expected UIBind %s, got %s", want, cfg.UIBind)
		}
		// --- END service.https ---
		if cfg.LogLevel != buildInfo.DefaultLogLevel {
			t.Errorf("Expected LogLevel %s, got %s", buildInfo.DefaultLogLevel, cfg.LogLevel)
		}

		// --- BEGIN update.notifications ---
		// Verify the periodic update-check lease table is part of schema v1.
		if _, err := db.Exec(`
			INSERT INTO update_check_lease (id, owner_token, expires_at)
			VALUES (1, 'owner', 1)
		`); err != nil {
			t.Errorf("update-check lease table not usable: %v", err)
		}
		// --- END update.notifications ---

		// --- BEGIN service.https ---
		// Verify sessions table exists and is usable.
		if _, err := db.Exec(
			`INSERT INTO sessions (token_hash, expiry, perms, username) VALUES ('t', 0, 0, 'admin')`,
		); err != nil {
			t.Errorf("sessions table not usable: %v", err)
		}
		// --- END service.https ---

		// --- BEGIN service ---
		// Verify the example service IPC table exists in the initial schema.
		if _, err := db.Exec(`
			INSERT INTO hash_requests (input, created_at, expires_at)
			VALUES ('hello', 0, 1)
		`); err != nil {
			t.Errorf("hash_requests table not usable: %v", err)
		}
		// --- END service ---

		// Verify Version
		if version := readVersion(db); version != wantVersion {
			t.Errorf("Expected version %d, got %d", wantVersion, version)
		}
	})

	t.Run("Idempotency", func(t *testing.T) {
		db := openRawDB()
		defer db.Close()

		// Run Migrate again (should be no-op)
		if err := Migrate(db, logger, buildInfo); err != nil {
			t.Fatalf("Second Migrate() failed: %v", err)
		}

		// Verify Version is unchanged
		if version := readVersion(db); version != wantVersion {
			t.Errorf("Expected version %d, got %d", wantVersion, version)
		}
	})

	t.Run("Unknown Newer Schema", func(t *testing.T) {
		db := openRawDB()
		defer db.Close()

		// A database from a newer app version must be rejected, not migrated.
		if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
			t.Fatalf("Failed to set user_version: %v", err)
		}
		if err := Migrate(db, logger, buildInfo); err == nil {
			t.Fatal("Expected Migrate() to fail on unknown newer schema version")
		}
		if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, wantVersion)); err != nil {
			t.Fatalf("Failed to restore user_version: %v", err)
		}
	})

	/*
		// Template for testing future migrations (e.g. 1 -> 2)
		t.Run("v1 to v2", func(t *testing.T) {
			// 1. Setup: Manually insert v1 data (or use a helper that sets up v1 state)
			// 2. Action: Run Migrate()
			// 3. Verify: Check that data is transformed to v2 format
		})
	*/
}

func TestNewMigrationPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	logger, err := xlog.New(filepath.Join(tmpDir, "logs"), "debug")
	if err != nil {
		t.Fatalf("initialize logger: %v", err)
	}
	defer logger.Close()

	buildInfo := build.BuildInfo{
		DefaultLogLevel:    "warn",
		ServiceDefaultPort: 8484,
	}

	t.Run("fresh database requires explicit migration", func(t *testing.T) {
		db, err := New(
			filepath.Join(tmpDir, "require-current"),
			logger,
			buildInfo,
			RequireCurrentSchema,
		)
		if err == nil {
			db.Close()
			t.Fatal("RequireCurrentSchema initialized a fresh database")
		}
	})

	t.Run("development policy initializes only a fresh schema", func(t *testing.T) {
		directory := filepath.Join(tmpDir, "initialize-fresh")
		db, err := New(directory, logger, buildInfo, InitializeFreshSchema)
		if err != nil {
			t.Fatalf("initialize fresh schema: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close initialized database: %v", err)
		}

		db, err = New(directory, logger, buildInfo, RequireCurrentSchema)
		if err != nil {
			t.Fatalf("open current schema without migration: %v", err)
		}
		defer db.Close()
	})
}
