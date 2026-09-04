// --- FILE service.https ---

package config

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/internal/types"
	"strings"
	"testing"

	"sprout/pkg/xlog"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	tmp := t.TempDir()
	logger, err := xlog.New(filepath.Join(tmp, "logs"), "error")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	db, err := database.New(filepath.Join(tmp, "db"), logger, build.BuildInfo{
		DefaultLogLevel:    "warn",
		ServiceDefaultPort: 8484,
	}, database.ApplyPendingMigrations)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		logger.Close()
	})
	return db
}

func TestDefaultConfigUsesSafeDashboardBinds(t *testing.T) {
	production := types.DefaultConfig(build.BuildInfo{ServiceDefaultPort: 8484})
	if production.UIBind != "127.0.0.1:8484" {
		t.Fatalf("production UI bind = %q, want 127.0.0.1:8484", production.UIBind)
	}

	development := types.DefaultConfig(build.BuildInfo{
		ServiceDefaultPort: 8484,
		DevMode:            true,
	})
	if development.UIBind != "127.0.0.1:8484" {
		t.Fatalf("development UI bind = %q, want 127.0.0.1:8484", development.UIBind)
	}
}

func TestUpdatePreservesExplicitWildcardDashboardBind(t *testing.T) {
	db := newTestDB(t)
	if _, err := Update(db, func(cfg *types.Configuration) error {
		cfg.UIBind = ":9443"
		return nil
	}); err != nil {
		t.Fatalf("persist wildcard bind: %v", err)
	}

	cfg, err := View(db)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UIBind != ":9443" {
		t.Fatalf("persisted UI bind = %q, want :9443", cfg.UIBind)
	}
}

func TestUpdateValidates(t *testing.T) {
	db := newTestDB(t)

	tests := []struct {
		name    string
		mutate  func(cfg *types.Configuration)
		wantErr bool
	}{
		{
			name:   "valid settings pass",
			mutate: func(cfg *types.Configuration) { cfg.LogLevel = "warn"; cfg.UIBind = ":9090" },
		},
		{
			name:   "valid loopback proxy bind passes",
			mutate: func(cfg *types.Configuration) { cfg.ProxyBind = "127.0.0.1:8485" },
		},
		{
			name:   "empty proxy bind passes (disabled)",
			mutate: func(cfg *types.Configuration) { cfg.ProxyBind = "" },
		},
		{
			name:    "invalid log level rejected",
			mutate:  func(cfg *types.Configuration) { cfg.LogLevel = "verbose" },
			wantErr: true,
		},
		{
			name:    "bind without port rejected",
			mutate:  func(cfg *types.Configuration) { cfg.UIBind = "8484" },
			wantErr: true,
		},
		{
			name:    "bind with bad port rejected",
			mutate:  func(cfg *types.Configuration) { cfg.UIBind = ":99999" },
			wantErr: true,
		},
		{
			name:    "non-loopback proxy bind rejected",
			mutate:  func(cfg *types.Configuration) { cfg.ProxyBind = "0.0.0.0:8485" },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Update(db, func(cfg *types.Configuration) error {
				tt.mutate(cfg)
				return nil
			})
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("Update failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error")
			}
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("expected wrapped ValidationError, got %v", err)
			}
		})
	}

	// invalid values must not be persisted
	cfg, err := View(db)
	if err != nil {
		t.Fatalf("View failed: %v", err)
	}
	if cfg.LogLevel != "warn" || cfg.UIBind != ":9090" || cfg.ProxyBind != "" {
		t.Fatalf("rejected values leaked into config: %+v", cfg)
	}
}

func TestUpdateReportsAllViolations(t *testing.T) {
	db := newTestDB(t)

	_, err := Update(db, func(cfg *types.Configuration) error {
		cfg.LogLevel = "nope"
		cfg.UIBind = "nope"
		return nil
	})
	if err == nil {
		t.Fatal("expected validation error")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected wrapped ValidationError, got %v", err)
	}
	body := validationErr.Error()
	for _, want := range []string{"log level", "bind"} {
		if !strings.Contains(body, want) {
			t.Fatalf("joined message %q missing %q", body, want)
		}
	}
}

func TestUpdateNormalizesPersistedConfig(t *testing.T) {
	db := newTestDB(t)

	updated, err := Update(db, func(cfg *types.Configuration) error {
		cfg.LogLevel = " WARN "
		cfg.UIBind = " :9090 "
		cfg.ProxyBind = " 127.0.0.1:8485 "
		return nil
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if updated.LogLevel != "warn" || updated.UIBind != ":9090" || updated.ProxyBind != "127.0.0.1:8485" {
		t.Fatalf("config was not normalized: %+v", updated)
	}

	persisted, err := View(db)
	if err != nil {
		t.Fatalf("View failed: %v", err)
	}
	if persisted.LogLevel != updated.LogLevel || persisted.UIBind != updated.UIBind || persisted.ProxyBind != updated.ProxyBind {
		t.Fatalf("persisted config differs from returned config: persisted=%+v updated=%+v", persisted, updated)
	}
}

func TestUpdateNormalizesAndRequiresUniqueUsernames(t *testing.T) {
	db := newTestDB(t)

	updated, err := Update(db, func(cfg *types.Configuration) error {
		cfg.Credentials = append(cfg.Credentials, types.Credential{Username: " Admin "})
		return nil
	})
	if err != nil {
		t.Fatalf("add normalized username: %v", err)
	}
	if got := updated.Credentials[0].Username; got != "admin" {
		t.Fatalf("normalized username = %q, want admin", got)
	}

	_, err = Update(db, func(cfg *types.Configuration) error {
		cfg.Credentials = append(cfg.Credentials, types.Credential{Username: "ADMIN"})
		return nil
	})
	if err == nil {
		t.Fatal("expected duplicate normalized username to fail")
	}

	persisted, err := View(db)
	if err != nil {
		t.Fatalf("View failed: %v", err)
	}
	if len(persisted.Credentials) != 1 || persisted.Credentials[0].Username != "admin" {
		t.Fatalf("duplicate username was persisted: %+v", persisted.Credentials)
	}
}
