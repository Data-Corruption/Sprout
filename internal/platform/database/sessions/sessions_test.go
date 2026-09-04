// --- FILE service.https ---

package sessions

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/pkg/xlog"
)

func newSessionTestDB(t *testing.T) *sql.DB {
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

func TestDeleteByUsernameTxObeysCallerTransaction(t *testing.T) {
	db := newSessionTestDB(t)
	session := Session{Expiry: time.Now().Add(time.Hour), Username: "victim"}
	if err := Create(db, "one", session); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := DeleteByUsernameTx(tx, "victim"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	} else if n != 1 {
		tx.Rollback()
		t.Fatalf("deleted sessions = %d, want 1", n)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := Get(db, "one"); err != nil {
		t.Fatal(err)
	} else if got == nil {
		t.Fatal("rolled-back session deletion was persisted")
	}

	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteByUsernameTx(tx, "victim"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, err := Get(db, "one"); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatal("committed session deletion was not persisted")
	}
}
