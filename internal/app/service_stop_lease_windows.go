//go:build windows

// --- FILE service ---

package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	ServiceStopLeaseFileName = "service.stop"
	// Keep this protocol duration in sync with scripts/install.ps1.
	ServiceStopLeaseDuration = time.Minute
	serviceStopPollInterval  = 250 * time.Millisecond
)

// serviceStopTombstone is an already-expired lease. Controllers overwrite
// rather than delete so a reader never sees the file vanish mid-poll.
const serviceStopTombstone = "0\n"

// RequestServiceStop writes a short-lived lease in the control directory that
// asks the running service instance to shut down. The written bytes are
// remembered so ReleaseServiceStopRequest can tell this request apart from a
// newer one.
func (a *App) RequestServiceStop() error {
	if err := os.MkdirAll(a.Layout.Control, 0o700); err != nil {
		return fmt.Errorf("create control directory: %w", err)
	}
	expires := time.Now().Add(ServiceStopLeaseDuration).UnixMilli()
	data := []byte(strconv.FormatInt(expires, 10) + "\n")
	if err := os.WriteFile(a.serviceStopLeasePath(), data, 0o600); err != nil {
		return fmt.Errorf("write service stop lease: %w", err)
	}
	a.serviceStopMu.Lock()
	a.serviceStopLease = data
	a.serviceStopMu.Unlock()
	return nil
}

// ServiceStopLease returns the bytes of the lease this process last wrote, or
// nil if it has not requested a stop. Detached helpers that outlive the
// process use it to make their own cleanup conditional.
func (a *App) ServiceStopLease() []byte {
	a.serviceStopMu.Lock()
	defer a.serviceStopMu.Unlock()
	return append([]byte(nil), a.serviceStopLease...)
}

// ReleaseServiceStopRequest retires the lease this process wrote, and only
// that one. A stop controller runs this after the service is down; by then a
// different controller may already have started a new service and asked it to
// stop, and tombstoning blindly would silently downgrade that request to the
// Task Scheduler kill fallback. Leaving a lease we did not write is always
// safe: it expires on its own, and every start path clears unconditionally
// before launching.
func (a *App) ReleaseServiceStopRequest() error {
	a.serviceStopMu.Lock()
	owned := a.serviceStopLease
	a.serviceStopLease = nil
	a.serviceStopMu.Unlock()
	if owned == nil {
		return nil
	}
	path := a.serviceStopLeasePath()
	current, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read service stop lease: %w", err)
	}
	if !bytes.Equal(current, owned) {
		return nil
	}
	if err := os.WriteFile(path, []byte(serviceStopTombstone), 0o600); err != nil {
		return fmt.Errorf("release service stop lease: %w", err)
	}
	return nil
}

// ClearServiceStopRequest tombstones whatever lease exists. Only start paths
// call it: no service is running yet, so any lease on disk is stale by
// definition and must not be allowed to kill the process about to launch.
func (a *App) ClearServiceStopRequest() error {
	path := a.serviceStopLeasePath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat service stop lease: %w", err)
	}
	if err := os.WriteFile(path, []byte(serviceStopTombstone), 0o600); err != nil {
		return fmt.Errorf("clear service stop lease: %w", err)
	}
	return nil
}

// RunServiceStopWatcher blocks as a service component until the Windows stop
// lease is observed or the service context ends. The service coordinator joins
// it before application cleanup begins.
func (a *App) RunServiceStopWatcher(ctx context.Context, cancel context.CancelFunc, ready func()) error {
	ticker := time.NewTicker(serviceStopPollInterval)
	defer ticker.Stop()

	var lastError string
	readySent := false
	for {
		active, err := readServiceStopLease(a.serviceStopLeasePath(), time.Now())
		if err != nil {
			if message := err.Error(); message != lastError {
				if a.Log != nil {
					a.Log.Warnf("failed to read service stop lease: %v", err)
				}
				lastError = message
			}
		} else {
			lastError = ""
			if active {
				if a.Log != nil {
					a.Log.Info("Service stop requested")
				}
				cancel()
				return nil
			}
			if !readySent {
				ready()
				readySent = true
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (a *App) serviceStopLeasePath() string {
	return a.Layout.ServiceStop
}

func readServiceStopLease(path string, now time.Time) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	expires, err := parseServiceStopLease(data)
	if err != nil {
		// A controller may be in the middle of replacing the contents. Leave
		// malformed data in place and retry on the next poll.
		return false, nil
	}
	if expires.After(now) {
		return true, nil
	}
	return false, nil
}

func parseServiceStopLease(data []byte) (time.Time, error) {
	value := strings.TrimSpace(string(data))
	expires, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid expiry %q: %w", value, err)
	}
	return time.UnixMilli(expires), nil
}
