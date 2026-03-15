package main

import (
	"context"
	"fmt"
	"os"

	"sprout/internal/app"
	"sprout/internal/app/commands"
	"sprout/internal/build"

	"github.com/urfave/cli/v3"
)

func main() {
	app := app.New(build.Info())
	defer app.Close()

	var subCommands []*cli.Command
	for _, regFunc := range commands.Registry {
		subCommands = append(subCommands, regFunc(app))
	}

	rootCommand := &cli.Command{
		Name:    app.BuildInfo().Name,
		Version: app.BuildInfo().Version,
		Usage:   "Sprout is a template for building Go services / cli apps.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "log",
				Aliases: []string{"l"},
				Value:   app.BuildInfo().DefaultLogLevel,
				Usage:   "override log level (debug|info|warn|error|none)",
			},
			&cli.BoolFlag{
				Name:    "version",
				Aliases: []string{"v"},
				Usage:   "print version and exit",
			},
			&cli.IntFlag{
				Name:    "port",
				Aliases: []string{"p"},
				Usage:   "temporarily override port in config",
			},
			&cli.BoolFlag{
				Name:    "migrate",
				Aliases: []string{"m"},
				Hidden:  true,
				Usage:   "skip migration guard (for the migrator)",
			},
			&cli.BoolFlag{
				Name:   "build-vars",
				Hidden: true,
				Usage:  "print build variables and exit",
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			if cmd.Bool("build-vars") {
				fmt.Println(app.BuildInfo().PrintJSON())
				os.Exit(0)
			}
			return app.Init(ctx, cmd)
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			app.Log.Info("Ran with no arguments.")
			fmt.Printf("%s version %s\n", app.BuildInfo().Name, app.BuildInfo().Version)
			fmt.Printf("Use '%s help' to see available commands.\n", app.BuildInfo().Name)
			return nil
		},
		Commands: subCommands,
	}

	if err := rootCommand.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
