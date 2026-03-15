//go:build linux

package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sprout/internal/platform/database/config"
	"sprout/internal/types"
	"sync"
	"syscall"
	"time"

	"github.com/Data-Corruption/lmdb-go/wrap"
	"github.com/Data-Corruption/stdx/xhttp"
	"golang.org/x/mod/semver"
)

const (
	UpdateTimeout       = 10 * time.Minute // max time for update process
	UpdateCheckInterval = 24 * time.Hour   // interval for update checks
)

var ErrDevBuild = &xhttp.Err{
	Code: http.StatusNotImplemented,
	Msg:  "development build detected, skipping...",
	Err:  fmt.Errorf("development build detected, skipping"),
}

// startAutoChecker starts a goroutine that checks for updates every [UpdateCheckInterval].
func (a *App) startAutoChecker(currentCfgCopy *types.Configuration) error {
	// if dev build, do nothing
	if a.buildInfo.Version == "vX.X.X" {
		return nil
	}

	// if update notifications are enabled, calculate initial delay for next check
	initialDelay := UpdateCheckInterval
	if currentCfgCopy.UpdateNotifications {
		// if last check was more than UpdateCheckInterval ago, do one right now
		if time.Since(currentCfgCopy.LastUpdateCheck) >= UpdateCheckInterval {
			var err error
			currentCfgCopy.UpdateAvailable, err = a.CheckForUpdate()
			if err != nil {
				a.Log.Errorf("Initial update check failed: %v", err) // may just be a network issue, so don't fail
			}
		} else {
			initialDelay = time.Until(currentCfgCopy.LastUpdateCheck.Add(UpdateCheckInterval))
		}
		// cli notification
		if currentCfgCopy.UpdateAvailable {
			fmt.Println("Update available! Run 'sprout update' to update to the latest version.")
		}
	}

	// start auto checker. on tick if update notifications are enabled, check for updates
	var acWaitGroup sync.WaitGroup
	acCloseChan := make(chan struct{})
	acWaitGroup.Add(1)
	go func() {
		defer acWaitGroup.Done()

		// handle initial delay interruptibly
		timer := time.NewTimer(initialDelay)
		select {
		case <-timer.C:
			// continue
		case <-acCloseChan:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}

		// check helper
		check := func() {
			cfg, err := config.View(a.DB)
			if err != nil {
				a.Log.Errorf("failed to view config: %v", err)
				return
			}
			// the -1 minute is to account for the time between the tick firing and LastUpdateCheck being set.
			// otherwise, on every other tick, the check would be skipped.
			if cfg.UpdateNotifications && time.Since(cfg.LastUpdateCheck) >= UpdateCheckInterval-time.Minute {
				if _, err := a.CheckForUpdate(); err != nil {
					a.Log.Errorf("Update check failed: %v", err) // may just be a network issue
				}
			}
		}
		check() // do one after initial delay

		// start periodic checks
		ticker := time.NewTicker(UpdateCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-acCloseChan:
				return
			case <-ticker.C:
				check()
			}
		}
	}()

	// ensure auto checker is stopped on cleanup
	a.AddCleanup(func() error {
		close(acCloseChan)
		acWaitGroup.Wait()
		return nil
	})

	return nil
}

// CheckForUpdate checks if there is a newer version of the application available and updates the config accordingly.
// It returns true if an update is available, false otherwise.
// When running a dev build (e.g. with `vX.X.X`), it returns false without checking.
func (a *App) CheckForUpdate() (bool, error) {
	if a.buildInfo.Version == "" {
		return false, fmt.Errorf("failed to get appVersion from context")
	}
	if a.buildInfo.Version == "vX.X.X" {
		return false, ErrDevBuild
	}

	lCtx, lCancel := context.WithTimeout(a.Context, 8*time.Second)
	defer lCancel()

	releaseURL, err := a.releaseURL()
	if err != nil {
		return false, err
	}

	latest, err := a.ReleaseSource.GetLatestVersion(lCtx, releaseURL)
	if err != nil {
		return false, err
	}

	updateAvailable := semver.Compare(latest, a.buildInfo.Version) > 0
	a.Log.Debugf("Latest version: %s, Current version: %s, Update available: %t", latest, a.buildInfo.Version, updateAvailable)

	// update config
	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.UpdateAvailable = updateAvailable
		cfg.LastUpdateCheck = time.Now()
		return nil
	}); err != nil {
		return false, fmt.Errorf("failed to update updateAvailable in config: %w", err)
	}

	return updateAvailable, nil
}

