//go:build windows

// --- FILE service ---

package commands

import (
	"fmt"
	"sprout/internal/app"
)

// printServiceHelp documents the per-user scheduled task that install.ps1
// registers as the Windows "service". Everything runs as the current user, no
// elevation required.
func printServiceHelp(a *app.App) {
	appName := a.BuildInfo().Name
	logDir := a.Layout.Logs
	fmt.Printf("Service Cheat Sheet (per-user scheduled task)\n\n")
	fmt.Printf("    Status:  %s service status\n", appName)
	fmt.Printf("    Start:   %s service start\n", appName)
	fmt.Printf("    Stop:    %s service stop\n", appName)
	fmt.Printf("    Restart: %s service restart\n\n", appName)
	fmt.Printf("    Enable:  Enable-ScheduledTask -TaskName %s\n", appName)
	fmt.Printf("    Disable: Disable-ScheduledTask -TaskName %s\n\n", appName)
	fmt.Printf("    Config:  %s config set --help\n", appName)
	fmt.Printf("    Logs:    %s\n", logDir)
	fmt.Printf("\nThe task starts at logon; it does not run before you log in.\n")
	fmt.Printf("Use the service commands above for normal control. Direct Stop-ScheduledTask\n")
	fmt.Printf("or schtasks /End is an emergency hard-stop that bypasses graceful shutdown.\n")
}
