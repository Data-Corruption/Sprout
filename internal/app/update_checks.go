// --- FILE update ---

package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"sprout/internal/platform/database/config"
	"sprout/internal/platform/database/updatelease"
	"sprout/internal/types"
)

const UpdateCheckInterval = 24 * time.Hour
const updateCheckLeaseDuration = time.Minute

func setUpdateCheckResult(cfg *types.Configuration, result updateResult, checkedAt time.Time) {
	cfg.UpdateCheckSource = result.source
	cfg.LatestUpdateVersion = result.version
	cfg.LastUpdateCheck = checkedAt
}

func (a *App) periodicUpdateCheckDue(cfg *types.Configuration, now time.Time) bool {
	source, err := a.releaseURL()
	return cfg.BackgroundUpdateChecks && (err != nil || source != cfg.UpdateCheckSource ||
		now.Sub(cfg.LastUpdateCheck) >= UpdateCheckInterval-time.Minute)
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
	if !a.periodicUpdateCheckDue(cfg, now) {
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
	if !a.periodicUpdateCheckDue(cfg, time.Now()) {
		if err := updatelease.Release(ctx, a.DB, lease); err != nil {
			return false, false, err
		}
		return false, false, nil
	}

	result, err := a.checkForUpdate(ctx)
	if err != nil {
		// A failed or canceled network check deliberately leaves the lease
		// behind until expiry.
		return false, false, err
	}

	checkedAt := time.Now()
	completed, err = updatelease.Complete(ctx, a.DB, lease, checkedAt, func(tx *sql.Tx) error {
		_, err := config.UpdateTx(tx, func(cfg *types.Configuration) error {
			setUpdateCheckResult(cfg, result, checkedAt)
			return nil
		})
		return err
	})
	if err != nil {
		return false, false, err
	}
	return result.available(a.buildInfo.Version), completed, nil
}

// StartUpdateCheckIfDue starts one cancellable background check for ordinary
// commands. It can persist availability, but it never applies an update.
func (a *App) StartUpdateCheckIfDue(ctx context.Context) error {
	if a.buildInfo.DevMode {
		return nil
	}

	// Missing source metadata disables discovery without a public-host fallback.
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
	if cfg.UpdateNotifications && a.UpdateAvailable(cfg) {
		fmt.Printf("Update available! Run '%s update' to review it.\n", a.buildInfo.Name)
	}
	if !a.periodicUpdateCheckDue(cfg, time.Now()) {
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
