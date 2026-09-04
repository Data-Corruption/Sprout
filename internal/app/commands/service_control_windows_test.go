//go:build windows

// --- FILE service ---

package commands

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"sprout/internal/app"
	"sprout/internal/build"
	"sprout/internal/layout"
)

func TestStopWindowsServiceAlreadyStopped(t *testing.T) {
	a := windowsControlTestApp(t)
	restoreWindowsControlTestState(t)
	queryWindowsTaskState = func(context.Context, string) (string, error) {
		return "Ready", nil
	}
	runWindowsControl = func(context.Context, string, string, ...string) (string, error) {
		t.Fatal("Task Scheduler command ran for an already-stopped task")
		return "", nil
	}

	output, err := stopWindowsService(context.Background(), a, "stop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "already stopped") {
		t.Fatalf("output = %q", output)
	}
}

func TestStopWindowsServiceUsesGracefulLease(t *testing.T) {
	a := windowsControlTestApp(t)
	restoreWindowsControlTestState(t)
	calls := 0
	queryWindowsTaskState = func(context.Context, string) (string, error) {
		calls++
		if calls == 1 {
			return "Running", nil
		}
		if _, err := os.Stat(a.Layout.ServiceStop); err != nil {
			t.Fatalf("stop lease was not written: %v", err)
		}
		return "Ready", nil
	}
	runWindowsControl = func(context.Context, string, string, ...string) (string, error) {
		t.Fatal("hard-stop fallback ran after graceful shutdown")
		return "", nil
	}

	output, err := stopWindowsService(context.Background(), a, "stop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "stopped gracefully") {
		t.Fatalf("output = %q", output)
	}
	assertStopLeaseInactive(t, a)
}

func TestStopWindowsServiceFallsBackAfterTimeout(t *testing.T) {
	a := windowsControlTestApp(t)
	restoreWindowsControlTestState(t)
	windowsGracefulStopTimeout = 5 * time.Millisecond
	windowsTaskPollInterval = time.Millisecond
	forced := false
	queryWindowsTaskState = func(context.Context, string) (string, error) {
		if forced {
			return "Ready", nil
		}
		return "Unknown", nil
	}
	runWindowsControl = func(_ context.Context, action, executable string, args ...string) (string, error) {
		if executable != "schtasks.exe" || len(args) == 0 || args[0] != "/End" {
			t.Fatalf("unexpected fallback command: %s %v", executable, args)
		}
		if !strings.Contains(action, "fallback") {
			t.Fatalf("fallback action = %q", action)
		}
		forced = true
		return "ended", nil
	}

	output, err := stopWindowsService(context.Background(), a, "stop")
	if err != nil {
		t.Fatal(err)
	}
	if !forced || !strings.Contains(output, "ended") {
		t.Fatalf("fallback output = %q, forced = %t", output, forced)
	}
}

func TestRestartWaitsForStopAndClearsLeaseBeforeRun(t *testing.T) {
	a := windowsControlTestApp(t)
	restoreWindowsControlTestState(t)
	state := "Running"
	queryWindowsTaskState = func(context.Context, string) (string, error) {
		if state == "Running" {
			data, err := os.ReadFile(a.Layout.ServiceStop)
			if err == nil && strings.TrimSpace(string(data)) != "0" {
				state = "Ready"
			}
		}
		return state, nil
	}
	runWindowsControl = func(_ context.Context, _ string, executable string, args ...string) (string, error) {
		if executable != "schtasks.exe" || len(args) == 0 || args[0] != "/Run" {
			t.Fatalf("unexpected command: %s %v", executable, args)
		}
		assertStopLeaseInactive(t, a)
		state = "Running"
		return "started", nil
	}

	output, err := controlService(context.Background(), a, "restart")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "stopped gracefully") || !strings.Contains(output, "started") {
		t.Fatalf("restart output = %q", output)
	}
}

func windowsControlTestApp(t *testing.T) *app.App {
	t.Helper()
	a := app.New(build.BuildInfo{Name: "sprout-test", ServiceEnabled: true})
	a.Layout = layout.FromStorage(t.TempDir(), "sprout-test")
	return a
}

func assertStopLeaseInactive(t *testing.T, a *app.App) {
	t.Helper()
	data, err := os.ReadFile(a.Layout.ServiceStop)
	if err != nil {
		t.Fatalf("read cleared stop lease: %v", err)
	}
	if strings.TrimSpace(string(data)) != "0" {
		t.Fatalf("cleared stop lease = %q, want 0", data)
	}
}

func restoreWindowsControlTestState(t *testing.T) {
	t.Helper()
	oldGraceful := windowsGracefulStopTimeout
	oldForced := windowsForcedStopTimeout
	oldStart := windowsStartTimeout
	oldPoll := windowsTaskPollInterval
	oldQuery := queryWindowsTaskState
	oldRun := runWindowsControl
	t.Cleanup(func() {
		windowsGracefulStopTimeout = oldGraceful
		windowsForcedStopTimeout = oldForced
		windowsStartTimeout = oldStart
		windowsTaskPollInterval = oldPoll
		queryWindowsTaskState = oldQuery
		runWindowsControl = oldRun
	})
}
