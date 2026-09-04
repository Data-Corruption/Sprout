//go:build !windows

// --- FILE service.https ---

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"sprout/internal/build"

	"github.com/urfave/cli/v3"
)

func TestInitDoesNotCreateDashboardSecretMaterial(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("application intentionally refuses ordinary initialization as real root")
	}
	t.Setenv("HOME", t.TempDir())
	a := New(build.BuildInfo{
		Name:               "sprout-init-test",
		Version:            "v0.0.0-test",
		ContactURL:         "https://example.invalid",
		DefaultLogLevel:    "warn",
		ServiceDefaultPort: 8484,
		DevMode:            true,
	})
	command := &cli.Command{
		Name: "sprout-init-test",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "log"},
			&cli.BoolFlag{Name: "migrate"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, err := a.Init(ctx, cmd)
			return err
		},
	}
	if err := command.Run(context.Background(), []string{command.Name}); err != nil {
		t.Fatalf("initialize app: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}
	for _, name := range []string{"cert.pem", "key.pem"} {
		if _, err := os.Stat(filepath.Join(a.Layout.Secrets, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Init created dashboard secret %s: %v", name, err)
		}
	}
}
