// --- FILE service ---

package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"sprout/internal/app"
	"sprout/internal/platform/database/hashrequests"

	"github.com/urfave/cli/v3"
)

const (
	hashWaitTimeout  = 10 * time.Second
	hashPollInterval = 100 * time.Millisecond
)

func hashCommand(a *app.App) *cli.Command {
	return &cli.Command{
		Name:      "hash",
		Usage:     "ask the service worker to compute an example SHA-256",
		ArgsUsage: "<text>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
			if len(args) == 0 {
				return fmt.Errorf("text is required")
			}
			text := strings.Join(args, " ")
			fmt.Fprintln(os.Stderr, "Warning: command-line text is visible in shell history and process listings; this SHA-256 example is not password hashing.")

			waitCtx, cancel := context.WithTimeout(ctx, hashWaitTimeout)
			defer cancel()
			id, err := hashrequests.Create(waitCtx, a.DB, text, time.Now().Add(hashWaitTimeout))
			if err != nil {
				return err
			}
			defer func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
				defer cleanupCancel()
				if err := hashrequests.Delete(cleanupCtx, a.DB, id); err != nil && a.Log != nil {
					a.Log.Warnf("failed to clean hash request %d: %v", id, err)
				}
			}()

			result, err := waitForHash(waitCtx, a, id)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, hashrequests.ErrRequestGone) {
					return fmt.Errorf("service did not respond within %s; run %q to check it",
						hashWaitTimeout, a.BuildInfo().Name+" service status")
				}
				return err
			}
			fmt.Printf("hello from service, here is your SHA-256: %s\n", result)
			return nil
		},
	}
}

func waitForHash(ctx context.Context, a *app.App, id int64) (string, error) {
	ticker := time.NewTicker(hashPollInterval)
	defer ticker.Stop()

	for {
		result, ready, err := hashrequests.Result(ctx, a.DB, id)
		if err != nil {
			return "", err
		}
		if ready {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}
