//go:build !windows

// --- FILE service ---

package commands

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"sprout/internal/app"
	"sprout/internal/build"
	"sprout/internal/layout"
)

func TestServiceHelpDistinguishesConfigFromOptionalEnvironment(t *testing.T) {
	a := app.New(build.BuildInfo{Name: "sprout"})
	a.Layout = layout.FromStorage(filepath.Join(t.TempDir(), "state"), "sprout")
	var output bytes.Buffer

	printServiceHelpTo(&output, a)
	got := output.String()
	for _, want := range []string{
		"sprout config set --help  (persistent application settings)",
		a.Layout.Env + " then restart  (optional systemd environment)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("service help missing %q:\n%s", want, got)
		}
	}
}
