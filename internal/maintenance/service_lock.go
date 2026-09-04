package maintenance

import (
	"context"
	"errors"

	"sprout/internal/layout"
	"sprout/pkg/xsyscall"
)

var ErrServiceAlreadyRunning = errors.New("service already running")

// AcquireServiceLock takes the nonblocking singleton lease for service run.
func AcquireServiceLock(l layout.Layout) (*xsyscall.Lock, error) {
	lock, err := xsyscall.AcquireLock(
		context.Background(),
		l.ServiceLock,
		xsyscall.LockOptions{Mode: xsyscall.ModeExclusive},
	)
	if errors.Is(err, xsyscall.ErrLocked) {
		return nil, ErrServiceAlreadyRunning
	}
	return lock, err
}
