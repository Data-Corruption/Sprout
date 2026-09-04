package maintenance

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"sprout/internal/layout"
	"sprout/pkg/xsyscall"
)

const (
	LifecycleLockTimeout = 5 * time.Minute
	LifecycleLockPoll    = 100 * time.Millisecond
)

// GuardOptions selects normal application or installer-authorized migrator
// behavior. A normal guard holds a shared lifecycle lease and publishes its
// PID until Close. A migrator validates state and the installer nonce but does
// not acquire the shared lease already excluded by its parent installer.
type GuardOptions struct {
	Version  string
	Migrator bool
	DevMode  bool
}

// Guard is one process's maintenance lease.
type Guard struct {
	cancel context.CancelFunc
	state  State
	lock   *xsyscall.Lock
	marker string
	done   <-chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// OpenGuard validates lifecycle state and establishes the process lease. Its
// returned context is always derived from ctx and is also cancelled if the
// atomic lifecycle state leaves ready or changes version/epoch while the
// guard is open.
func OpenGuard(
	ctx context.Context,
	l layout.Layout,
	opts GuardOptions,
) (context.Context, *Guard, error) {
	guardCtx, cancel := context.WithCancel(ctx)
	fail := func(err error) (context.Context, *Guard, error) {
		cancel()
		return guardCtx, nil, err
	}
	if err := guardCtx.Err(); err != nil {
		return fail(err)
	}
	if opts.Version == "" {
		return fail(fmt.Errorf("running version is empty"))
	}

	if opts.Migrator {
		state, err := authorizeMigrator(l, opts.Version)
		if err != nil {
			return fail(err)
		}
		return guardCtx, &Guard{cancel: cancel, state: state}, nil
	}

	if opts.DevMode {
		if _, err := EnsureDevReady(l, opts.Version); err != nil {
			return fail(fmt.Errorf("prepare development lifecycle state: %w", err))
		}
	}
	before, err := requireReady(l, opts.Version, "before lifecycle lock")
	if err != nil {
		return fail(err)
	}

	lock, err := xsyscall.AcquireLock(guardCtx, l.LifecycleLock, xsyscall.LockOptions{
		Mode:    xsyscall.ModeShared,
		Timeout: LifecycleLockTimeout,
		Poll:    LifecycleLockPoll,
	})
	if err != nil {
		return fail(fmt.Errorf("acquire shared lifecycle lock: %w", err))
	}
	guard := &Guard{cancel: cancel, state: before, lock: lock}
	failGuard := func(err error) (context.Context, *Guard, error) {
		_ = guard.Close()
		return guardCtx, nil, err
	}

	// Publish the marker before the post-lock state check. A controller that
	// transitions in this narrow window will either see and drain the marker,
	// or the check below will fail and Close will remove it immediately. Doing
	// this after the check would let a controller complete an empty marker scan
	// and then wait behind an unmarked shared-lock holder.
	marker := filepath.Join(l.Instances, strconv.Itoa(os.Getpid()))
	markerFile, err := xsyscall.OpenNoFollow(marker, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return failGuard(fmt.Errorf("create instance marker: %w", err))
	}
	if err := markerFile.Close(); err != nil {
		_ = os.Remove(marker)
		return failGuard(fmt.Errorf("close instance marker: %w", err))
	}
	guard.marker = marker

	after, err := requireReady(l, opts.Version, "after lifecycle lock")
	if err != nil {
		return failGuard(err)
	}
	if err := (Expectation{InstallationEpoch: before.InstallationEpoch}).Check(after); err != nil {
		return failGuard(fmt.Errorf("installation changed while acquiring lifecycle lock: %w", err))
	}
	guard.state = after
	guard.done = startStateWatcher(guardCtx, l, after, cancel)
	return guardCtx, guard, nil
}

// State returns the lifecycle snapshot captured under the guard.
func (g *Guard) State() State {
	if g == nil {
		return State{}
	}
	return g.state
}

// Close cancels and joins the state watcher, removes the PID marker, and
// releases the shared lifecycle lock. It is safe to call repeatedly.
func (g *Guard) Close() error {
	if g == nil {
		return nil
	}
	g.closeOnce.Do(func() {
		g.cancel()
		if g.done != nil {
			<-g.done
		}
		var markerErr error
		if g.marker != "" {
			if err := os.Remove(g.marker); err != nil && !errors.Is(err, os.ErrNotExist) {
				markerErr = fmt.Errorf("remove instance marker: %w", err)
			}
		}
		var lockErr error
		if g.lock != nil {
			lockErr = g.lock.Close()
		}
		g.closeErr = errors.Join(markerErr, lockErr)
	})
	return g.closeErr
}

func requireReady(l layout.Layout, version, when string) (State, error) {
	state, err := ReadState(l)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, fmt.Errorf("maintenance state is missing %s; rerun the installer", when)
	}
	if err != nil {
		return State{}, fmt.Errorf("read maintenance state %s: %w", when, err)
	}
	if err := (Expectation{Phase: PhaseReady, Version: version}).Check(state); err != nil {
		return State{}, fmt.Errorf("application is not ready %s: %w", when, err)
	}
	return state, nil
}

func authorizeMigrator(l layout.Layout, version string) (State, error) {
	state, err := ReadState(l)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, fmt.Errorf("--migrate requires installer maintenance state")
	}
	if err != nil {
		return State{}, fmt.Errorf("read installer maintenance state: %w", err)
	}
	if state.Phase != PhaseInstalling && state.Phase != PhaseUpdating {
		return State{}, fmt.Errorf("--migrate requires installing or updating state, got %q", state.Phase)
	}
	if err := (Expectation{TargetVersion: version}).Check(state); err != nil {
		return State{}, fmt.Errorf("authorize migration: %w", err)
	}
	provided := os.Getenv(NonceEnv)
	if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(state.Nonce)) != 1 {
		return State{}, fmt.Errorf("--migrate requires the matching installer nonce")
	}
	return state, nil
}
