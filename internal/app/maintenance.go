package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sprout/internal/maintenance"
)

var ErrDevBuild = errors.New("development build detected, skipping")

// StartMaintenance admits one detached installer-owned transaction for this
// process. It deliberately has no caller-origin or initiating-PID mode: the
// lifecycle transition drains every matching application instance.
func (a *App) StartMaintenance(ctx context.Context, action maintenance.Action) (string, error) {
	a.maintenanceMu.Lock()
	defer a.maintenanceMu.Unlock()
	running, err := a.refreshMaintenanceLocked()
	if err != nil {
		return "", err
	}
	if running {
		if a.maintenanceAction != action {
			return "", fmt.Errorf("%s maintenance is already in progress", a.maintenanceAction)
		}
		return a.maintenanceLog, nil
	}
	if a.buildInfo.Version == "" {
		return "", fmt.Errorf("app version is not set")
	}
	if a.DevMode {
		return "", ErrDevBuild
	}

	// --- BEGIN update.self ---
	if action == maintenance.ActionUpdate {
		if err := a.setUpdateAvailable(false); err != nil {
			return "", err
		}
	}
	// --- END update.self ---

	admission, err := maintenance.StartMaintenance(ctx, maintenance.LaunchOptions{
		Layout:       a.Layout,
		Name:         a.buildInfo.Name,
		Version:      a.buildInfo.Version,
		CertIdentity: a.buildInfo.CertIdentity,
		OIDCIssuer:   a.buildInfo.OidcIssuer,
		DevMode:      a.DevMode,
	}, action)
	if admission.JobDir != "" {
		a.maintenanceStarted = true
		a.maintenanceAction = action
		a.maintenanceJob = admission.JobDir
		a.maintenanceLog = admission.LogPath
		a.maintenanceAdmitted = time.Now()
	}
	if err != nil {
		// --- BEGIN update.self ---
		if action == maintenance.ActionUpdate && admission.JobDir == "" {
			if restoreErr := a.setUpdateAvailable(true); restoreErr != nil {
				return "", fmt.Errorf("%w; restore available-update flag: %v", err, restoreErr)
			}
		}
		// --- END update.self ---
		return "", err
	}
	return admission.LogPath, nil
}

// refreshMaintenanceLocked observes completion of the exact job this process
// admitted. An update controller that disappeared while the same version is
// still ready failed before committing (or rolled back), so it becomes
// eligible for retry. A controller that died without removing its job
// directory is treated the same way once the probe proves it is gone; this
// process is the only one that recorded that directory, so it is also the only
// one that can reap it. a.maintenanceMu must be held.
func (a *App) refreshMaintenanceLocked() (bool, error) {
	if !a.maintenanceStarted {
		return false, nil
	}
	status, err := maintenance.ProbeJob(a.maintenanceJob, a.maintenanceAdmitted)
	if err != nil {
		return false, fmt.Errorf("inspect admitted maintenance job: %w", err)
	}
	switch status {
	case maintenance.JobRunning:
		return true, nil
	case maintenance.JobOrphaned:
		a.Log.Warnf("maintenance %s job %s ended without cleaning up; removing its directory", a.maintenanceAction, filepath.Base(a.maintenanceJob))
		if err := os.RemoveAll(a.maintenanceJob); err != nil {
			return false, fmt.Errorf("remove orphaned maintenance job: %w", err)
		}
	}

	// --- BEGIN update.self ---
	if a.maintenanceAction == maintenance.ActionUpdate {
		state, err := maintenance.ReadState(a.Layout)
		if err != nil {
			return false, fmt.Errorf("read lifecycle state after maintenance job: %w", err)
		}
		if state.Phase == maintenance.PhaseReady && state.Version == a.buildInfo.Version {
			if err := a.setUpdateAvailable(true); err != nil {
				return false, fmt.Errorf("restore update after maintenance job: %w", err)
			}
		}
	}
	// --- END update.self ---

	a.maintenanceStarted = false
	a.maintenanceAction = ""
	a.maintenanceJob = ""
	a.maintenanceLog = ""
	a.maintenanceAdmitted = time.Time{}
	return false, nil
}

// waitForMaintenance waits until the admitted controller finishes or the
// application is cancelled by that controller's cooperative drain.
func (a *App) waitForMaintenance(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		a.maintenanceMu.Lock()
		running, err := a.refreshMaintenanceLocked()
		a.maintenanceMu.Unlock()
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
