//go:build windows

// --- FILE service ---

package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"sprout/internal/app"
)

var (
	windowsGracefulStopTimeout = 15 * time.Second
	windowsForcedStopTimeout   = 10 * time.Second
	windowsStartTimeout        = 15 * time.Second
	windowsTaskPollInterval    = 250 * time.Millisecond
	queryWindowsTaskState      = getWindowsTaskState
	runWindowsControl          = runControlCommand
)

func controlService(ctx context.Context, a *app.App, action string) (string, error) {
	appName := a.BuildInfo().Name
	switch action {
	case "start":
		return startWindowsService(ctx, a, action)
	case "stop":
		return stopWindowsService(ctx, a, action)
	case "status":
		return runControlCommand(ctx, action, "schtasks.exe", "/Query", "/TN", appName, "/FO", "LIST", "/V")
	case "restart":
		stopOutput, stopErr := stopWindowsService(ctx, a, "restart (stop)")
		if stopErr != nil {
			return stopOutput, stopErr
		}
		startOutput, startErr := startWindowsService(ctx, a, "restart (start)")
		return joinControlOutput(stopOutput, startOutput), startErr
	default:
		return "", fmt.Errorf("unknown service action %q", action)
	}
}

func stopWindowsService(ctx context.Context, a *app.App, action string) (output string, err error) {
	appName := a.BuildInfo().Name
	defer func() {
		err = errors.Join(err, a.ReleaseServiceStopRequest())
	}()

	state, err := queryWindowsTaskState(ctx, appName)
	if err != nil {
		return "", fmt.Errorf("service %s could not query scheduled task: %w", action, err)
	}
	if state == "Missing" {
		return "", fmt.Errorf("service %s failed: scheduled task %q does not exist", action, appName)
	}
	if windowsTaskStopped(state) {
		return fmt.Sprintf("Scheduled task %q is already stopped.", appName), nil
	}

	leaseWritten := true
	if leaseErr := a.RequestServiceStop(); leaseErr != nil {
		leaseWritten = false
		if a.Log != nil {
			a.Log.Warnf("graceful service stop request failed, using Task Scheduler fallback: %v", leaseErr)
		}
	}
	if leaseWritten {
		graceCtx, cancel := context.WithTimeout(ctx, windowsGracefulStopTimeout)
		_, waitErr := waitForWindowsTask(graceCtx, appName, func(state string) bool {
			return windowsTaskStopped(state)
		})
		cancel()
		if waitErr == nil {
			return fmt.Sprintf("Scheduled task %q stopped gracefully.", appName), nil
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("service %s did not complete: %w", action, ctx.Err())
		}
	}

	endOutput, endErr := runWindowsControl(ctx, action+" fallback", "schtasks.exe", "/End", "/TN", appName)
	if endErr != nil {
		state, queryErr := queryWindowsTaskState(ctx, appName)
		if queryErr != nil || !windowsTaskStopped(state) {
			return endOutput, endErr
		}
	}

	forceCtx, cancel := context.WithTimeout(ctx, windowsForcedStopTimeout)
	state, waitErr := waitForWindowsTask(forceCtx, appName, func(state string) bool {
		return windowsTaskStopped(state)
	})
	cancel()
	if waitErr != nil {
		return endOutput, fmt.Errorf(
			"service %s fallback did not stop scheduled task %q (state %s): %w",
			action,
			appName,
			state,
			waitErr,
		)
	}
	return joinControlOutput(endOutput, fmt.Sprintf("Scheduled task %q stopped.", appName)), nil
}

func startWindowsService(ctx context.Context, a *app.App, action string) (string, error) {
	appName := a.BuildInfo().Name
	if err := a.ClearServiceStopRequest(); err != nil {
		return "", fmt.Errorf("service %s could not clear stop request: %w", action, err)
	}

	state, err := queryWindowsTaskState(ctx, appName)
	if err != nil {
		return "", fmt.Errorf("service %s could not query scheduled task: %w", action, err)
	}
	if state == "Missing" {
		return "", fmt.Errorf("service %s failed: scheduled task %q does not exist", action, appName)
	}
	if state == "Running" {
		return fmt.Sprintf("Scheduled task %q is already running.", appName), nil
	}

	output, err := runWindowsControl(ctx, action, "schtasks.exe", "/Run", "/TN", appName)
	if err != nil {
		return output, err
	}

	startCtx, cancel := context.WithTimeout(ctx, windowsStartTimeout)
	state, waitErr := waitForWindowsTask(startCtx, appName, func(state string) bool {
		return state == "Running"
	})
	cancel()
	if waitErr != nil {
		return output, fmt.Errorf(
			"service %s did not enter the Running state (state %s): %w",
			action,
			state,
			waitErr,
		)
	}
	return output, nil
}

func windowsTaskStopped(state string) bool {
	return state == "Missing" || state == "Ready" || state == "Disabled"
}

func waitForWindowsTask(ctx context.Context, appName string, done func(string) bool) (string, error) {
	var state string
	for {
		var err error
		state, err = queryWindowsTaskState(ctx, appName)
		if err != nil {
			return state, err
		}
		if done(state) {
			return state, nil
		}

		timer := time.NewTimer(windowsTaskPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return state, ctx.Err()
		case <-timer.C:
		}
	}
}

func getWindowsTaskState(ctx context.Context, appName string) (string, error) {
	script := "$task=Get-ScheduledTask -TaskPath '\\' -TaskName " + psQuote(appName) +
		" -ErrorAction SilentlyContinue; " +
		"if ($null -eq $task) { 'Missing' } else { $task.State.ToString() }"
	output, err := runControlCommand(
		ctx,
		"state query",
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		script,
	)
	if err != nil {
		return "", err
	}
	state := strings.TrimSpace(output)
	switch state {
	case "Missing", "Disabled", "Queued", "Ready", "Running", "Unknown":
		return state, nil
	default:
		return state, fmt.Errorf("unexpected scheduled task state %q", state)
	}
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func joinControlOutput(outputs ...string) string {
	var joined string
	for _, output := range outputs {
		if output == "" {
			continue
		}
		if joined != "" {
			joined += "\n"
		}
		joined += output
	}
	return joined
}
