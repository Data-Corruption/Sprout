package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"sprout/internal/app"
	"sprout/internal/app/commands"
	"sprout/internal/build"
	"sprout/pkg/xlog"

	"github.com/urfave/cli/v3"
)

func main() {
	os.Exit(runMain())
}

func notifyProcessContext(parent context.Context) (context.Context, context.CancelFunc) {
	// On Windows, the Go runtime installs SetConsoleCtrlHandler. It maps delivered
	// CTRL_C_EVENT and CTRL_BREAK_EVENT to os.Interrupt, and maps
	// CTRL_CLOSE_EVENT, CTRL_LOGOFF_EVENT, and CTRL_SHUTDOWN_EVENT to SIGTERM.
	ctx, stopSignals := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	// Pretty sure all of these signals escalate to a hard kill after a timeout.
	go func() {
		<-ctx.Done()
		stopSignals()
	}()
	return ctx, stopSignals
}

func runMain() int {
	ctx, stopSignals := notifyProcessContext(context.Background())
	defer stopSignals()

	application := app.New(build.Info())

	rootCommand := &cli.Command{
		Name:    application.BuildInfo().Name,
		Version: application.BuildInfo().Version,
		Usage:   "Sprout is a template for building Go services / cli apps.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "log",
				Aliases: []string{"l"},
				Value:   application.BuildInfo().DefaultLogLevel,
				Usage:   "override log level (" + xlog.ValidLevels + ")",
			},
			&cli.BoolFlag{
				Name:    "migrate",
				Aliases: []string{"m"},
				Hidden:  true,
				Usage:   "apply database migrations (installer use only)",
			},
			&cli.BoolFlag{
				Name:   "build-vars",
				Hidden: true,
				Usage:  "print build variables and exit",
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			if cmd.Bool("build-vars") {
				fmt.Println(application.BuildInfo().PrintJSON())
				os.Exit(0)
			}
			ctx, err := application.Init(ctx, cmd)
			if err != nil || cmd.Bool("migrate") {
				return ctx, err
			}
			// --- BEGIN update.notifications ---
			if err := application.StartUpdateCheckIfDue(ctx); err != nil {
				// Update discovery is optional and must never prevent recovery or
				// an unrelated command from running. Manual checks still return
				// release-source errors directly.
				application.Log.Errorf("start update check: %v", err)
			}
			// --- END update.notifications ---
			return ctx, nil
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			application.Log.Info("Ran with no arguments.")
			fmt.Printf("%s version %s\n", application.BuildInfo().Name, application.BuildInfo().Version)
			fmt.Printf("Use '%s help' to see available commands.\n", application.BuildInfo().Name)
			return nil
		},
		Commands: commands.All(application),
	}

	commandErr := rootCommand.Run(ctx, os.Args)
	if commandErr != nil && application.Log != nil {
		application.Log.Errorf("command failed: %v", commandErr)
	}
	closeErr := application.Close()
	if err := errors.Join(commandErr, closeErr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
