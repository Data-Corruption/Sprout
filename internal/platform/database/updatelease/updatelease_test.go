// --- FILE update ---

package updatelease_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/internal/platform/database/updatelease"
	"sprout/pkg/xlog"
)

func newLeaseTestDB(t *testing.T) *sql.DB {
	t.Helper()
	tmp := t.TempDir()
	logger, err := xlog.New(filepath.Join(tmp, "logs"), "error")
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.New(
		filepath.Join(tmp, "db"),
		logger,
		build.BuildInfo{DefaultLogLevel: "warn", ServiceDefaultPort: 8484},
		database.ApplyPendingMigrations,
	)
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

func TestClaimContentionHasOneWinner(t *testing.T) {
	db := newLeaseTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	start := make(chan struct{})
	results := make(chan bool, 16)
	errorsCh := make(chan error, 16)

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, claimed, err := updatelease.Claim(context.Background(), db, now, time.Minute)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- claimed
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errorsCh)

	for err := range errorsCh {
		t.Errorf("claim failed: %v", err)
	}
	winners := 0
	for claimed := range results {
		if claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners = %d, want 1", winners)
	}
}

func TestClaimReplacesExpiredLease(t *testing.T) {
	db := newLeaseTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)

	first, claimed, err := updatelease.Claim(ctx, db, now, 10*time.Second)
	if err != nil || !claimed {
		t.Fatalf("first claim: claimed=%t err=%v", claimed, err)
	}
	if _, claimed, err := updatelease.Claim(ctx, db, now.Add(9*time.Second), time.Minute); err != nil {
		t.Fatal(err)
	} else if claimed {
		t.Fatal("unexpired lease was replaced")
	}
	second, claimed, err := updatelease.Claim(ctx, db, now.Add(10*time.Second), time.Minute)
	if err != nil || !claimed {
		t.Fatalf("expired lease replacement: claimed=%t err=%v", claimed, err)
	}
	if second.OwnerToken == first.OwnerToken {
		t.Fatal("replacement reused owner token")
	}

	called := false
	completed, err := updatelease.Complete(ctx, db, first, now.Add(10*time.Second), func(*sql.Tx) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed || called {
		t.Fatal("expired prior owner completed the replacement lease")
	}
}

func TestCompleteRejectsWrongOwnerAndRollsBackFailure(t *testing.T) {
	db := newLeaseTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	lease, claimed, err := updatelease.Claim(ctx, db, now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim: claimed=%t err=%v", claimed, err)
	}

	wrong := lease
	wrong.OwnerToken = "wrong-owner"
	called := false
	completed, err := updatelease.Complete(ctx, db, wrong, now.Add(time.Second), func(*sql.Tx) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed || called {
		t.Fatal("wrong owner was allowed to complete")
	}

	wantErr := errors.New("forced persistence failure")
	if _, err := updatelease.Complete(ctx, db, lease, now.Add(time.Second), func(*sql.Tx) error {
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("completion error = %v, want %v", err, wantErr)
	}
	if _, claimed, err := updatelease.Claim(ctx, db, now.Add(2*time.Second), time.Minute); err != nil {
		t.Fatal(err)
	} else if claimed {
		t.Fatal("failed completion removed the live lease")
	}

	completed, err = updatelease.Complete(ctx, db, lease, now.Add(3*time.Second), func(*sql.Tx) error {
		return nil
	})
	if err != nil || !completed {
		t.Fatalf("owner completion: completed=%t err=%v", completed, err)
	}
	if _, claimed, err := updatelease.Claim(ctx, db, now.Add(4*time.Second), time.Minute); err != nil {
		t.Fatal(err)
	} else if !claimed {
		t.Fatal("successful completion did not remove the lease")
	}
}
