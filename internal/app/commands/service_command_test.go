// --- FILE service ---

package commands

import (
	"testing"

	"sprout/internal/app"
	"sprout/internal/build"
)

func TestServiceCommandExposesManagedControlsAndHidesRun(t *testing.T) {
	command := serviceCommand(app.New(build.BuildInfo{
		Name:           "sprout",
		ServiceEnabled: true,
	}))
	if command == nil {
		t.Fatal("service command is disabled")
	}

	found := make(map[string]bool)
	for _, subcommand := range command.Commands {
		found[subcommand.Name] = true
		if subcommand.Name == "run" && !subcommand.Hidden {
			t.Fatal("service run must be hidden from normal help")
		}
	}
	for _, name := range []string{"start", "stop", "restart", "status", "run"} {
		if !found[name] {
			t.Errorf("service subcommand %q is missing", name)
		}
	}
	if found["set"] {
		t.Fatal("persistent settings still live under service set")
	}
}
