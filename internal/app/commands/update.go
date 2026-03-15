package commands

import (
	"context"
	"fmt"
	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/types"

	"github.com/urfave/cli/v3"
)

var Update = register(func(a *app.App) *cli.Command {
	return &cli.Command{
		Name:  "update",
		Usage: "update the app",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "notify",
				Usage: "toggle update notification",
			},
			&cli.BoolFlag{
				Name:  "check",
				Usage: "just check for updates",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			notify := cmd.Bool("notify")
			if notify {
				var updateNotifications bool
				if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
					cfg.UpdateNotifications = !cfg.UpdateNotifications
					updateNotifications = cfg.UpdateNotifications
					return nil
				}); err != nil {
					return fmt.Errorf("failed to update notification setting in config: %w", err)
				}
				// print status
				if updateNotifications {
					fmt.Println("Update notifications are now enabled.")
				} else {
					fmt.Println("Update notifications are now disabled.")
				}
				return nil
			}

			check := cmd.Bool("check")
			if check {
				if updateAvailable, err := a.CheckForUpdate(); err != nil {
					return fmt.Errorf("failed to check for updates: %w", err)
				} else if updateAvailable {
					fmt.Println("Update available! Run 'sprout update' to update to the latest version.")
				} else {
					fmt.Println("No updates available.")
				}
				return nil
			}

			return a.DeferUpdate()
		},
	}
})
