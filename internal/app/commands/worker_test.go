// --- FILE service ---

package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"sprout/internal/app"
	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/internal/platform/database/hashrequests"
	"sprout/pkg/xlog"
)

func TestRunWorkerHashesRequest(t *testing.T) {
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
	a := &app.App{DB: db, Log: logger}

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runWorker(ctx, a, func() { close(ready) }) }()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("worker did not announce readiness")
	}

	id, err := hashrequests.Create(ctx, db, "hello", time.Now().Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
	result, err := waitForHash(waitCtx, a, id)
	waitCancel()
	if err != nil {
		t.Fatalf("waitForHash: %v", err)
	}
	sum := sha256.Sum256([]byte("hello"))
	if want := hex.EncodeToString(sum[:]); result != want {
		t.Fatalf("hash = %q, want %q", result, want)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWorker stopped with error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runWorker did not stop after cancellation")
	}
}
