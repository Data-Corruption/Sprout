// --- FILE service ---

package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"sprout/internal/app"
	"sprout/internal/platform/database/hashrequests"
)

const (
	workerPollInterval       = 250 * time.Millisecond
	workerCleanupInterval    = time.Minute
	completedRequestLifetime = time.Minute
)

// runWorker is the service customization point. Replace this example loop
// with your application's long-running work: a bot, UDP server, scheduler,
// queue consumer, or another ordinary function that obeys ctx cancellation.
// scripts/test-lifecycle-e2e.sh can exercise this exact hash protocol as an
// SQLite IPC probe. Finalized forks start with TEST_EXAMPLE_HASH=false there;
// set it to true only if you keep this example. The other installer and
// service checks run either way.
func runWorker(ctx context.Context, a *app.App, ready func()) error {
	pollTicker := time.NewTicker(workerPollInterval)
	defer pollTicker.Stop()
	cleanupTicker := time.NewTicker(workerCleanupInterval)
	defer cleanupTicker.Stop()
	ready()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-pollTicker.C:
			request, err := hashrequests.NextPending(ctx, a.DB)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("poll hash request: %w", err)
			}
			if request != nil {
				sum := sha256.Sum256([]byte(request.Input))
				if err := hashrequests.Complete(ctx, a.DB, request.ID, hex.EncodeToString(sum[:])); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
			}
		case <-cleanupTicker.C:
			if err := hashrequests.Cleanup(ctx, a.DB, time.Now().Add(-completedRequestLifetime)); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}
