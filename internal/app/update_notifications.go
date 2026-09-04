// --- FILE update.notifications ---

package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"sprout/internal/maintenance"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/database/updatelease"
	"sprout/internal/types"
)

const UpdateCheckInterval = 24 * time.Hour
const updateCheckLeaseDuration = time.Minute

// CheckForUpdateAndNotify performs a fresh check and persists its result for
// CLI/dashboard notices.
func (a *App) CheckForUpdateAndNotify(ctx context.Context) (bool, error) {
	a.updateCheckMu.Lock()
	defer a.updateCheckMu.Unlock()

	updateAvailable, err := a.checkForUpdate(ctx)
	if err != nil {
		return false, err
	}
	if err := a.recordUpdateCheck(updateAvailable); err != nil {
		return false, err
	}
	return updateAvailable, nil
}

func (a *App) recordUpdateCheck(updateAvailable bool) error {
	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		setUpdateCheckResult(cfg, updateAvailable, time.Now())
		return nil
	}); err != nil {
		return fmt.Errorf("persist update-check result: %w", err)
	}
	return nil
}

func setUpdateCheckResult(cfg *types.Configuration, updateAvailable bool, checkedAt time.Time) {
	cfg.UpdateAvailable = updateAvailable
	cfg.LastUpdateCheck = checkedAt
}

func periodicUpdateCheckDue(cfg *types.Configuration, now time.Time) bool {
	return cfg.UpdateNotifications &&
		now.Sub(cfg.LastUpdateCheck) >= UpdateCheckInterval-time.Minute
}

// checkForPeriodicUpdate obtains the cross-process lease before making the
// network request. The returned completed flag is true only after the result
// and lease removal have committed atomically.
func (a *App) checkForPeriodicUpdate(ctx context.Context) (updateAvailable, completed bool, err error) {
	a.updateCheckMu.Lock()
	defer a.updateCheckMu.Unlock()
	return a.checkForPeriodicUpdateLocked(ctx)
}

func (a *App) checkForPeriodicUpdateLocked(ctx context.Context) (updateAvailable, completed bool, err error) {
	now := time.Now()
	cfg, err := config.View(a.DB)
	if err != nil {
		return false, false, fmt.Errorf("view config for periodic update check: %w", err)
	}
	if !periodicUpdateCheckDue(cfg, now) {
		return false, false, nil
	}

	lease, claimed, err := updatelease.Claim(ctx, a.DB, now, updateCheckLeaseDuration)
	if err != nil {
		return false, false, err
	}
	if !claimed {
		return false, false, nil
	}

	// Recheck after claiming. A manual check may have completed between the
	// optimistic config read and lease acquisition.
	cfg, err = config.View(a.DB)
	if err != nil {
		// Treat a config read error as a failed check: retain the lease so
		// another process does not immediately amplify a database failure.
		return false, false, fmt.Errorf("recheck config for periodic update check: %w", err)
	}
	if !periodicUpdateCheckDue(cfg, time.Now()) {
		if err := updatelease.Release(ctx, a.DB, lease); err != nil {
			return false, false, err
		}
		return false, false, nil
	}

	updateAvailable, err = a.checkForUpdate(ctx)
	if err != nil {
		// A failed or canceled network check deliberately leaves the lease
		// behind until expiry.
		return false, false, err
	}

	checkedAt := time.Now()
	completed, err = updatelease.Complete(ctx, a.DB, lease, checkedAt, func(tx *sql.Tx) error {
		_, err := config.UpdateTx(tx, func(cfg *types.Configuration) error {
			setUpdateCheckResult(cfg, updateAvailable, checkedAt)
			return nil
		})
		return err
	})
	if err != nil {
		return false, false, err
	}
	return updateAvailable, completed, nil
}

// StartUpdateCheckIfDue starts one cancellable background check for ordinary
// commands. It can persist availability, but it never applies an update.
func (a *App) StartUpdateCheckIfDue(ctx context.Context) error {
	if a.buildInfo.DevMode {
		return nil
	}

	// A missing release-url means this installation should not manage updates,
	// such as an installation sourced from a mirror.
	if _, err := a.releaseURL(); err != nil {
		if errors.Is(err, ErrUpdatesDisabled) {
			a.Log.Debugf("update checking disabled: %v", err)
			return nil
		}
		return err
	}

	cfg, err := config.View(a.DB)
	if err != nil {
		return fmt.Errorf("view config for update check: %w", err)
	}
	if cfg.UpdateNotifications && cfg.UpdateAvailable {
		fmt.Printf("Update available! Run '%s update' to review it.\n", a.buildInfo.Name)
	}
	if !periodicUpdateCheckDue(cfg, time.Now()) {
		return nil
	}

	checkCtx, cancel := context.WithCancel(ctx)
	var waitGroup sync.WaitGroup
	a.AddCleanup(func() error {
		cancel()
		waitGroup.Wait()
		return nil
	})
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		updateAvailable, completed, err := a.checkForPeriodicUpdate(checkCtx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				a.Log.Errorf("update check failed: %v", err)
			}
			return
		}
		if completed {
			a.Log.Debugf("one-shot update check complete: available=%t", updateAvailable)
		}
	}()
	return nil
}

