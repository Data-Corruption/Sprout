// --- FILE update ---

package commands

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"sprout/internal/app"

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

// --- BEGIN update.self ---
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

// --- END update.self ---
