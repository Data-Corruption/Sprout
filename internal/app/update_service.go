// --- FILE update ---

package app

// --- BEGIN service ---
import (
	"context"
	"errors"
	"fmt"
	"sprout/internal/platform/database/config"
	"sync"
	"time"
)

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
			// --- BEGIN update.apply.auto ---
			retrySoon = a.runAutomaticUpdate(ctx, true)
			if ctx.Err() != nil {
				return nil
			}
			// --- END update.apply.auto ---
			markReady()
		} else {
			if ctx.Err() != nil {
				return nil
			}
			if step.checkErr != nil {
				a.Log.Errorf("update check failed: %v", step.checkErr)
			} else if step.completed {
				a.Log.Debugf("service update check complete: available=%t", step.available)
				// --- BEGIN update.apply.auto ---
				retrySoon = a.runAutomaticUpdate(ctx, step.available)
				if ctx.Err() != nil {
					return nil
				}
				// --- END update.apply.auto ---
			}
		}

		delay := updateCheckLeaseDuration
		if !retrySoon {
			latest, viewErr := config.View(a.DB)
			if viewErr != nil {
				a.Log.Errorf("view config for service update delay: %v", viewErr)
			} else {
				switch {
				case !latest.BackgroundUpdateChecks:
					delay = time.Hour
				case !a.periodicUpdateCheckDue(latest, time.Now()):
					delay = time.Until(latest.LastUpdateCheck.Add(UpdateCheckInterval))
					if delay < time.Second {
						delay = time.Second
					}
				}
			}
		}
		// Notice and scheduling preferences can change in another process.
		// Poll configuration without increasing the daily network-check rate.
		delay = min(delay, time.Minute)
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
	if cfg.BackgroundUpdateChecks && a.UpdateAvailable(cfg) && !a.periodicUpdateCheckDue(cfg, time.Now()) {
		return serviceUpdateStep{freshAvailability: true}, nil
	}

	// Reading service configuration is this component's startup preflight. A
	// network check may proceed after service readiness.
	markReady()
	var step serviceUpdateStep
	step.available, step.completed, step.checkErr = a.checkForPeriodicUpdateLocked(ctx)
	return step, nil
}

// --- END service ---