// RunUpdateChecker is the service-owned update loop. Unlike the root one-shot
// check, this component may admit a detached automatic update.
func (a *App) RunUpdateChecker(ctx context.Context, ready func()) error {
	var readyOnce sync.Once
	markReady := func() {
		readyOnce.Do(func() {
			if ready != nil {
				ready()
			}
		})
	}
	if a.buildInfo.DevMode {
		markReady()
		<-ctx.Done()
		return nil
	}
	for {
		if _, err := a.releaseURL(); err == nil {
			break
		} else if errors.Is(err, ErrUpdatesDisabled) {
			markReady()
			<-ctx.Done()
			return nil
		} else {
			// Update metadata is optional to the service itself. Keep the
			// component alive and retry instead of briefly publishing service
			// readiness and then taking the whole service down.
			a.Log.Errorf("update checker configuration failed: %v", err)
			markReady()
			timer := time.NewTimer(time.Hour)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return nil
			case <-timer.C:
			}
		}
	}

	for {
		step, err := a.serviceUpdateStep(ctx, markReady)
		if err != nil {
			return err
		}
		retrySoon := false
		if step.freshAvailability {
			// --- BEGIN update.self ---
			// --- BEGIN update.auto ---
			retrySoon = a.runAutomaticUpdate(ctx, true)
			if ctx.Err() != nil {
				return nil
			}
			// --- END update.auto ---
			// --- END update.self ---
			markReady()
		} else {
			if ctx.Err() != nil {
				return nil
			}
			if step.checkErr != nil {
				a.Log.Errorf("update check failed: %v", step.checkErr)
			} else if step.completed {
				a.Log.Debugf("service update check complete: available=%t", step.available)
				// --- BEGIN update.self ---
				// --- BEGIN update.auto ---
				retrySoon = a.runAutomaticUpdate(ctx, step.available)
				if ctx.Err() != nil {
					return nil
				}
				// --- END update.auto ---
				// --- END update.self ---
			}
		}

		delay := updateCheckLeaseDuration
		if !retrySoon {
			latest, viewErr := config.View(a.DB)
			if viewErr != nil {
				a.Log.Errorf("view config for service update delay: %v", viewErr)
			} else {
				switch {
				case !latest.UpdateNotifications:
					delay = time.Hour
				case !periodicUpdateCheckDue(latest, time.Now()):
					delay = time.Until(latest.LastUpdateCheck.Add(UpdateCheckInterval))
					if delay < time.Second {
						delay = time.Second
					}
				}
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

// serviceUpdateStep is one decision of the service update loop.
type serviceUpdateStep struct {
	// freshAvailability reports a persisted, still-current availability
	// result. No network check ran and markReady was not called.
	freshAvailability bool
	// Results of the periodic check, valid when freshAvailability is false.
	available, completed bool
	checkErr             error
}

// serviceUpdateStep holds updateCheckMu across the availability decision and
// the periodic check. A short CLI may have completed the network check just
// before the service acquired the mutex; holding it across both closes the
// inverse race too: a one-shot that starts just after an optimistic read
// cannot make the service sleep for a day without consuming its result.
func (a *App) serviceUpdateStep(ctx context.Context, markReady func()) (serviceUpdateStep, error) {
	a.updateCheckMu.Lock()
	defer a.updateCheckMu.Unlock()

	cfg, err := config.View(a.DB)
	if err != nil {
		return serviceUpdateStep{}, fmt.Errorf("view config for service update checker: %w", err)
	}
	if cfg.UpdateNotifications && cfg.UpdateAvailable && !periodicUpdateCheckDue(cfg, time.Now()) {
		return serviceUpdateStep{freshAvailability: true}, nil
	}

	// Reading service configuration is this component's startup preflight. A
	// network check may proceed after service readiness.
	markReady()
	var step serviceUpdateStep
	step.available, step.completed, step.checkErr = a.checkForPeriodicUpdateLocked(ctx)
	return step, nil
}

// --- BEGIN update.self ---
// --- BEGIN update.auto ---
// runAutomaticUpdate admits a detached update when one is available and waits
// for its controller. A successful controller drains this service by
// cancelling ctx. When it ends without draining, its job directory is gone and
// the failure was pre-transition or safely rolled back, so the caller retries
// after a short backoff: retrySoon is true whenever a launch was attempted.
func (a *App) runAutomaticUpdate(ctx context.Context, available bool) (retrySoon bool) {
	if !available {
		return false
	}
	watch := "watch automatic update job"
	if err := maybeStartAutomaticUpdate(true, func() error {
		_, err := a.StartMaintenance(ctx, maintenance.ActionUpdate)
		return err
	}); err != nil {
		a.Log.Errorf("automatic update launch failed: %v", err)
		// A launch that timed out ambiguously may still have admitted a job;
		// waiting is a no-op when nothing was recorded.
		watch = "watch ambiguously admitted update job"
	}
	if err := a.waitForMaintenance(ctx); err != nil && !errors.Is(err, context.Canceled) {
		a.Log.Errorf("%s: %v", watch, err)
	}
	return true
}

func maybeStartAutomaticUpdate(updateAvailable bool, start func() error) error {
	if !updateAvailable {
		return nil
	}
	if err := start(); err != nil {
		return fmt.Errorf("start detached updater: %w", err)
	}
	return nil
}

// --- END update.auto ---
// --- END update.self ---
