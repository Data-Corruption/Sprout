// Package xsyscall wraps the handful of OS primitives this project needs that
// the standard library does not expose portably. Today that is advisory
// file locking and symlink-refusing opens.
//
// Locks are acquired by polling a non-blocking primitive rather than blocking
// in a goroutine. A blocking flock raced against a timer has no way to
// abandon the wait: the timer fires, the caller closes the descriptor, and the
// parked goroutine is still holding a reference to a file it no longer owns.
// Polling costs a wakeup every Poll interval and buys honest cancellation.
package xsyscall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// ErrLocked reports that a conflicting lock is held. Acquisition attempts that
// exhaust their timeout, and untimed attempts that find the file contended,
// both return it.
var ErrLocked = errors.New("file is locked by another process")

// Mode selects the kind of lock to take.
type Mode int

const (
	// ModeShared allows other shared holders and excludes exclusive ones.
	ModeShared Mode = iota
	// ModeExclusive excludes every other holder.
	ModeExclusive
)

func (m Mode) String() string {
	if m == ModeExclusive {
		return "exclusive"
	}
	return "shared"
}

// DefaultPoll is the retry interval used when LockOptions.Poll is zero.
const DefaultPoll = 100 * time.Millisecond

// LockOptions configures AcquireLock.
type LockOptions struct {
	// Mode is the lock kind. The zero value is ModeShared.
	Mode Mode
	// Timeout bounds how long to keep retrying. Zero means try exactly once
	// and return ErrLocked if the file is contended. Every caller should pick
	// a bound: an unbounded wait on a startup path is a hang, not an error.
	Timeout time.Duration
	// Poll is the retry interval. Zero means DefaultPoll.
	Poll time.Duration
	// Perm is the mode for the lock file when it has to be created. Zero means
	// 0o600, which is what every caller here wants.
	Perm os.FileMode
}

// Lock is a held OS-level file lock. Close releases it and is safe to call
// more than once; the operating system also releases the lock when the process
// exits or the descriptor is closed for any other reason.
type Lock struct {
	file *os.File
	once sync.Once
	err  error
	// state carries whatever the platform needs at unlock time. Windows must
	// hand UnlockFileEx the same OVERLAPPED it locked with; Unix ignores it.
	state unlockState
}

// AcquireLock opens path (creating it if needed, never following a symlink)
// and takes the requested lock on it.
//
// It returns ErrLocked when the lock is held elsewhere and opts.Timeout
// elapses, and ctx.Err() when ctx is cancelled first. Any other error is the
// underlying open or lock failure.
func AcquireLock(ctx context.Context, path string, opts LockOptions) (*Lock, error) {
	if opts.Mode != ModeShared && opts.Mode != ModeExclusive {
		return nil, fmt.Errorf("invalid lock mode %d", opts.Mode)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	perm := opts.Perm
	if perm == 0 {
		perm = 0o600
	}
	file, err := OpenNoFollow(path, os.O_CREATE|os.O_RDWR, perm)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect lock file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("lock file %s is not a regular file", path)
	}

	poll := opts.Poll
	if poll <= 0 {
		poll = DefaultPoll
	}
	var deadline time.Time
	if opts.Timeout > 0 {
		deadline = time.Now().Add(opts.Timeout)
	}

	for attempt := 0; ; attempt++ {
		if attempt != 0 {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return nil, err
			}
			if !time.Now().Before(deadline) {
				_ = file.Close()
				return nil, fmt.Errorf(
					"acquire %s lock on %s after %v: %w",
					opts.Mode,
					path,
					opts.Timeout,
					ErrLocked,
				)
			}
		}

		state, err := tryLock(file, opts.Mode)
		if err == nil {
			return &Lock{file: file, state: state}, nil
		}
		if !errors.Is(err, ErrLocked) {
			_ = file.Close()
			return nil, fmt.Errorf("acquire %s lock on %s: %w", opts.Mode, path, err)
		}
		if opts.Timeout <= 0 {
			_ = file.Close()
			return nil, ErrLocked
		}
		wait := min(poll, time.Until(deadline))
		if wait <= 0 {
			_ = file.Close()
			return nil, fmt.Errorf(
				"acquire %s lock on %s after %v: %w",
				opts.Mode,
				path,
				opts.Timeout,
				ErrLocked,
			)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Close releases the lock and closes the underlying file. Repeat calls return
// the first result.
func (l *Lock) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		unlockErr := unlock(l.file, l.state)
		closeErr := l.file.Close()
		switch {
		case unlockErr != nil:
			l.err = fmt.Errorf("release lock on %s: %w", l.file.Name(), unlockErr)
		case closeErr != nil:
			l.err = fmt.Errorf("close lock file %s: %w", l.file.Name(), closeErr)
		}
	})
	return l.err
}

// Name returns the path the lock is held on.
func (l *Lock) Name() string {
	if l == nil {
		return ""
	}
	return l.file.Name()
}
