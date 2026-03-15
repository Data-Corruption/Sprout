package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sprout/internal/app"
	"sprout/pkg/x"
	"time"

	"github.com/Data-Corruption/stdx/xterm/prompt"
	"github.com/urfave/cli/v3"
)

var Uninstall = register(func(a *app.App) *cli.Command {
	return &cli.Command{
		Name:  "uninstall",
		Usage: "uninstall the app",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// confirmation
			msg := fmt.Sprintf("Are you sure you want to uninstall %s? This will delete all data and the application binary.", a.BuildInfo().Name)
			if yes, err := prompt.YesNo(msg); err != nil {
				return fmt.Errorf("prompt failed: %w", err)
			} else if !yes {
				fmt.Println("Uninstall cancelled.")
				return nil
			}

			// prepare paths
			serviceName := a.BuildInfo().Name + ".service"
			home, err := x.GetUserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get user home dir: %w", err)
			}
			serviceFile := filepath.Join(home, ".config/systemd/user", serviceName)
			storagePath := a.StorageDir
			binPath, err := getBinPath()
			if err != nil {
				return fmt.Errorf("failed to get executable path: %w", err)
			}

			fmt.Println("Uninstalling...")

			// schedule cleanup
			a.SetPostCleanup(func() error {
				// stop / disable service
				if a.BuildInfo().ServiceEnabled {
					fmt.Println("Stopping service...")
					ctxStop, cancelStop := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancelStop()
					_ = exec.CommandContext(ctxStop, "systemctl", "--user", "stop", serviceName).Run()

					fmt.Println("Disabling service...")
					ctxDisable, cancelDisable := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancelDisable()
					_ = exec.CommandContext(ctxDisable, "systemctl", "--user", "disable", serviceName).Run()

					// remove service file
					if _, err := os.Stat(serviceFile); err == nil {
						fmt.Printf("Removing service file: %s\n", serviceFile)
						if err := os.Remove(serviceFile); err != nil {
							fmt.Printf("Failed to remove service file: %v\n", err)
						}
					}

					// reload daemon
					ctxReload, cancelReload := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancelReload()
					_ = exec.CommandContext(ctxReload, "systemctl", "--user", "daemon-reload").Run()
				}

				// remove storage
				if storagePath != "" {
					fmt.Printf("Removing storage directory: %s\n", storagePath)
					if err := os.RemoveAll(storagePath); err != nil {
						fmt.Printf("Failed to remove storage directory: %v\n", err)
					}
				}

				// remove binary
				fmt.Printf("Removing binary: %s\n", binPath)
				if err := os.Remove(binPath); err != nil {
					fmt.Printf("Failed to remove binary: %v\n", err)
					// if we can't remove it (e.g. running), we might need to try a different approach or warn.
					// but usually on Linux you can unlink a running binary.
				}

				fmt.Println("Uninstall complete.")
				return nil
			})

			return nil
		},
	}
})

func getBinPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}
