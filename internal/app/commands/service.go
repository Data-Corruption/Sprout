package commands

import (
	"context"
	"fmt"
	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/http/router"
	"sprout/internal/platform/http/server"
	"sprout/internal/types"
	"time"

	"github.com/Data-Corruption/stdx/xnet"
	"github.com/urfave/cli/v3"
)

const (
	botShutdownTimeout = 10 * time.Second
)

var Service = register(func(a *app.App) *cli.Command {
	if !a.BuildInfo().ServiceEnabled {
		return nil
	}
	return &cli.Command{
		Name:  "service",
		Usage: "service management commands",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// get service name / env file path
			if a.BuildInfo().Name == "" || a.StorageDir == "" {
				return fmt.Errorf("app name or storage path not found")
			}
			serviceName := a.BuildInfo().Name + ".service"
			envFilePath := fmt.Sprintf("%s/%s.env", a.StorageDir, a.BuildInfo().Name)

			// print service management commands
			fmt.Printf("🖧 Service Cheat Sheet\n\n")
			fmt.Printf("    Status:  systemctl --user status %s\n", serviceName)
			fmt.Printf("    Enable:  systemctl --user enable %s\n", serviceName)
			fmt.Printf("    Disable: systemctl --user disable %s\n\n", serviceName)
			fmt.Printf("    Start:   systemctl --user start %s\n", serviceName)
			fmt.Printf("    Stop:    systemctl --user stop %s\n", serviceName)
			fmt.Printf("    Restart: systemctl --user restart %s\n\n", serviceName)
			fmt.Printf("    Reset:   systemctl --user reset-failed %s\n\n", serviceName)
			fmt.Printf("    Env:     edit %s then restart the service\n\n", envFilePath)
			fmt.Printf("    Logs:        journalctl --user -u %s -n 200 --no-pager\n", serviceName)
			fmt.Printf("    Stop Logs:   journalctl --user -u %s-stop* -n 200 --no-pager\n", serviceName)
			fmt.Printf("    Update Logs: journalctl --user -u %s-update* -n 200 -f\n", a.BuildInfo().Name)

			return nil
		},
		Commands: []*cli.Command{
			{
				Name:        "set",
				Description: "Set config values for bootstrapping server",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "log",
						Usage: "set log level (DEBUG, INFO, WARN, ERROR)",
					},
					&cli.IntFlag{
						Name:  "port",
						Usage: "set server port",
					},
					&cli.StringFlag{
						Name:  "host",
						Usage: "set server host (e.g., localhost, 0.0.0.0)",
					},
					&cli.IntFlag{
						Name:  "proxy",
						Usage: "set proxy port (0 = no proxy)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					updated := false

					if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
						if cmd.IsSet("log") {
							cfg.LogLevel = cmd.String("log")
							updated = true
						}
						if cmd.IsSet("port") {
							cfg.Port = int(cmd.Int("port"))
							updated = true
						}
						if cmd.IsSet("host") {
							cfg.Host = cmd.String("host")
							updated = true
						}
						if cmd.IsSet("proxy") {
							cfg.ProxyPort = int(cmd.Int("proxy"))
							updated = true
						}
						return nil
					}); err != nil {
						return fmt.Errorf("failed to update config: %w", err)
					}

					if updated {
						fmt.Println("Configuration updated successfully.")
					} else {
						fmt.Println("No configuration values were changed. Use --help to see available options.")
					}

					return nil
				},
			},
			{
				Name:        "run",
				Description: "Runs service in foreground. Typically called by systemd. If you need to run it manually/unmanaged, use this command.",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "rc",
						Usage: "register commands on startup",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					// wait for network (systemd user mode Wants/After is unreliable)
					if err := xnet.Wait(ctx, 0); err != nil {
						return fmt.Errorf("failed to wait for network: %w", err)
					}

					// get config
					cfg, err := config.View(a.DB)
					if err != nil {
						return fmt.Errorf("failed to get configuration from database: %w", err)
					}

					// get port, handle override
					port := cmd.Int("port")
					if port == 0 {
						port = cfg.Port
					}

					// create server
					mux := router.New(a)
					if err := server.New(a, port, mux); err != nil {
						return fmt.Errorf("failed to create server: %w", err)
					}

					// start http server
					if err := a.Server.Listen(); err != nil { // blocks until server stops or shutdown signal received
						return fmt.Errorf("server stopped with error: %w", err)
					} else {
						fmt.Println("server stopped gracefully")
					}

					return nil
				},
			},
		},
	}
})
