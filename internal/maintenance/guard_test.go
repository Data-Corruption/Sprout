package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sprout/pkg/xsyscall"
)

func TestNormalGuardHoldsLifecycleLockAndMarker(t *testing.T) {
	l := testLayout(t)
	state := readyState()
	if err := WriteState(l, state); err != nil {
		t.Fatalf("write ready state: %v", err)
	}
	ctx, guard, err := OpenGuard(context.Background(), l, GuardOptions{Version: state.Version})
	if err != nil {
		t.Fatalf("open guard: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("guard context already cancelled: %v", ctx.Err())
	}
	if guard.State() != state {
		t.Fatalf("guard state = %#v, want %#v", guard.State(), state)
	}
	marker := filepath.Join(l.Instances, strconv.Itoa(os.Getpid()))
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("instance marker missing: %v", err)
	}
	contender, err := xsyscall.AcquireLock(context.Background(), l.LifecycleLock, xsyscall.LockOptions{Mode: xsyscall.ModeExclusive})
	if contender != nil {
		_ = contender.Close()
		t.Fatal("exclusive lifecycle lock acquired while guard was open")
	}
	if !errors.Is(err, xsyscall.ErrLocked) {
		t.Fatalf("exclusive contender error = %v, want ErrLocked", err)
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("close guard: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("closing guard did not cancel derived context")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker remains after Close: %v", err)
	}
	contender, err = xsyscall.AcquireLock(context.Background(), l.LifecycleLock, xsyscall.LockOptions{Mode: xsyscall.ModeExclusive})
	if err != nil {
		t.Fatalf("acquire lock after Close: %v", err)
	}
	_ = contender.Close()
	if err := guard.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestStateWatcherCancelsAndCloseJoins(t *testing.T) {
	l := testLayout(t)
	state := readyState()
	if err := WriteState(l, state); err != nil {
		t.Fatalf("write ready state: %v", err)
	}
	ctx, guard, err := OpenGuard(context.Background(), l, GuardOptions{Version: state.Version})
	if err != nil {
		t.Fatalf("open guard: %v", err)
	}
	state.Phase = PhaseUninstalling
	state.Nonce = ""
	state.ChangedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := WriteState(l, state); err != nil {
		t.Fatalf("write uninstalling state: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("state watcher did not cancel context")
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("close guard: %v", err)
	}
}

func TestNormalGuardFailsClosedOutsideReady(t *testing.T) {
	l := testLayout(t)
	state := State{
		Phase:             PhaseUpdating,
		Version:           "v1.2.3",
		TargetVersion:     "v1.2.4",
		Nonce:             testNonce,
		ChangedAt:         "2026-08-28T17:00:00Z",
		InstallationEpoch: testNonce,
	}
	if err := WriteState(l, state); err != nil {
		t.Fatalf("write updating state: %v", err)
	}
	ctx, guard, err := OpenGuard(context.Background(), l, GuardOptions{Version: state.Version})
	if guard != nil {
		_ = guard.Close()
		t.Fatal("guard returned for transitional state")
	}
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("OpenGuard error = %v, want not-ready failure", err)
	}
	if ctx.Err() == nil {
		t.Fatal("failed guard's derived context was not cancelled")
	}
}

func TestMigratorValidatesStateAndSkipsSharedLock(t *testing.T) {
	l := testLayout(t)
	state := State{
		Phase:             PhaseUpdating,
		Version:           "v1.2.3",
		TargetVersion:     "v1.2.4",
		Nonce:             testNonce,
		ChangedAt:         "2026-08-28T17:00:00Z",
		InstallationEpoch: testNonce,
	}
	if err := WriteState(l, state); err != nil {
		t.Fatalf("write updating state: %v", err)
	}
	exclusive, err := xsyscall.AcquireLock(context.Background(), l.LifecycleLock, xsyscall.LockOptions{Mode: xsyscall.ModeExclusive})
	if err != nil {
		t.Fatalf("take installer lifecycle lock: %v", err)
	}
	defer exclusive.Close()

	if _, guard, err := OpenGuard(context.Background(), l, GuardOptions{Version: state.TargetVersion, Migrator: true}); err == nil {
		_ = guard.Close()
		t.Fatal("migrator accepted missing nonce")
	}
	t.Setenv(NonceEnv, testNonce)
	_, guard, err := OpenGuard(context.Background(), l, GuardOptions{Version: state.TargetVersion, Migrator: true})
	if err != nil {
		t.Fatalf("open authorized migrator guard: %v", err)
	}
	if guard.State() != state {
		t.Fatalf("migrator state = %#v, want %#v", guard.State(), state)
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("close migrator guard: %v", err)
	}
}

func TestDevGuardBootstrapsReadyState(t *testing.T) {
	l := testLayout(t)
	_, guard, err := OpenGuard(context.Background(), l, GuardOptions{Version: "v1.2.3", DevMode: true})
	if err != nil {
		t.Fatalf("open dev guard: %v", err)
	}
	defer guard.Close()
	state, err := ReadState(l)
	if err != nil {
		t.Fatalf("read bootstrapped state: %v", err)
	}
	if state.Phase != PhaseReady || state.Version != "v1.2.3" {
		t.Fatalf("bootstrapped state = %#v", state)
	}
}

func TestServiceLockIsSingleton(t *testing.T) {
	l := testLayout(t)
	first, err := AcquireServiceLock(l)
	if err != nil {
		t.Fatalf("acquire first service lock: %v", err)
	}
	defer first.Close()
	second, err := AcquireServiceLock(l)
	if second != nil {
		_ = second.Close()
		t.Fatal("second service lock acquired")
	}
	if !errors.Is(err, ErrServiceAlreadyRunning) {
		t.Fatalf("second lock error = %v, want ErrServiceAlreadyRunning", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first lock: %v", err)
	}
	second, err = AcquireServiceLock(l)
	if err != nil {
		t.Fatalf("acquire after close: %v", err)
	}
	_ = second.Close()
}
