package xsyscall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const holderHelperEnv = "SPROUT_XSYSCALL_HELPER"

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.lock")
}

func TestExclusiveLockExcludes(t *testing.T) {
	path := lockPath(t)
	held, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeExclusive})
	if err != nil {
		t.Fatalf("acquire first exclusive lock: %v", err)
	}
	defer held.Close()

	if _, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeExclusive}); !errors.Is(err, ErrLocked) {
		t.Fatalf("second exclusive lock error = %v, want ErrLocked", err)
	}
	if _, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeShared}); !errors.Is(err, ErrLocked) {
		t.Fatalf("shared lock against exclusive holder error = %v, want ErrLocked", err)
	}
}

func TestSharedLocksCoexist(t *testing.T) {
	path := lockPath(t)
	first, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeShared})
	if err != nil {
		t.Fatalf("acquire first shared lock: %v", err)
	}
	defer first.Close()
	second, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeShared})
	if err != nil {
		t.Fatalf("acquire second shared lock: %v", err)
	}
	defer second.Close()

	if _, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeExclusive}); !errors.Is(err, ErrLocked) {
		t.Fatalf("exclusive lock against shared holders error = %v, want ErrLocked", err)
	}
}

func TestLockReleasedOnClose(t *testing.T) {
	path := lockPath(t)
	held, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeExclusive})
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("close lock: %v", err)
	}
	next, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeExclusive})
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatalf("close reacquired lock: %v", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	held, err := AcquireLock(context.Background(), lockPath(t), LockOptions{Mode: ModeExclusive})
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if err := (*Lock)(nil).Close(); err != nil {
		t.Fatalf("close nil lock: %v", err)
	}
}

func TestTimeoutIsHonored(t *testing.T) {
	path := lockPath(t)
	held, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeExclusive})
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer held.Close()

	const timeout = 250 * time.Millisecond
	start := time.Now()
	_, err = AcquireLock(context.Background(), path, LockOptions{
		Mode:    ModeExclusive,
		Timeout: timeout,
		Poll:    20 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("timed-out acquisition error = %v, want ErrLocked", err)
	}
	if elapsed < timeout {
		t.Fatalf("gave up after %v, want at least %v", elapsed, timeout)
	}
	if elapsed > 10*timeout {
		t.Fatalf("gave up after %v, far past the %v timeout", elapsed, timeout)
	}
}

func TestTimeoutDoesNotWaitForPollOrAcquireLate(t *testing.T) {
	path := lockPath(t)
	held, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeExclusive})
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer held.Close()

	release := time.AfterFunc(100*time.Millisecond, func() {
		_ = held.Close()
	})
	defer release.Stop()

	start := time.Now()
	late, err := AcquireLock(context.Background(), path, LockOptions{
		Mode:    ModeExclusive,
		Timeout: 20 * time.Millisecond,
		Poll:    time.Second,
	})
	if late != nil {
		_ = late.Close()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("timed-out acquisition error = %v, want ErrLocked", err)
	}
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Fatalf("timeout followed the %v poll interval; returned after %v", time.Second, elapsed)
	}
}

func TestContextCancellationStopsWaiting(t *testing.T) {
	path := lockPath(t)
	held, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeExclusive})
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer held.Close()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err = AcquireLock(ctx, path, LockOptions{
		Mode:    ModeExclusive,
		Timeout: time.Minute,
		Poll:    10 * time.Millisecond,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquisition error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("cancellation took %v to take effect", elapsed)
	}
}

func TestCancelledContextDoesNotAcquire(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := lockPath(t)

	held, err := AcquireLock(ctx, path, LockOptions{Mode: ModeExclusive})
	if held != nil {
		_ = held.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("acquisition error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled acquisition created the lock file: %v", err)
	}
}

func TestAcquireLockRejectsInvalidMode(t *testing.T) {
	path := lockPath(t)
	held, err := AcquireLock(context.Background(), path, LockOptions{Mode: Mode(99)})
	if held != nil {
		_ = held.Close()
	}
	if err == nil {
		t.Fatal("AcquireLock accepted an invalid mode")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid acquisition created the lock file: %v", err)
	}
}

