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
	usage := "check for updates"
	// --- BEGIN update.apply ---
	usage = "check for and apply updates"
	// --- END update.apply ---
	return &cli.Command{
		Name:  "update",
		Usage: usage,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "notify",
				Usage: "show update notices (--notify=false to hide them)",
				Value: true,
			},
			&cli.BoolFlag{Name: "background", Value: true, Usage: "check periodically (--background=false to disable)"},
			// --- BEGIN update.apply.auto ---
			&cli.BoolFlag{Name: "automatic", Usage: "apply updates unattended from the service (--automatic=false to disable)"},
			// --- END update.apply.auto ---
			// --- BEGIN update.apply ---
			&cli.BoolFlag{
				Name:    "yes",
				Aliases: []string{"y"},
				Usage:   "apply an available update without prompting",
			},
			// --- END update.apply ---
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			settingsChanged := cmd.IsSet("notify") || cmd.IsSet("background")
			// --- BEGIN update.apply.auto ---
			settingsChanged = settingsChanged || cmd.IsSet("automatic")
			// --- END update.apply.auto ---
			if settingsChanged {
				if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
					if cmd.IsSet("notify") {
						cfg.UpdateNotifications = cmd.Bool("notify")
					}
					if cmd.IsSet("background") {
						cfg.BackgroundUpdateChecks = cmd.Bool("background")
					}
					// --- BEGIN update.apply.auto ---
					if cmd.IsSet("automatic") {
						cfg.AutomaticUpdates = cmd.Bool("automatic")
						if cfg.AutomaticUpdates && !cmd.IsSet("background") {
							cfg.BackgroundUpdateChecks = true
						}
					}
					// --- END update.apply.auto ---
					return nil
				}); err != nil {
					return fmt.Errorf("save update preferences: %w", err)
				}
				fmt.Println("Update preferences saved.")
				return nil
			}

			checkForUpdate := a.CheckForUpdate

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

			applyHandled := false
			// --- BEGIN update.apply ---
			applyHandled = true
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
			// --- END update.apply ---
			if applyHandled {
				return nil
			}

			fmt.Printf("An update is available. %s\n", app.UpdateGuidance)
			return nil
		},
	}
}

// --- BEGIN update.apply ---
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

// --- END update.apply ---
