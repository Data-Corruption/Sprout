package maintenance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sprout/internal/layout"
)

// Action selects the installer-owned maintenance transaction.
type Action string

const (
	ActionUpdate    Action = "update"
	ActionUninstall Action = "uninstall"
)

// LaunchOptions contains immutable installation identity needed by the
// detached verifier. The worker reads no database state after admission.
type LaunchOptions struct {
	Layout       layout.Layout
	Name         string
	Version      string
	CertIdentity string
	OIDCIssuer   string
	DevMode      bool
}

// Admission identifies the detached job admitted by StartMaintenance.
// LogPath is stable across jobs; JobDir exists until this specific controller
// finishes and lets the admitting process distinguish completion from drain.
// ProbeJob tells a running controller from one that died without cleaning up.
type Admission struct {
	LogPath string
	JobDir  string
}

// StartMaintenance validates the current installation, writes a private job
// runner, and admits it to the platform's detached process manager. Success
// means the job was admitted, not that the transaction completed.
func StartMaintenance(ctx context.Context, opts LaunchOptions, action Action) (Admission, error) {
	if action != ActionUpdate && action != ActionUninstall {
		return Admission{}, fmt.Errorf("unknown maintenance action %q", action)
	}
	if opts.DevMode {
		return Admission{}, fmt.Errorf("maintenance is unavailable for development builds")
	}
	if err := ctx.Err(); err != nil {
		return Admission{}, err
	}
	state, err := ReadState(opts.Layout)
	if err != nil {
		return Admission{}, fmt.Errorf("read lifecycle state for maintenance admission: %w", err)
	}
	if err := (Expectation{
		Phase:             PhaseReady,
		Version:           opts.Version,
		InstallationEpoch: state.InstallationEpoch,
	}).Check(state); err != nil {
		return Admission{}, fmt.Errorf("admit maintenance job: %w", err)
	}

	releaseURL, releaseErr := readReleaseURL(opts.Layout.ReleaseURL)
	if action == ActionUpdate && releaseErr != nil {
		return Admission{}, releaseErr
	}
	if action == ActionUninstall && releaseErr != nil {
		// Any unusable remote-source metadata is equivalent to the remote
		// selection failing before execution. The verified retained cache is
		// precisely the uninstall recovery path for that case.
		releaseURL = ""
	}
	if releaseURL != "" || action == ActionUninstall {
		if opts.CertIdentity == "" || opts.OIDCIssuer == "" {
			return Admission{}, fmt.Errorf("maintenance unavailable: no cosign identity baked into this build")
		}
	}
	if action == ActionUninstall && releaseURL == "" {
		if err := requireCachedInstaller(opts.Layout); err != nil {
			return Admission{}, err
		}
	}

	id, err := newJobID()
	if err != nil {
		return Admission{}, err
	}
	jobDir := filepath.Join(opts.Layout.Jobs, id)
	if err := os.Mkdir(jobDir, 0o700); err != nil {
		return Admission{}, fmt.Errorf("create maintenance job directory: %w", err)
	}
	admission := Admission{LogPath: opts.Layout.MaintenanceLog, JobDir: jobDir}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(jobDir)
		}
	}()

	expectVersion := ""
	if action == ActionUpdate {
		expectVersion = state.Version
	}
	job := launchJob{
		action:        action,
		id:            id,
		jobDir:        jobDir,
		layout:        opts.Layout,
		name:          opts.Name,
		releaseURL:    releaseURL,
		certIdentity:  opts.CertIdentity,
		oidcIssuer:    opts.OIDCIssuer,
		expectEpoch:   state.InstallationEpoch,
		expectVersion: expectVersion,
	}
	runnerPath, err := writePlatformRunner(job)
	if err != nil {
		return Admission{}, err
	}
	mayHaveStarted, err := startPlatformJob(ctx, job, runnerPath)
	if mayHaveStarted {
		// Admission timeouts are inherently ambiguous: the platform manager
		// may have accepted the job just before its registrar was killed. Keep
		// the runner available; lifecycle expectations make a retry harmless.
		cleanup = false
	}
	if err != nil {
		if mayHaveStarted {
			return admission, err
		}
		return Admission{}, err
	}
	cleanup = false
	return admission, nil
}

type launchJob struct {
	action        Action
	id            string
	jobDir        string
	layout        layout.Layout
	name          string
	releaseURL    string
	certIdentity  string
	oidcIssuer    string
	expectEpoch   string
	expectVersion string
}

func readReleaseURL(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("updates disabled: missing release source %s: %w", path, err)
		}
		return "", fmt.Errorf("read release source: %w", err)
	}
	value := strings.TrimRight(strings.TrimSpace(string(data)), "/")
	if value == "" {
		return "", fmt.Errorf("release source %s is empty", path)
	}
	return value + "/", nil
}

func requireCachedInstaller(l layout.Layout) error {
	for _, path := range []string{l.CachedInstaller, l.CachedBundle} {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("cached uninstall recovery is unavailable at %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cached uninstall recovery path is not a regular file: %s", path)
		}
	}
	return nil
}

func newJobID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate maintenance job id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