func TestAcquireLockCreatesFileWithRequestedPerm(t *testing.T) {
	path := lockPath(t)
	held, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeExclusive})
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer held.Close()

	if held.Name() != path {
		t.Fatalf("lock name = %q, want %q", held.Name(), path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestOpenNoFollowRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink test unavailable: %v", err)
	}
	if file, err := OpenNoFollow(link, os.O_RDWR, 0o600); err == nil {
		file.Close()
		t.Fatal("OpenNoFollow followed a symlink")
	}
}

func TestAcquireLockRejectsFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are unix-only")
	}
	path := filepath.Join(t.TempDir(), "lock.fifo")
	if err := exec.Command("mkfifo", path).Run(); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	lock, err := AcquireLock(context.Background(), path, LockOptions{Mode: ModeExclusive})
	if lock != nil {
		_ = lock.Close()
	}
	if err == nil {
		t.Fatal("AcquireLock accepted a FIFO")
	}
}

func TestAcquireLockRefusesSymlinkedLockFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.lock")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	link := filepath.Join(dir, "redirected.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink test unavailable: %v", err)
	}
	if held, err := AcquireLock(context.Background(), link, LockOptions{Mode: ModeExclusive}); err == nil {
		held.Close()
		t.Fatal("AcquireLock followed a symlinked lock path")
	}
}

// TestCrossProcessExclusion is the case the in-process tests cannot prove on
// their own: flock and LockFileEx are only interesting because they coordinate
// separate processes.
func TestCrossProcessExclusion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cross.lock")
	ready := filepath.Join(dir, "ready")
	release := filepath.Join(dir, "release")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	var output bytes.Buffer
	child := exec.CommandContext(ctx, executable, "-test.run=^TestCrossProcessExclusionHelper$")
	child.Env = append(os.Environ(),
		holderHelperEnv+"=1",
		"SPROUT_XSYSCALL_LOCK="+path,
		"SPROUT_XSYSCALL_READY="+ready,
		"SPROUT_XSYSCALL_RELEASE="+release,
	)
	child.Stdout = &output
	child.Stderr = &output
	if err := child.Start(); err != nil {
		t.Fatalf("start lock holder: %v", err)
	}
	defer func() {
		_ = os.WriteFile(release, nil, 0o600)
		_ = child.Wait()
	}()

	if err := waitForPath(ctx, ready); err != nil {
		t.Fatalf("wait for lock holder: %v\n%s", err, output.String())
	}
	if _, err := AcquireLock(ctx, path, LockOptions{Mode: ModeExclusive}); !errors.Is(err, ErrLocked) {
		t.Fatalf("acquisition against another process error = %v, want ErrLocked\n%s", err, output.String())
	}

	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("signal lock holder to release: %v", err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("lock holder failed: %v\n%s", err, output.String())
	}
	held, err := AcquireLock(ctx, path, LockOptions{Mode: ModeExclusive, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("acquire after holder exited: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("close lock: %v", err)
	}
}

func TestCrossProcessExclusionHelper(t *testing.T) {
	if os.Getenv(holderHelperEnv) != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	held, err := AcquireLock(ctx, os.Getenv("SPROUT_XSYSCALL_LOCK"), LockOptions{Mode: ModeExclusive})
	if err != nil {
		t.Fatalf("helper acquire lock: %v", err)
	}
	if err := os.WriteFile(os.Getenv("SPROUT_XSYSCALL_READY"), nil, 0o600); err != nil {
		t.Fatalf("helper signal readiness: %v", err)
	}
	if err := waitForPath(ctx, os.Getenv("SPROUT_XSYSCALL_RELEASE")); err != nil {
		t.Fatalf("helper wait for release: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("helper release lock: %v", err)
	}
}

func waitForPath(ctx context.Context, path string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
