// --- FILE service ---

package commands

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func runControlCommand(ctx context.Context, action, executable string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, executable, args...).CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if ctx.Err() != nil {
		return detail, fmt.Errorf("service %s did not complete: %w", action, ctx.Err())
	}
	if err != nil {
		if detail == "" {
			return "", fmt.Errorf("service %s failed: %w", action, err)
		}
		return detail, fmt.Errorf("service %s failed: %w: %s", action, err, detail)
	}
	return detail, nil
}
