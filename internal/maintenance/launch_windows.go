//go:build windows

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
	cosignPath := filepath.Join(filepath.Dir(job.layout.Storage), "Programs", "cosign", "cosign.exe")
	taskName := fmt.Sprintf("%s Maintenance %s", job.name, job.id)
	runnerPath := filepath.Join(job.jobDir, windowsRunnerName)
	flag := "-" + strings.ToUpper(string(job.action)[:1]) + string(job.action)[1:]
	// The identity file is the first statement inside try so a failed write
	// exits through finally instead of leaving a runner the admitting process
	// can never identify (see ProbeJob).
	script := fmt.Sprintf(`$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$TaskName = %s
$JobDir = %s
$LogPath = %s
$ReleaseUrl = %s
$CachedInstaller = %s
$CachedBundle = %s
$CertificateIdentity = %s
$OidcIssuer = %s
$FallbackCosign = %s
$Action = %s
$exitCode = 0
$Utf8NoBom = New-Object Text.UTF8Encoding($false)

function Add-MaintenanceText([string]$Text) {
    if (-not [string]::IsNullOrEmpty($Text)) {
        [IO.File]::AppendAllText($LogPath, $Text, $Utf8NoBom)
    }
}

function Write-MaintenanceLog([string]$Message) {
    Add-MaintenanceText (("[{0:u}] {1}" -f [DateTime]::UtcNow, $Message) + [Environment]::NewLine)
}

function Assert-InstallerSignature([string]$CosignPath, [string]$Bundle, [string]$Installer) {
    # Windows PowerShell 5.1 promotes native stderr to error records. Cosign
    # writes successful status there, so judge the native exit code explicitly.
    $savedErrorActionPreference = $ErrorActionPreference
    $nativeExitCode = 1
    $nativeOutput = ""
    try {
        $ErrorActionPreference = "Continue"
        $nativeOutput = (& $CosignPath verify-blob --bundle $Bundle --certificate-identity $CertificateIdentity --certificate-oidc-issuer $OidcIssuer $Installer 2>&1 | Out-String)
        $nativeExitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedErrorActionPreference
    }
    Add-MaintenanceText $nativeOutput
    if ($nativeExitCode -ne 0) { throw "installer verification failed (exit $nativeExitCode)" }
}

try {
    %s
    New-Item -ItemType Directory -Path (Split-Path -Parent $LogPath) -Force | Out-Null
    Write-MaintenanceLog ("Maintenance " + $Action + " job started.")
    # Prefer the installer-managed verifier. This avoids PATH-dependent
    # maintenance behavior while retaining PATH as a recovery fallback.
    if (Test-Path -LiteralPath $FallbackCosign -PathType Leaf) { $cosign = $FallbackCosign }
    else {
        $cosign = Get-Command -Name "cosign" -CommandType Application -ErrorAction SilentlyContinue
        if ($null -ne $cosign) { $cosign = $cosign.Source }
        else { throw "cosign is unavailable." }
    }

    $selected = $null
    if (-not [string]::IsNullOrWhiteSpace($ReleaseUrl)) {
        try {
            $remoteInstaller = Join-Path $JobDir "install.ps1"
            $remoteBundle = Join-Path $JobDir "install.ps1.cosign.bundle"
            [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
            Invoke-WebRequest -Uri ($ReleaseUrl + "install.ps1") -OutFile $remoteInstaller -UseBasicParsing -TimeoutSec 300
            Invoke-WebRequest -Uri ($ReleaseUrl + "install.ps1.cosign.bundle") -OutFile $remoteBundle -UseBasicParsing -TimeoutSec 300
            Assert-InstallerSignature -CosignPath $cosign -Bundle $remoteBundle -Installer $remoteInstaller
            $selected = $remoteInstaller
        } catch {
            Write-MaintenanceLog ("Remote installer selection failed: " + $_.Exception.Message)
        }
    }
    if ($null -eq $selected -and $Action -eq "uninstall") {
        $fallbackInstaller = Join-Path $JobDir "cached-install.ps1"
        $fallbackBundle = Join-Path $JobDir "cached-install.ps1.cosign.bundle"
        Copy-Item -LiteralPath $CachedBundle -Destination $fallbackBundle -Force
        Copy-Item -LiteralPath $CachedInstaller -Destination $fallbackInstaller -Force
        Assert-InstallerSignature -CosignPath $cosign -Bundle $fallbackBundle -Installer $fallbackInstaller
        $selected = $fallbackInstaller
    }
    if ($null -eq $selected) { throw "No verified installer is available." }

    $env:APP_MAINTENANCE_EXPECT_EPOCH = %s
    $env:APP_MAINTENANCE_EXPECT_VERSION = %s
    if (-not [string]::IsNullOrWhiteSpace($ReleaseUrl)) { $env:APP_RELEASE_URL = $ReleaseUrl.TrimEnd("/") }
    $savedErrorActionPreference = $ErrorActionPreference
    $installerExitCode = 1
    $installerOutput = ""
    try {
        $ErrorActionPreference = "Continue"
        $installerOutput = (& (Join-Path $PSHOME "powershell.exe") -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $selected %s 2>&1 | Out-String)
        $installerExitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedErrorActionPreference
    }
    Add-MaintenanceText $installerOutput
    if ($installerExitCode -ne 0) { throw "installer failed (exit $installerExitCode)" }
    Write-MaintenanceLog ("Maintenance " + $Action + " completed.")
} catch {
    $exitCode = 1
    Write-MaintenanceLog ("Maintenance " + $Action + " failed: " + $_.Exception.Message)
} finally {
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $JobDir -Recurse -Force -ErrorAction SilentlyContinue
}
exit $exitCode
`, psLiteral(taskName), psLiteral(job.jobDir), psLiteral(job.layout.MaintenanceLog),
		psLiteral(job.releaseURL), psLiteral(job.layout.CachedInstaller), psLiteral(job.layout.CachedBundle),
		psLiteral(job.certIdentity), psLiteral(job.oidcIssuer), psLiteral(cosignPath), psLiteral(string(job.action)),
		windowsIdentityStatement, psLiteral(job.expectEpoch), psLiteral(job.expectVersion), flag)
	if err := os.WriteFile(runnerPath, []byte(script), 0o600); err != nil {
		return "", fmt.Errorf("write Windows maintenance runner: %w", err)
	}
	return runnerPath, nil
}

