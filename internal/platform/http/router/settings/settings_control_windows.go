//go:build windows

// --- FILE service.https ---

package settings

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"sprout/internal/app"
)

// requestServiceStop asks the service coordinator to cancel every retained
// component, then retires the consumed lease after this process exits. The
// lease is written first so the helper knows exactly which bytes it may
// tombstone; if the helper cannot start, the lease simply expires on its own.
func requestServiceStop(a *app.App) {
	if err := a.RequestServiceStop(); err != nil {
		a.Log.Errorf("failed to request graceful service stop: %v", err)
		go a.Server.Shutdown()
		return
	}
	if err := startWindowsExitHelper(a, false); err != nil {
		a.Log.Errorf("failed to start stop cleanup helper: %v", err)
	}
}

// requestServiceRestart restarts the managed scheduled task. Task Scheduler
// has no systemd-style Restart=always for clean exits, so a detached helper
// waits for this process to exit and starts the task again.
// Unmanaged/dev builds simply stop.
func requestServiceRestart(a *app.App) {
	if !a.BuildInfo().ServiceEnabled || a.DevMode {
		go a.Server.Shutdown()
		return
	}

	if err := a.RequestServiceStop(); err != nil {
		a.Log.Errorf("failed to request graceful service restart: %v", err)
		go a.Server.Shutdown()
		return
	}
	if err := startWindowsExitHelper(a, true); err != nil {
		// The stop is already in flight and cannot be recalled; without the
		// helper nothing will start the task again, so this is a plain stop.
		a.Log.Errorf("failed to start restart helper; the service will stop without restarting: %v", err)
	}
}

// startWindowsExitHelper detaches a PowerShell process that waits for this
// process to exit, retires the stop lease this process wrote (and only that
// one, mirroring App.ReleaseServiceStopRequest), and optionally starts the
// scheduled task again.
func startWindowsExitHelper(a *app.App, restart bool) error {
	taskName := psQuote(a.BuildInfo().Name)
	leasePath := psQuote(a.Layout.ServiceStop)
	// The lease is compared trimmed so the command line never carries a raw
	// newline; the on-disk value is digits plus one trailing newline.
	ownedLease := psQuote(strings.TrimSpace(string(a.ServiceStopLease())))
	script := fmt.Sprintf(
		"$ErrorActionPreference='Stop'; "+
			"Wait-Process -Id %d -ErrorAction SilentlyContinue; "+
			"if ((Test-Path -LiteralPath %s) -and ([IO.File]::ReadAllText(%s).Trim() -eq %s)) { [IO.File]::WriteAllText(%s, \"0`n\") }",
		os.Getpid(),
		leasePath,
		leasePath,
		ownedLease,
		leasePath,
	)
	if restart {
		script += fmt.Sprintf(
			"; $deadline=[DateTime]::UtcNow.AddSeconds(30); "+
				"do { "+
				"$task=Get-ScheduledTask -TaskPath '\\' -TaskName %s -ErrorAction SilentlyContinue; "+
				"if ($null -eq $task) { throw 'Scheduled task disappeared during restart.' }; "+
				"$state=$task.State.ToString(); "+
				"if ($state -eq 'Ready') { Start-ScheduledTask -TaskPath '\\' -TaskName %s; exit 0 }; "+
				"if ($state -eq 'Disabled') { throw 'Scheduled task is disabled.' }; "+
				"Start-Sleep -Milliseconds 250 "+
				"} while ([DateTime]::UtcNow -lt $deadline); "+
				"throw 'Timed out waiting for the scheduled task to stop.'",
			taskName,
			taskName,
		)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// psQuote wraps value in PowerShell single quotes, where only the quote
// itself needs escaping.
func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
