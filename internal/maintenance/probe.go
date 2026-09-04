package maintenance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// JobStatus is what ProbeJob learned about an admitted job.
type JobStatus int

const (
	// JobGone means the job directory no longer exists: the runner removed it
	// on exit, which is the normal completion signal.
	JobGone JobStatus = iota
	// JobRunning means the runner is alive, or admission is recent enough
	// that it may not have published its identity yet.
	JobRunning
	// JobOrphaned means the job directory exists but the runner is provably
	// not running: it was killed without reaching its cleanup trap, or never
	// started at all.
	JobOrphaned
)

// jobIdentityName is the file a runner writes into its job directory as its
// first action. Line one is the runner's PID; line two is a platform-specific
// token that distinguishes that PID from a later process that recycled it.
const jobIdentityName = "pid"

// jobStartGrace bounds how long an admitted runner may take to publish its
// identity. Platform admission (systemd-run --no-block, Task Scheduler)
// returns before the runner executes its first line, so a missing identity
// file inside this window means "starting", and outside it means "never ran".
const jobStartGrace = time.Minute

type runnerIdentity struct {
	pid   int
	token string
}

// errIncompleteIdentity is returned by a platform probe whose required token
// is missing or unparsable. ProbeJob treats it like a partial write inside the
// start grace window and as an error after it.
var errIncompleteIdentity = errors.New("incomplete runner identity")

// ProbeJob reports whether the runner admitted into jobDir is still running.
// Directory existence alone is the runner's cooperative completion signal; the
// identity file lets the admitting process tell a runner that is still working
// from one that died without cleaning up. Evidence that cannot be read is an
// error rather than a guess: the caller keeps waiting and the failure is
// logged, never acted on.
func ProbeJob(jobDir string, admittedAt time.Time) (JobStatus, error) {
	if _, err := os.Stat(jobDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return JobGone, nil
		}
		return 0, fmt.Errorf("inspect maintenance job directory: %w", err)
	}
	inGrace := time.Since(admittedAt) < jobStartGrace
	data, err := os.ReadFile(filepath.Join(jobDir, jobIdentityName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if inGrace {
				return JobRunning, nil
			}
			return JobOrphaned, nil
		}
		return 0, fmt.Errorf("read maintenance runner identity: %w", err)
	}
	identity, err := parseRunnerIdentity(data)
	if err != nil {
		// The runner writes the file in one call but not atomically; a
		// partial read during startup is indistinguishable from a missing
		// file. After the grace window it is evidence of something else.
		if inGrace {
			return JobRunning, nil
		}
		return 0, fmt.Errorf("maintenance runner identity in %s: %w", jobDir, err)
	}
	alive, err := runnerAlive(jobDir, identity)
	if err != nil {
		if errors.Is(err, errIncompleteIdentity) && inGrace {
			return JobRunning, nil
		}
		return 0, err
	}
	if alive {
		return JobRunning, nil
	}
	return JobOrphaned, nil
}

func parseRunnerIdentity(data []byte) (runnerIdentity, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(data)), "\r\n", "\n"), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || pid <= 0 {
		return runnerIdentity{}, fmt.Errorf("malformed runner pid %q", lines[0])
	}
	id := runnerIdentity{pid: pid}
	if len(lines) > 1 {
		id.token = strings.TrimSpace(lines[1])
	}
	return id, nil
}
