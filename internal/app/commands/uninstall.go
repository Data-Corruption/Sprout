package commands

import (
	"context"
	"fmt"
	"sprout/internal/app"
	"sprout/internal/maintenance"

	"sprout/pkg/xterm/prompt"

	"github.com/urfave/cli/v3"
)

func uninstallCommand(a *app.App) *cli.Command {
	return &cli.Command{
		Name:  "uninstall",
		Usage: "uninstall the app",
		Action: func(ctx context.Context, _ *cli.Command) error {
			// confirmation
			msg := fmt.Sprintf("Are you sure you want to uninstall %s? This will delete all data and the application binary.", a.BuildInfo().Name)
			if yes, err := prompt.YesNo(msg); err != nil {
				return fmt.Errorf("prompt failed: %w", err)
			} else if !yes {
				fmt.Println("Uninstall cancelled.")
				return nil
			}

			logPath, err := a.StartMaintenance(ctx, maintenance.ActionUninstall)
			if err != nil {
				return fmt.Errorf("start uninstall maintenance: %w", err)
			}
			fmt.Printf("Uninstall accepted. To view the resulting uninstallation logs see: %s\n", logPath)
			return nil
		},
	}
}
