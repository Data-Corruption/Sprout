//go:build !windows

// --- FILE service.https ---

package settings

import (
	"fmt"
	"os/exec"
	"sprout/internal/app"
	"time"
)

// requestServiceStop stops the managed service via a transient systemd unit
// that survives our process dying, so the stop completes and logs reliably.
// Unmanaged/dev builds just shut the server down.
func requestServiceStop(a *app.App) {
	if !a.BuildInfo().ServiceEnabled || a.DevMode {
		go a.Server.Shutdown()
		return
	}
	go func() {
		serviceName := a.BuildInfo().Name + ".service"
		unitName := fmt.Sprintf("%s-stop-%s", a.BuildInfo().Name, time.Now().Format("20060102-150405"))
		syslogIdent := fmt.Sprintf("SyslogIdentifier=%s-stop", a.BuildInfo().Name)

		cmd := exec.CommandContext(
			a.Context,
			"systemd-run",
			"--user",
			"--unit="+unitName,
			"--quiet",
			"--no-block",
			"-p", "StandardOutput=journal",
			"-p", "StandardError=journal",
			"-p", syslogIdent,
			"systemctl", "--user", "stop", serviceName,
		)
		if err := cmd.Run(); err != nil {
			// ServiceEnabled is a build flag, not proof that systemd is
			// managing this process: a hand-started `service run` on a host
			// without user systemd lands here too. The user asked to stop, so
			// stop, rather than reporting the request accepted and staying up.
			a.Log.Errorf("failed to start stop unit, shutting down directly: %v", err)
			a.Server.Shutdown()
		}
	}()
}

// requestServiceRestart exits cleanly; under systemd the unit's
// Restart=always brings the service back up. Unmanaged/dev builds simply
// stop.
func requestServiceRestart(a *app.App) {
	go a.Server.Shutdown()
}
