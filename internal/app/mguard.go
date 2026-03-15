package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

const (
	LockAcquireTimeout = 5 * time.Minute
	LockFileName       = "migrate.lock"
	InstancesDir       = "instances"
)

// mguard sets up the migration guard for the application. It performs the following:
// - Creates (if not exists) and acquires a shared lock on the lock file to prevent concurrent migrations.
// - Writes the process PID to the instances directory to allow the installer/updater to signal shutdown.
// It returns a cleanup function to be called on application exit.
//
// The installation/update script shuts down all running instances by reading PIDs from the instances
// directory and sending SIGTERM. Except the service instance, which is stopped via systemctl. It then
// attempts to acquire an exclusive lock on the lock file with a timeout. If successful, it proceeds
// with the migration, releases the lock, and restarts the service, etc.
func (a *App) mguard() error {
	// ensure dirs exists
	if err := os.MkdirAll(filepath.Join(a.RuntimeDir, InstancesDir), 0o755); err != nil {
		return err
	}

	// create/open lock file
	lockPath := filepath.Join(a.RuntimeDir, LockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}

	// acquire shared lock with timeout
	done := make(chan error, 1)
	go func() {
		done <- unix.Flock(int(f.Fd()), unix.LOCK_SH)
	}()
	select {
	case err := <-done:
		if err != nil {
			_ = f.Close()
			return err
		}
	case <-time.After(LockAcquireTimeout):
		_ = f.Close()
		return fmt.Errorf("timeout acquiring shared lock after %v", LockAcquireTimeout)
	}

	// write PID file for installer to signal shutdown
	pidPath := filepath.Join(a.RuntimeDir, InstancesDir, strconv.Itoa(os.Getpid()))
	pidFile, err := os.OpenFile(pidPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = f.Close()
		return err
	}
	_ = pidFile.Close() // file just needs to exist

	a.AddCleanup(func() error {
		_ = os.Remove(pidPath)
		return f.Close() // release shared lock
	})

	return nil
}
