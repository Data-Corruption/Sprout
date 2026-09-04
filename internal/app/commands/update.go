// --- FILE update ---

package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"sprout/internal/app"
	"sprout/internal/maintenance"
	"sprout/internal/platform/database/config"
	"sprout/internal/types"
	"sprout/pkg/xterm/prompt"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

func updateCommand(a *app.App) *cli.Command {
	return &cli.Command{
		Name:  "update",
		Usage: "check for and apply updates",
		Flags: []cli.Flag{
			// --- BEGIN update.notifications ---
			&cli.BoolFlag{
				Name:  "notify",
				Usage: "toggle update notifications",
			},
			// --- END update.notifications ---
			// --- BEGIN update.self ---
			&cli.BoolFlag{
				Name:    "yes",
				Aliases: []string{"y"},
				Usage:   "apply an available update without prompting",
			},
			// --- END update.self ---
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// --- BEGIN update.notifications ---
			if cmd.Bool("notify") {
				var updateNotifications bool
				if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
					cfg.UpdateNotifications = !cfg.UpdateNotifications
					updateNotifications = cfg.UpdateNotifications
					return nil
				}); err != nil {
					return fmt.Errorf("update notification setting: %w", err)
				}
				if updateNotifications {
					fmt.Println("Update notifications are now enabled.")
				} else {
					fmt.Println("Update notifications are now disabled.")
				}
				return nil
			}
			// --- END update.notifications ---

			checkForUpdate := a.CheckForUpdate
			// --- BEGIN update.notifications ---
			checkForUpdate = a.CheckForUpdateAndNotify
			// --- END update.notifications ---

			updateAvailable, err := checkForUpdate(ctx)
			if err != nil {
				if errors.Is(err, app.ErrUpdatesDisabled) {
					fmt.Printf("This installation does not manage updates. %s\n", app.UpdateGuidance)
					return nil
				}
				return fmt.Errorf("check for updates: %w", err)
			}
			if !updateAvailable {
				fmt.Println("Already running the latest version.")
				return nil
			}

			selfUpdateHandled := false
			// --- BEGIN update.self ---
			selfUpdateHandled = true
			if err := applyAvailableUpdate(
				cmd.Bool("yes"),
				term.IsTerminal(int(os.Stdin.Fd())),
				prompt.YesNo,
				func() (string, error) {
					return a.StartMaintenance(ctx, maintenance.ActionUpdate)
				},
				os.Stdout,
			); err != nil {
				return err
			}
			// --- END update.self ---
			if selfUpdateHandled {
				return nil
			}

			fmt.Printf("An update is available. %s\n", app.UpdateGuidance)
			return nil
		},
	}
}

// --- BEGIN update.self ---
func applyAvailableUpdate(
	yes bool,
	interactive bool,
	ask func(string) (bool, error),
	schedule func() (string, error),
	output io.Writer,
) error {
	if !yes {
		if !interactive {
			return fmt.Errorf("update confirmation requires interactive input; use --yes")
		}
		confirmed, err := ask("Apply the available update?")
		if err != nil {
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("update confirmation requires interactive input; use --yes")
			}
			return fmt.Errorf("read update confirmation: %w", err)
		}
		if !confirmed {
			fmt.Fprintln(output, "Update declined.")
			return nil
		}
	}

	logPath, err := schedule()
	if err != nil {
		if errors.Is(err, app.ErrUpdatesDisabled) {
			fmt.Fprintf(output, "The update cannot be applied by this installation. %s\n", app.UpdateGuidance)
			return nil
		}
		return fmt.Errorf("start self-update: %w", err)
	}
	fmt.Fprintf(output, "Update accepted and will now start in the background.\nIt may take a few moments.\nTo view the update logs see: %s\n", logPath)
	return nil
}

// --- END update.self ---
