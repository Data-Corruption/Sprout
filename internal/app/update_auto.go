// --- FILE update.apply.auto ---

package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sprout/internal/maintenance"
	"sprout/internal/platform/database/config"
)

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

// runAutomaticUpdate admits a detached update when one is available and waits
// for its controller. A successful controller drains this service by
// cancelling ctx. When it ends without draining, its job directory is gone and
// the failure was pre-transition or safely rolled back, so the caller retries
// after a short backoff: retrySoon is true whenever a launch was attempted.
func (a *App) runAutomaticUpdate(ctx context.Context, available bool) (retrySoon bool) {
	cfg, err := config.View(a.DB)
	if err != nil {
		a.Log.Errorf("read automatic update preference: %v", err)
		return false
	}
	if !available || !cfg.AutomaticUpdates || !cfg.BackgroundUpdateChecks {
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
