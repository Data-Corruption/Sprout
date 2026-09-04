//go:build !windows

// --- FILE service ---

package app

import "context"

// RunServiceStopWatcher has no lease to observe on Unix. It stays blocked as a
// joined service component until the root process context is canceled.
func (a *App) RunServiceStopWatcher(ctx context.Context, _ context.CancelFunc, ready func()) error {
	ready()
	<-ctx.Done()
	return nil
}
