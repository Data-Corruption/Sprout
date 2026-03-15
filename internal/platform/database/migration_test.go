package database

import (
	"path/filepath"
	"sprout/internal/build"
	"sprout/internal/types"
	"testing"

	"github.com/Data-Corruption/lmdb-go/lmdb"
	"github.com/Data-Corruption/lmdb-go/wrap"
	"github.com/Data-Corruption/stdx/xlog"
)

func TestMigrate(t *testing.T) {
	// Setup temporary directory for DB and Logs
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "db")
	logPath := filepath.Join(tmpDir, "logs")

	// Initialize Logger
	logger, err := xlog.New(logPath, "debug")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	// Helper to open DB without running Migrate() automatically
	// We want to test Migrate() explicitly, but database.New() calls it.
	// So we'll use wrap.New() directly to get a raw DB, then call Migrate().
	openRawDB := func() *wrap.DB {
		db, _, err := wrap.New(dbPath, DBINameList())
		if err != nil {
			t.Fatalf("Failed to open raw DB: %v", err)
		}
		// Cache DBIs manually since we're bypassing database.New()
		dbis := db.GetDBis()
		for _, entry := range dbiRegistry {
			*entry.handle = dbis[entry.name]
		}
		return db
	}

	t.Run("Initial Schema", func(t *testing.T) {
		db := openRawDB()
		defer db.Close()

		// Run Migrate
		if err := Migrate(db, logger); err != nil {
			t.Fatalf("Migrate() failed: %v", err)
		}

		// Verify Config Exists
		var cfg types.Configuration
		err := db.View(func(txn *lmdb.Txn) error {
			return TxnGet(txn, *ConfigDBI, []byte(ConfigDataKey), &cfg, EncodingJSON)
		})
		if err != nil {
			t.Fatalf("Failed to read config: %v", err)
		}

		bi := build.Info()

		// Verify Default Values
		if cfg.Port != bi.ServiceDefaultPort {
			t.Errorf("Expected Port %d, got %d", bi.ServiceDefaultPort, cfg.Port)
		}
		if cfg.LogLevel != bi.DefaultLogLevel {
			t.Errorf("Expected LogLevel %s, got %s", bi.DefaultLogLevel, cfg.LogLevel)
		}

		// Verify Version
		var version string
		err = db.View(func(txn *lmdb.Txn) error {
			return TxnGet(txn, *ConfigDBI, []byte(ConfigVersionKey), &version, EncodingJSON)
		})
		if err != nil {
			t.Fatalf("Failed to read version: %v", err)
		}
		if version != "v1" {
			t.Errorf("Expected version v1, got %s", version)
		}
	})

	t.Run("Idempotency", func(t *testing.T) {
		db := openRawDB()
		defer db.Close()

		// Run Migrate again (should be no-op)
		if err := Migrate(db, logger); err != nil {
			t.Fatalf("Second Migrate() failed: %v", err)
		}

		// Verify Version is still v1
		var version string
		err = db.View(func(txn *lmdb.Txn) error {
			return TxnGet(txn, *ConfigDBI, []byte(ConfigVersionKey), &version, EncodingJSON)
		})
		if err != nil {
			t.Fatalf("Failed to read version: %v", err)
		}
		if version != "v1" {
			t.Errorf("Expected version v1, got %s", version)
		}
	})

	/*
		// Template for testing future migrations (e.g. v1 -> v2)
		t.Run("v1 to v2", func(t *testing.T) {
			// 1. Setup: Manually insert v1 data (or use a helper that sets up v1 state)
			// 2. Action: Run Migrate()
			// 3. Verify: Check that data is transformed to v2 format
		})
	*/
}
