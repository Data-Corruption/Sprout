//go:build !windows

package maintenance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func writePlatformRunner(job launchJob) (string, error) {
	cosignPath, err := resolveCosignUnix()
	if err != nil {
		return "", err
	}
	flag := "--" + string(job.action)
	remoteInstaller := filepath.Join(job.jobDir, "install.sh")
	remoteBundle := remoteInstaller + ".cosign.bundle"
	runnerPath := filepath.Join(job.jobDir, unixRunnerName)
	// The identity file is written after the traps are armed so a failed write
	// exits through cleanup instead of leaving a runner the admitting process
	// can never identify (see ProbeJob). $$ is this shell, which outlives the
	// installer child, and its argv names run.sh inside the random job
	// directory, which is what the probe matches against a recycled PID.
	script := fmt.Sprintf(`#!/bin/sh
set -u
job=%s
log=%s
mkdir -p %s
touch "$log"
chmod 600 "$log" 2>/dev/null || :
exec >>"$log" 2>&1
cleanup() { rm -rf "$job"; }
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
printf '%%s\n' "$$" > "$job/%s" || exit 1
printf '[%%s] maintenance %s job started\n' "$(date -u '+%%Y-%%m-%%dT%%H:%%M:%%SZ')"
selected=''
remote_error=''
if [ -n %s ]; then
  if curl --connect-timeout 15 --max-time 300 -sSfL -o %s %s &&
     curl --connect-timeout 15 --max-time 300 -sSfL -o %s %s &&
     %s verify-blob --bundle %s --certificate-identity %s --certificate-oidc-issuer %s %s; then
    selected=%s
  else
    remote_error='download or verification failed'
    printf 'Remote installer selection failed; considering allowed fallback.\n'
  fi
fi
if [ -z "$selected" ] && [ %s = uninstall ]; then
  cached_installer=%s
  cached_bundle=%s
  if cp %s "$cached_bundle" &&
     cp %s "$cached_installer" &&
     %s verify-blob --bundle "$cached_bundle" --certificate-identity %s --certificate-oidc-issuer %s "$cached_installer"; then
    selected="$cached_installer"
  else
    printf 'Cached uninstall installer verification failed.\n' >&2
    exit 1
  fi
fi
if [ -z "$selected" ]; then
  printf 'No verified installer is available (%%s).\n' "$remote_error" >&2
  exit 1
fi
export APP_MAINTENANCE_EXPECT_EPOCH=%s
export APP_MAINTENANCE_EXPECT_VERSION=%s
if [ -n %s ]; then export APP_RELEASE_URL=%s; fi
sh "$selected" %s
rc=$?
printf '[%%s] maintenance %s job finished (exit %%s)\n' "$(date -u '+%%Y-%%m-%%dT%%H:%%M:%%SZ')" "$rc"
exit "$rc"
`,
		shellQuote(job.jobDir), shellQuote(job.layout.MaintenanceLog), shellQuote(job.layout.Logs),
		jobIdentityName, job.action, shellQuote(job.releaseURL),
		shellQuote(remoteInstaller), shellQuote(job.releaseURL+"install.sh"),
		shellQuote(remoteBundle), shellQuote(job.releaseURL+"install.sh.cosign.bundle"),
		shellQuote(cosignPath), shellQuote(remoteBundle), shellQuote(job.certIdentity),
		shellQuote(job.oidcIssuer), shellQuote(remoteInstaller), shellQuote(remoteInstaller),
		shellQuote(string(job.action)), shellQuote(filepath.Join(job.jobDir, "cached-install.sh")),
		shellQuote(filepath.Join(job.jobDir, "cached-install.sh.cosign.bundle")),
		shellQuote(job.layout.CachedBundle), shellQuote(job.layout.CachedInstaller), shellQuote(cosignPath),
		shellQuote(job.certIdentity), shellQuote(job.oidcIssuer), shellQuote(job.expectEpoch), shellQuote(job.expectVersion),
		shellQuote(job.releaseURL), shellQuote(strings.TrimRight(job.releaseURL, "/")), flag, job.action,
	)
	if err := os.WriteFile(runnerPath, []byte(script), 0o700); err != nil {
		return "", fmt.Errorf("write Unix maintenance runner: %w", err)
	}
	return runnerPath, nil
}

func startPlatformJob(ctx context.Context, job launchJob, runnerPath string) (bool, error) {
	if useSystemdMaintenanceLauncher() {
		admitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		unit := fmt.Sprintf("%s-maintenance-%s", job.name, job.id)
		cmd := exec.CommandContext(admitCtx,
			"systemd-run", "--user", "--quiet", "--no-block", "--collect",
			"--unit="+unit,
			"/bin/sh", runnerPath,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			mayHaveStarted := admitCtx.Err() != nil
			return mayHaveStarted, fmt.Errorf("admit systemd maintenance job: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return true, nil
	}

	cmd := exec.Command("/bin/sh", runnerPath)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("start detached maintenance job: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return true, fmt.Errorf("release detached maintenance job: %w", err)
	}
	return true, nil
}

func useSystemdMaintenanceLauncher() bool {
	serviceCapable := false
	// --- BEGIN service ---
	serviceCapable = true
	// --- END service ---
	return serviceCapable && os.Getenv("NOTIFY_SOCKET") != ""
}

func resolveCosignUnix() (string, error) {
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".local", "bin", "cosign")
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	if path, lookErr := exec.LookPath("cosign"); lookErr == nil {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("cosign is unavailable on PATH (managed location unavailable: %w)", err)
	}
	return "", fmt.Errorf("cosign is unavailable at %s or on PATH", filepath.Join(home, ".local", "bin", "cosign"))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
