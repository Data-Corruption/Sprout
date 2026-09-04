//go:build windows

package maintenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sprout/internal/layout"
)

func TestWindowsMaintenanceTaskIsHiddenInteractiveAndNetworkCapable(t *testing.T) {
	script := windowsTaskRegistration("Sprout Maintenance job", `C:\state\jobs\job\run.ps1`)
	for _, want := range []string{
		"-WindowStyle Hidden",
		"New-ScheduledTaskSettingsSet -Hidden",
		"-ExecutionTimeLimit ([TimeSpan]::Zero)",
		"-LogonType Interactive",
		"Start-ScheduledTask",
		"Unregister-ScheduledTask",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("registration script missing %q", want)
		}
	}
	if strings.Contains(script, "S4U") {
		t.Fatal("maintenance task uses S4U, which cannot download the remote installer")
	}
}

func TestWindowsRunnerUsesExitCodesAndConsistentUTF8Logging(t *testing.T) {
	l := layout.FromStorage(filepath.Join(t.TempDir(), "Sprout"), "sprout")
	jobDir := filepath.Join(l.Jobs, "job")
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	runner, err := writePlatformRunner(launchJob{
		action: ActionUninstall,
		id:     "job", jobDir: jobDir, layout: l, name: "sprout",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(runner)
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, want := range []string{
		"try {\n    " + windowsIdentityStatement + "\n",
		"UTF8Encoding($false)", "AppendAllText", "$installerExitCode", "$nativeExitCode",
		"Copy-Item -LiteralPath $CachedInstaller -Destination $fallbackInstaller",
		"Assert-InstallerSignature -CosignPath $cosign -Bundle $fallbackBundle -Installer $fallbackInstaller",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("runner source missing %q", want)
		}
	}
	if strings.Contains(source, "*>> $LogPath") {
		t.Fatal("runner mixes Windows PowerShell redirection encoding into maintenance.log")
	}
}
