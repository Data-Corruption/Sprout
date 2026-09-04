package commands

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"

	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/types"
	"sprout/pkg/xlog"

	"github.com/urfave/cli/v3"
)

func configCommand(a *app.App) *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "show or change persistent configuration",
		Commands: []*cli.Command{
			{
				Name:  "show",
				Usage: "show safe user-configurable values",
				Action: func(context.Context, *cli.Command) error {
					cfg, err := config.View(a.DB)
					if err != nil {
						return fmt.Errorf("failed to read config: %w", err)
					}
					writeSafeConfig(os.Stdout, cfg)
					return nil
				},
			},
			{
				Name:  "set",
				Usage: "change persistent configuration",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "log",
						Usage: "set log level (" + xlog.ValidLevels + ")",
					},
					// --- BEGIN service.https ---
					&cli.StringFlag{
						Name:  "ui-bind",
						Usage: `set dashboard HTTPS bind (for example ":8484")`,
					},
					&cli.IntFlag{
						Name:  "port",
						Usage: "set dashboard HTTPS port while preserving its host",
					},
					&cli.StringFlag{
						Name:  "proxy-bind",
						Usage: `set loopback proxy HTTP bind; pass "" to disable`,
					},
					&cli.IntFlag{
						Name:  "proxy-port",
						Usage: "set proxy port while preserving its loopback host",
					},
					// --- END service.https ---
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					// --- BEGIN service.https ---
					if cmd.IsSet("ui-bind") && cmd.IsSet("port") {
						return fmt.Errorf("--ui-bind and --port cannot be used together")
					}
					if cmd.IsSet("proxy-bind") && cmd.IsSet("proxy-port") {
						return fmt.Errorf("--proxy-bind and --proxy-port cannot be used together")
					}
					// --- END service.https ---

					updated := false
					cfg, err := config.Update(a.DB, func(cfg *types.Configuration) error {
						if cmd.IsSet("log") {
							cfg.LogLevel = cmd.String("log")
							updated = true
						}
						// --- BEGIN service.https ---
						if cmd.IsSet("ui-bind") {
							cfg.UIBind = cmd.String("ui-bind")
							updated = true
						}
						if cmd.IsSet("port") {
							bind, err := bindWithPort(cfg.UIBind, cmd.Int("port"), "")
							if err != nil {
								return fmt.Errorf("set dashboard port: %w", err)
							}
							cfg.UIBind = bind
							updated = true
						}
						if cmd.IsSet("proxy-bind") {
							cfg.ProxyBind = cmd.String("proxy-bind")
							updated = true
						}
						if cmd.IsSet("proxy-port") {
							bind, err := bindWithPort(cfg.ProxyBind, cmd.Int("proxy-port"), "127.0.0.1")
							if err != nil {
								return fmt.Errorf("set proxy port: %w", err)
							}
							cfg.ProxyBind = bind
							updated = true
						}
						// --- END service.https ---
						return nil
					})
					if err != nil {
						return fmt.Errorf("failed to update config: %w", err)
					}

					if !updated {
						fmt.Println("No configuration values were changed. Use --help to see available options.")
						return nil
					}
					fmt.Println("Configuration updated successfully.")
					writeSafeConfig(os.Stdout, cfg)
					return nil
				},
			},
		},
	}
}

func writeSafeConfig(w io.Writer, cfg *types.Configuration) {
	fmt.Fprintf(w, "log: %s\n", cfg.LogLevel)
	// --- BEGIN service.https ---
	fmt.Fprintf(w, "ui-bind: %s\n", cfg.UIBind)
	proxyBind := cfg.ProxyBind
	if proxyBind == "" {
		proxyBind = "disabled"
	}
	fmt.Fprintf(w, "proxy-bind: %s\n", proxyBind)
	// --- END service.https ---
}

// --- BEGIN service.https ---
func bindWithPort(bind string, port int, defaultHost string) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("port %d is outside 1-65535", port)
	}
	host := defaultHost
	if bind != "" {
		var err error
		host, _, err = net.SplitHostPort(bind)
		if err != nil {
			return "", fmt.Errorf("invalid existing bind %q: %w", bind, err)
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// --- END service.https ---
