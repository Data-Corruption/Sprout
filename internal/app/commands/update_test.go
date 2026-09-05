// --- FILE update ---

package commands

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"sprout/internal/app"
	"sprout/internal/build"
	"sprout/internal/platform/database"
	"sprout/internal/platform/database/config"
	"sprout/internal/types"
	"sprout/pkg/xlog"

	"github.com/urfave/cli/v3"
)

func TestUpdateCommandHasNoCheckFlag(t *testing.T) {
	command := updateCommand(&app.App{})
	for _, flag := range command.Flags {
		boolean, ok := flag.(*cli.BoolFlag)
		if ok && boolean.Name == "check" {
			t.Fatal("update command still exposes --check")
		}
	}
}

// --- BEGIN update.apply ---
func TestApplyAvailableUpdate(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		var output bytes.Buffer
		asked, scheduled := 0, 0
		err := applyAvailableUpdate(
			false,
			true,
			func(string) (bool, error) {
				asked++
				return true, nil
			},
			func() (string, error) {
				scheduled++
				return "/tmp/maintenance.log", nil
			},
			&output,
		)
		if err != nil {
			t.Fatal(err)
		}
		if asked != 1 || scheduled != 1 {
			t.Fatalf("asked=%d scheduled=%d, want 1/1", asked, scheduled)
		}
	})

	t.Run("declined", func(t *testing.T) {
		var output bytes.Buffer
		scheduled := 0
		err := applyAvailableUpdate(
			false,
			true,
			func(string) (bool, error) { return false, nil },
			func() (string, error) {
				scheduled++
				return "/tmp/maintenance.log", nil
			},
			&output,
		)
		if err != nil {
			t.Fatal(err)
		}
		if scheduled != 0 || !strings.Contains(output.String(), "declined") {
			t.Fatalf("scheduled=%d output=%q", scheduled, output.String())
		}
	})

	t.Run("yes skips prompt", func(t *testing.T) {
		asked, scheduled := 0, 0
		err := applyAvailableUpdate(
			true,
			false,
			func(string) (bool, error) {
				asked++
				return false, nil
			},
			func() (string, error) {
				scheduled++
				return "/tmp/maintenance.log", nil
			},
			&bytes.Buffer{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if asked != 0 || scheduled != 1 {
			t.Fatalf("asked=%d scheduled=%d, want 0/1", asked, scheduled)
		}
	})

	t.Run("non-interactive requires yes", func(t *testing.T) {
		err := applyAvailableUpdate(
			false,
			false,
			func(string) (bool, error) { return true, nil },
			func() (string, error) { return "", nil },
			&bytes.Buffer{},
		)
		if err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("error = %v, want --yes guidance", err)
		}
	})

	t.Run("unsupported uses source-neutral guidance", func(t *testing.T) {
		var output bytes.Buffer
		err := applyAvailableUpdate(
			true,
			false,
			func(string) (bool, error) { return false, nil },
			func() (string, error) { return "", app.ErrUpdatesDisabled },
			&output,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), app.UpdateGuidance) {
			t.Fatalf("output = %q, want source-neutral guidance", output.String())
		}
	})

	t.Run("launch error surfaces", func(t *testing.T) {
		want := errors.New("boom")
		err := applyAvailableUpdate(
			true,
			false,
			func(string) (bool, error) { return false, nil },
			func() (string, error) { return "", want },
			&bytes.Buffer{},
		)
		if !errors.Is(err, want) {
			t.Fatalf("error = %v, want wrapped launch failure", err)
		}
	})
}

// --- END update.apply ---

func TestUpdatePreferencesAreExplicitAndIndependent(t *testing.T) {
	root := t.TempDir()
	logger, err := xlog.New(filepath.Join(root, "logs"), "error")
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	db, err := database.New(filepath.Join(root, "db"), logger, build.Info(), database.ApplyPendingMigrations)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &app.App{DB: db, Log: logger}
	run := func(args ...string) *types.Configuration {
		t.Helper()
		if err := updateCommand(a).Run(context.Background(), append([]string{"update"}, args...)); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.View(db)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	cfg := run("--notify=false")
	if cfg.UpdateNotifications || !cfg.BackgroundUpdateChecks {
		t.Fatalf("hiding notices changed scheduling: %+v", cfg)
	}
	cfg = run("--notify=false")
	if cfg.UpdateNotifications {
		t.Fatal("repeating --notify=false toggled notices on")
	}
	cfg = run("--background=false")
	if cfg.BackgroundUpdateChecks {
		t.Fatal("background checks remained enabled")
	}
	// --- BEGIN update.apply.auto ---
	if cfg.AutomaticUpdates {
		t.Fatal("automatic application enabled by default")
	}
	cfg = run("--automatic=true")
	if !cfg.AutomaticUpdates || !cfg.BackgroundUpdateChecks || cfg.UpdateNotifications {
		t.Fatalf("automatic preference affected notices or failed to enable checks: %+v", cfg)
	}
	cfg = run("--automatic=false")
	if cfg.AutomaticUpdates || !cfg.BackgroundUpdateChecks {
		t.Fatalf("disabling automatic application disabled discovery: %+v", cfg)
	}
	// --- END update.apply.auto ---
}
