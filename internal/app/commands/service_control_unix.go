//go:build !windows

// --- FILE service ---

package commands

import (
	"context"
	"fmt"

	"sprout/internal/app"
)

func controlService(ctx context.Context, a *app.App, action string) (string, error) {
	serviceName := a.BuildInfo().Name + ".service"
	switch action {
	case "start", "stop", "restart":
		return runControlCommand(ctx, action, "systemctl", "--user", action, serviceName)
	case "status":
		return runControlCommand(ctx, action, "systemctl", "--user", "status", "--no-pager", serviceName)
	default:
		return "", fmt.Errorf("unknown service action %q", action)
	}
}