// DeferUpdate prepares the install/update script to be run on exit.
// It will prep the update regardless of if an update is available or not.
// You should exit soon after calling this.
// Calling either DeferUpdate or DetachUpdate more than once does nothing.
// Only the first call will have any effect.
func (a *App) DeferUpdate() error {
	var rErr error
	a.uOnce.Do(func() {
		if err := uPrep(a.buildInfo.Version, a.DB); err != nil {
			rErr = err
			return
		}

		releaseURL, err := a.releaseURL()
		if err != nil {
			rErr = err
			return
		}

		// prepare update command
		pipeline := fmt.Sprintf("curl -sSfL %s | sh", releaseURL+"install.sh")
		a.Log.Debugf("Prepared update, command: %s", pipeline)

		a.SetPostCleanup(func() error {
			rCtx, rCancel := context.WithTimeout(a.Context, UpdateTimeout)
			defer rCancel()

			cmd := exec.CommandContext(rCtx, "sh", "-c", pipeline)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return cmd.Run()
		})
	})
	return rErr
}

// DetachUpdate starts the install/update script as a detached process.
// It does so regardless of if an update is available or not.
// After calling this, the process will soon be closed externally by the install/update script.
// Calling either DeferUpdate or DetachUpdate more than once does nothing.
// Only the first call will have any effect.
func (a *App) DetachUpdate() error {
	var rErr error
	a.uOnce.Do(func() {
		if err := uPrep(a.buildInfo.Version, a.DB); err != nil {
			rErr = err
			return
		}

		releaseURL, err := a.releaseURL()
		if err != nil {
			rErr = err
			return
		}

		// prepare update command
		name := a.buildInfo.Name
		pipeline := fmt.Sprintf("curl -sSfL %s | sh", releaseURL+"install.sh")
		logPath := filepath.Join(a.StorageDir, "update.log")
		a.Log.Debugf("Prepared detached update: command: %s, logPath: %s", pipeline, logPath)

		// run update (install/update script will close this process)
		if err := runUpdateDetached(a.buildInfo.ServiceEnabled, name, pipeline, logPath); err != nil {
			rErr = err
			return
		}
	})
	return rErr
}

// uPrep prepares the update by setting updateAvailable to false and updateFollowup to the current version.
// After restart, updateFollowup will be used to lazily infer if an update was successful.
func uPrep(version string, db *wrap.DB) error {
	// double check version string
	if version == "" {
		return fmt.Errorf("failed to get appVersion")
	}
	if version == "vX.X.X" {
		return ErrDevBuild
	}
	// set updateAvailable to false since we're updating
	if _, err := config.Update(db, func(cfg *types.Configuration) error {
		cfg.UpdateAvailable = false
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update updateAvailable in config: %w", err)
	}
	return nil
}

func runUpdateDetached(serviceEnabled bool, name, pipeline, logPath string) error {
	if serviceEnabled {
		// Run as transient systemd service (like a service but one-off and
		// configured via cmdline args). Assuming this is run from in the daemon,
		// we need this to survive the parent process (service) exiting, which
		// will kill the c group, including any child processes. Even those started
		// using `cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}`. The service
		// needs to exit because the install script updates the unit file, etc.

		lCtx, lCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer lCancel()

		unitName := fmt.Sprintf("%s-update-%s", name, time.Now().Format("20060102-150405"))
		runtime := fmt.Sprintf("RuntimeMaxSec=%ds", int(UpdateTimeout.Seconds()))
		syslogIdent := fmt.Sprintf("SyslogIdentifier=%s-update", name)

		cmd := exec.CommandContext(
			lCtx,
			"systemd-run",
			"--user",
			"--unit="+unitName,
			"--quiet",
			"--no-block", // fully detached
			"-p", "StandardOutput=journal",
			"-p", "StandardError=journal",
			"-p", syslogIdent,
			"-p", runtime, // apply timeout
			"-p", "KillSignal=SIGINT",
			"-p", "TimeoutStopSec=30s", // graceful shutdown time
			"/bin/sh", "-c", pipeline,
		)
		return cmd.Run()
	} else {
		// Not under threat of c group being killed, so just use setsid
		// with shell-managed logging. escape logPath to be safe.
		pipelineWithLogging := fmt.Sprintf("( %s ) >> %q 2>&1", pipeline, logPath)
		cmd := exec.Command("sh", "-c", pipelineWithLogging)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start detached update: %w", err)
		}
		// release resources so the parent doesn't track the child (prevents zombies)
		if err := cmd.Process.Release(); err != nil {
			return fmt.Errorf("failed to release process: %w", err)
		}
		return nil
	}
}
