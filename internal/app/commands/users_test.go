// --- FILE service.https ---

package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/database/sessions"
	"sprout/internal/types"
	"sprout/pkg/xlog"
)

func newCredentialRemovalTestDB(t *testing.T) *sql.DB {
	t.Helper()
	root := t.TempDir()
	logger, err := xlog.New(filepath.Join(root, "logs"), "error")
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.New(
		filepath.Join(root, "db"),
		logger,
		build.BuildInfo{DefaultLogLevel: "warn", ServiceDefaultPort: 8484},
		database.ApplyPendingMigrations,
	)
	if err != nil {
		_ = logger.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = logger.Close()
	})
	return db
}

func TestRemoveCredentialRevokesOnlyItsSessions(t *testing.T) {
	db := newCredentialRemovalTestDB(t)
	addRemovalTestCredentials(t, db)
	createRemovalTestSession(t, db, "victim-token", "victim")
	createRemovalTestSession(t, db, "other-token", "other")

	revoked, err := removeCredential(context.Background(), db, "victim")
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Fatalf("revoked sessions = %d, want 1", revoked)
	}

	cfg, err := config.View(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Credentials) != 1 || cfg.Credentials[0].Username != "other" {
		t.Fatalf("credentials after removal = %+v", cfg.Credentials)
	}
	if session, err := sessions.Get(db, "victim-token"); err != nil || session != nil {
		t.Fatalf("victim session after removal = %+v, err=%v", session, err)
	}
	if session, err := sessions.Get(db, "other-token"); err != nil || session == nil {
		t.Fatalf("other session after removal = %+v, err=%v", session, err)
	}
}

func TestRemoveCredentialRollsBackWhenSessionDeletionFails(t *testing.T) {
	db := newCredentialRemovalTestDB(t)
	addRemovalTestCredentials(t, db)
	createRemovalTestSession(t, db, "victim-token", "victim")
	if _, err := db.Exec(`
		CREATE TRIGGER fail_victim_session_delete
		BEFORE DELETE ON sessions
		WHEN OLD.username = 'victim'
		BEGIN
			SELECT RAISE(ABORT, 'forced session delete failure');
		END;
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := removeCredential(context.Background(), db, "victim"); err == nil ||
		!strings.Contains(err.Error(), "forced session delete failure") {
		t.Fatalf("credential removal error = %v", err)
	}
	assertCredentialAndSessionRemain(t, db)
}

func TestRemoveCredentialRollsBackOnConfigValidationFailure(t *testing.T) {
	db := newCredentialRemovalTestDB(t)
	addRemovalTestCredentials(t, db)
	createRemovalTestSession(t, db, "victim-token", "victim")

	cfg, err := config.View(db)
	if err != nil {
		t.Fatal(err)
	}
	cfg.UIBind = "not-a-bind"
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`UPDATE config SET value = ? WHERE key = ?`,
		string(data),
		database.ConfigDataKey,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := removeCredential(context.Background(), db, "victim"); err == nil ||
		!strings.Contains(err.Error(), "invalid configuration") {
		t.Fatalf("credential removal error = %v", err)
	}
	assertCredentialAndSessionRemain(t, db)
}

func addRemovalTestCredentials(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := config.Update(db, func(cfg *types.Configuration) error {
		cfg.Credentials = []types.Credential{
			{Username: "victim"},
			{Username: "other"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func createRemovalTestSession(t *testing.T, db *sql.DB, tokenHash, username string) {
	t.Helper()
	if err := sessions.Create(db, tokenHash, sessions.Session{
		Expiry:   time.Now().Add(time.Hour),
		Username: username,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertCredentialAndSessionRemain(t *testing.T, db *sql.DB) {
	t.Helper()
	cfg, err := config.View(db)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, credential := range cfg.Credentials {
		found = found || credential.Username == "victim"
	}
	if !found {
		t.Fatal("victim credential was not rolled back")
	}
	if session, err := sessions.Get(db, "victim-token"); err != nil || session == nil {
		t.Fatalf("victim session after rollback = %+v, err=%v", session, err)
	}
}
