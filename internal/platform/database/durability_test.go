package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"sprout/internal/build"
	"sprout/internal/types"
	"sprout/pkg/xlog"
)

const concurrentDatabaseHelperEnv = "SPROUT_DATABASE_INIT_HELPER"

func TestNewCreatesPrivateDatabaseDirectory(t *testing.T) {
	root := t.TempDir()
	databaseDir := filepath.Join(root, "db")
	logger, err := xlog.New(filepath.Join(root, "logs"), "debug")
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	defer logger.Close()

	db, err := New(databaseDir, logger, databaseTestBuildInfo(), ApplyPendingMigrations)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	defer db.Close()

	info, err := os.Stat(databaseDir)
	if err != nil {
		t.Fatalf("stat database directory: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("database directory mode = %04o, want 0700", got)
		}
	}
	if got := db.Stats().MaxOpenConnections; got != 4 {
		t.Fatalf("maximum open connections = %d, want 4", got)
	}
}

func TestConcurrentDatabaseInitialization(t *testing.T) {
	const processCount = 8

	root := t.TempDir()
	databaseDir := filepath.Join(root, "db")
	readyDir := filepath.Join(root, "ready")
	barrier := filepath.Join(root, "start")
	if err := os.Mkdir(readyDir, 0o700); err != nil {
		t.Fatalf("create ready directory: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	type child struct {
		cmd    *exec.Cmd
		output bytes.Buffer
	}
	children := make([]child, processCount)
	for i := range children {
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestConcurrentDatabaseInitializationHelper$")
		cmd.Env = append(os.Environ(),
			concurrentDatabaseHelperEnv+"=1",
			"SPROUT_DATABASE_INIT_DIR="+databaseDir,
			"SPROUT_DATABASE_INIT_READY_DIR="+readyDir,
			"SPROUT_DATABASE_INIT_BARRIER="+barrier,
			"SPROUT_DATABASE_INIT_CHILD="+strconv.Itoa(i),
		)
		cmd.Stdout = &children[i].output
		cmd.Stderr = &children[i].output
		children[i].cmd = cmd
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatalf("start child %d: %v", i, err)
		}
	}

	for i := range children {
		if err := waitForDatabaseBarrier(ctx, filepath.Join(readyDir, strconv.Itoa(i))); err != nil {
			cancel()
			t.Fatalf("wait for child %d readiness: %v", i, err)
		}
	}
	if err := os.WriteFile(barrier, nil, 0o600); err != nil {
		cancel()
		t.Fatalf("release child barrier: %v", err)
	}
	for i := range children {
		if err := children[i].cmd.Wait(); err != nil {
			t.Errorf("child %d failed: %v\n%s", i, err, children[i].output.String())
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("concurrent database initialization timed out: %v", err)
	}

	logger, err := xlog.New(filepath.Join(root, "verify-logs"), "debug")
	if err != nil {
		t.Fatalf("create verification logger: %v", err)
	}
	defer logger.Close()
	db, err := New(databaseDir, logger, databaseTestBuildInfo(), RequireCurrentSchema)
	if err != nil {
		t.Fatalf("open final database: %v", err)
	}
	defer db.Close()

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer verifyCancel()
	var integrity string
	if err := db.QueryRowContext(verifyCtx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("check final database integrity: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("final database integrity = %q, want ok", integrity)
	}
	var journalMode string
	if err := db.QueryRowContext(verifyCtx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read final journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("final journal mode = %q, want wal", journalMode)
	}
	var version int
	if err := db.QueryRowContext(verifyCtx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read final schema version: %v", err)
	}
	if version != 1 {
		t.Fatalf("final schema version = %d, want 1", version)
	}
	var configRows int
	if err := db.QueryRowContext(
		verifyCtx,
		`SELECT COUNT(*) FROM config WHERE key = ?`,
		ConfigDataKey,
	).Scan(&configRows); err != nil {
		t.Fatalf("count final config rows: %v", err)
	}
	if configRows != 1 {
		t.Fatalf("final config row count = %d, want 1", configRows)
	}
	var data string
	if err := db.QueryRowContext(
		verifyCtx,
		`SELECT value FROM config WHERE key = ?`,
		ConfigDataKey,
	).Scan(&data); err != nil {
		t.Fatalf("read final config: %v", err)
	}
	var cfg types.Configuration
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("decode final config: %v", err)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("final log level = %q, want warn", cfg.LogLevel)
	}
}

func TestConcurrentDatabaseInitializationHelper(t *testing.T) {
	if os.Getenv(concurrentDatabaseHelperEnv) != "1" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := os.Getenv("SPROUT_DATABASE_INIT_CHILD")
	readyPath := filepath.Join(os.Getenv("SPROUT_DATABASE_INIT_READY_DIR"), child)
	if err := os.WriteFile(readyPath, nil, 0o600); err != nil {
		t.Fatalf("signal readiness: %v", err)
	}
	if err := waitForDatabaseBarrier(ctx, os.Getenv("SPROUT_DATABASE_INIT_BARRIER")); err != nil {
		t.Fatal(err)
	}
	logger, err := xlog.New(
		filepath.Join(filepath.Dir(os.Getenv("SPROUT_DATABASE_INIT_DIR")), "child-logs-"+child),
		"debug",
	)
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	defer logger.Close()
	db, err := New(
		os.Getenv("SPROUT_DATABASE_INIT_DIR"),
		logger,
		databaseTestBuildInfo(),
		ApplyPendingMigrations,
	)
	if err != nil {
		t.Fatalf("initialize database: %v", err)
	}
	defer db.Close()

	var data string
	if err := db.QueryRowContext(
		ctx,
		`SELECT value FROM config WHERE key = ?`,
		ConfigDataKey,
	).Scan(&data); err != nil {
		t.Fatalf("read initialized config: %v", err)
	}
	var cfg types.Configuration
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("decode initialized config: %v", err)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("initialized log level = %q, want warn", cfg.LogLevel)
	}
}

func waitForDatabaseBarrier(ctx context.Context, path string) error {
	if path == "" {
		return fmt.Errorf("database initialization barrier path is empty")
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat database initialization barrier: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for database initialization barrier: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func databaseTestBuildInfo() build.BuildInfo {
	return build.BuildInfo{
		Name:               "sprout-database-test",
		Version:            "v0.0.0-test",
		DefaultLogLevel:    "warn",
		ServiceDefaultPort: 8484,
	}
}
