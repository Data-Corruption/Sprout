package maintenance

import (
	"context"
	"time"

	"sprout/internal/layout"
)

const statePollInterval = 250 * time.Millisecond

// startStateWatcher cancels the guard context as soon as the durable state
// stops describing the installation this process started under: the phase
// leaves ready, the version changes, or the epoch changes.
//
// Installers publish the transitional state before they stop or signal
// anything, so this is the first thing a running process can observe about an
// install, update, or uninstall. On Windows it is the only cooperative stop
// signal a controller has. On Unix the installer also sends SIGTERM to every
// marked process, which reaches the same cancellation; the watcher runs there
// too so both platforms drain through one code path and a process that missed
// its signal still steps aside instead of holding the shared lease for the
// full lock timeout.
//
// An unreadable state file is treated like a mismatch. The installer owns that
// file and replaces it atomically, so a read failure means the installation is
// being changed under us or the tree is damaged; either way, continuing is
// wrong.
func startStateWatcher(
	ctx context.Context,
	l layout.Layout,
	expected State,
	cancel context.CancelFunc,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(statePollInterval)
		defer ticker.Stop()
		want := Expectation{
			Phase:             PhaseReady,
			Version:           expected.Version,
			InstallationEpoch: expected.InstallationEpoch,
		}
		for {
			state, err := ReadState(l)
			if err != nil || want.Check(state) != nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}
