// --- FILE service ---

package hashrequests

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/pkg/xlog"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	tmp := t.TempDir()
	logger, err := xlog.New(filepath.Join(tmp, "logs"), "error")
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.New(filepath.Join(tmp, "db"), logger, build.BuildInfo{
		DefaultLogLevel:    "warn",
		ServiceDefaultPort: 8484,
	}, database.ApplyPendingMigrations)
	if err != nil {
		logger.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		logger.Close()
	})
	return db
}

func TestRequestRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	id, err := Create(ctx, db, "hello", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	request, err := NextPending(ctx, db)
	if err != nil {
		t.Fatalf("NextPending: %v", err)
	}
	if request == nil || request.ID != id || request.Input != "hello" {
		t.Fatalf("request = %+v, want id=%d input=hello", request, id)
	}
	if err := Complete(ctx, db, id, "result"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	result, ready, err := Result(ctx, db, id)
	if err != nil || !ready || result != "result" {
		t.Fatalf("Result = %q, %t, %v", result, ready, err)
	}
	if err := Delete(ctx, db, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := Result(ctx, db, id); !errors.Is(err, ErrRequestGone) {
		t.Fatalf("result after delete = %v, want ErrRequestGone", err)
	}
}

func TestCreateBoundsInputAndQueue(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	for _, input := range []string{"", strings.Repeat("x", MaxTextBytes+1)} {
		if _, err := Create(ctx, db, input, time.Now().Add(time.Minute)); err == nil {
			t.Fatalf("Create(%d bytes) succeeded", len(input))
		}
	}

	now := time.Now()
	for i := 0; i < MaxPending; i++ {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO hash_requests (input, created_at, expires_at)
			VALUES ('x', ?, ?)
		`, now.UnixMilli(), now.Add(time.Minute).UnixMilli()); err != nil {
			t.Fatalf("fill queue %d: %v", i, err)
		}
	}
	if _, err := Create(ctx, db, "overflow", time.Now().Add(time.Minute)); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue error = %v, want ErrQueueFull", err)
	}
}

func TestCleanupRemovesExpiredAndOldCompleted(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO hash_requests (input, result, created_at, expires_at, completed_at)
		VALUES
			('expired', NULL, ?, ?, NULL),
			('old complete', 'x', ?, ?, ?),
			('current', NULL, ?, ?, NULL)
	`, now.Add(-time.Minute).UnixMilli(), now.Add(-time.Second).UnixMilli(),
		now.Add(-time.Minute).UnixMilli(), now.Add(time.Minute).UnixMilli(), now.Add(-time.Minute).UnixMilli(),
		now.UnixMilli(), now.Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	if err := Cleanup(ctx, db, now.Add(-30*time.Second)); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM hash_requests`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("remaining requests = %d, want 1", count)
	}
}
