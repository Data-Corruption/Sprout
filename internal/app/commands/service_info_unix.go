//go:build !windows

// --- FILE service ---

package commands

import (
	"fmt"
	"io"
	"os"
	"sprout/internal/app"
)

func printServiceHelp(a *app.App) {
	printServiceHelpTo(os.Stdout, a)
}

func printServiceHelpTo(w io.Writer, a *app.App) {
	appName := a.BuildInfo().Name
	serviceName := a.BuildInfo().Name + ".service"
	envFilePath := a.Layout.Env
	fmt.Fprint(w, "🖧 Service Cheat Sheet\n\n")
	fmt.Fprintf(w, "    Status:  %s service status\n", appName)
	fmt.Fprintf(w, "    Start:   %s service start\n", appName)
	fmt.Fprintf(w, "    Stop:    %s service stop\n", appName)
	fmt.Fprintf(w, "    Restart: %s service restart\n\n", appName)
	fmt.Fprintf(w, "    Enable:  systemctl --user enable %s\n", serviceName)
	fmt.Fprintf(w, "    Disable: systemctl --user disable %s\n\n", serviceName)
	fmt.Fprintf(w, "    Reset:   systemctl --user reset-failed %s\n\n", serviceName)
	fmt.Fprintf(w, "    Config:  %s config set --help  (persistent application settings)\n", appName)
	fmt.Fprintf(w, "    Env:     edit %s then restart  (optional systemd environment)\n\n", envFilePath)
	fmt.Fprintf(w, "    Logs:        journalctl --user -u %s -n 200 --no-pager\n", serviceName)
	fmt.Fprintf(w, "    Stop Logs:   journalctl --user -u %s-stop* -n 200 --no-pager\n", serviceName)
	fmt.Fprintf(w, "    Maintenance Log: %s\n", a.Layout.MaintenanceLog)
}
