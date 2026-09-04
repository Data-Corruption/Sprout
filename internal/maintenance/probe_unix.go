//go:build !windows

package maintenance

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const unixRunnerName = "run.sh"

// runnerAlive decides whether the PID recorded by the runner still belongs to
// it. kill(0) answers "is there a process"; /proc/<pid>/cmdline answers "is it
// ours": the runner is started as `/bin/sh <jobDir>/run.sh` and sh never
// rewrites its argv, so a recycled PID would have to be executing a script
// under a random 96-bit directory name to be mistaken for the runner.
func runnerAlive(jobDir string, id runnerIdentity) (bool, error) {
	if err := syscall.Kill(id.pid, 0); err != nil {
		// EPERM is a live process owned by someone else, which the same-user
		// runner never is.
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM) {
			return false, nil
		}
		return false, fmt.Errorf("signal maintenance runner %d: %w", id.pid, err)
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", id.pid))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read maintenance runner %d command line: %w", id.pid, err)
	}
	// A zombie (exited, not yet reaped) still answers kill(0) but has an empty
	// cmdline, so it falls through to "not ours" here, which is correct.
	runner := []byte(filepath.Join(jobDir, unixRunnerName))
	for _, arg := range bytes.Split(cmdline, []byte{0}) {
		if bytes.Equal(arg, runner) {
			return true, nil
		}
	}
	return false, nil
}