func startPlatformJob(ctx context.Context, job launchJob, runnerPath string) (bool, error) {
	taskName := fmt.Sprintf("%s Maintenance %s", job.name, job.id)
	registration := windowsTaskRegistration(taskName, runnerPath)
	admitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(admitCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", registration)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := cmd.CombinedOutput(); err != nil {
		mayHaveStarted := admitCtx.Err() != nil
		return mayHaveStarted, fmt.Errorf("admit Windows maintenance job: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return true, nil
}

func windowsTaskRegistration(taskName, runnerPath string) string {
	args := fmt.Sprintf(`-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "%s"`, strings.ReplaceAll(runnerPath, `"`, `\"`))
	return fmt.Sprintf(`$ErrorActionPreference = "Stop"
$taskName = %s
$currentUser = [Security.Principal.WindowsIdentity]::GetCurrent().Name
$powerShell = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$action = New-ScheduledTaskAction -Execute $powerShell -Argument %s
$settings = New-ScheduledTaskSettingsSet -Hidden -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -MultipleInstances IgnoreNew
$principal = New-ScheduledTaskPrincipal -UserId $currentUser -LogonType Interactive -RunLevel Limited
try {
    Register-ScheduledTask -TaskName $taskName -Action $action -Settings $settings -Principal $principal -Description "Runs verified application maintenance." -Force | Out-Null
    Start-ScheduledTask -TaskName $taskName
}
catch {
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    throw
}
`, psLiteral(taskName), psLiteral(args))
}

func psLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
