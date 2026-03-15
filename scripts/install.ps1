# Transparent WSL app installation script for Windows
# Usage: powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
#
# This script will:
# 1. Check for WSL and offer to install it if missing (requires admin)
# 2. Ensure systemd is enabled for service support
# 3. Install the app inside WSL
# 4. Create a Windows shim for seamless CLI usage

$AppName = "<APP_NAME>"
$ReleaseURL = "<RELEASE_URL>"
$Service = <SERVICE>

$ErrorActionPreference = "Stop"

function Fail([string]$msg, [int]$code = 1) { $host.UI.WriteErrorLine("$msg"); [Environment]::Exit($code) }
function Info($msg) { Write-Host $msg }
function Warn($msg) { Write-Host $msg -ForegroundColor Yellow }
function Success($msg) { Write-Host $msg -ForegroundColor Green }

function Test-IsAdmin {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Test-WslInstalled {
  try {
    $null = & wsl.exe --status 2>&1
    return $true
  } catch {
    return $false
  }
}

function Test-WslDistroExists {
  try {
    $output = & wsl.exe -l -q 2>&1
    # Filter out empty lines and check if any distro exists
    $distros = $output | Where-Object { $_ -and $_.Trim() -ne "" }
    return ($distros.Count -gt 0)
  } catch {
    return $false
  }
}

function Test-WslRunning {
  try {
    $null = & wsl.exe -e true 2>&1
    return $true
  } catch {
    return $false
  }
}

function Test-SystemdEnabled {
  try {
    # Check if systemd is running as PID 1
    $pid1 = & wsl.exe -e sh -c 'cat /proc/1/comm 2>/dev/null' 2>&1
    return ($pid1 -eq "systemd")
  } catch {
    return $false
  }
}

function Test-SystemdConfigured {
  try {
    $result = & wsl.exe -e sh -c 'grep -qE "^[[:space:]]*systemd[[:space:]]*=[[:space:]]*true" /etc/wsl.conf 2>/dev/null && echo yes || echo no' 2>&1
    return ($result -eq "yes")
  } catch {
    return $false
  }
}

# Ensure WSL is installed -----------------------------------------------------
if (-not (Test-WslInstalled)) {
  Warn "WSL is not installed on this system."
  Write-Host ""
  
  $response = Read-Host "Would you like to install WSL now? This requires administrator privileges. [Y/n]"
  if ($response -match '^[Nn]') {
    Fail "WSL is required. Please install WSL manually and re-run this script." 1
  }
  
  if (-not (Test-IsAdmin)) {
    Info "Requesting administrator privileges to install WSL..."
    Write-Host ""
    
    # Re-launch this script as admin to install WSL
    $scriptPath = $MyInvocation.MyCommand.Path
    $proc = Start-Process powershell -Verb RunAs -ArgumentList "-NoProfile -ExecutionPolicy Bypass -Command `"wsl --install --no-launch; Read-Host 'Press Enter to continue'`"" -Wait -PassThru
    
    if ($proc.ExitCode -ne 0) {
      Fail "WSL installation may have failed. Please try running 'wsl --install' manually as administrator." 1
    }
    
    Write-Host ""
    Warn "WSL has been installed!"
    Warn "You MUST RESTART your computer before continuing."
    Write-Host ""
    Write-Host "After restarting:"
    Write-Host "  1. Open a terminal and run 'wsl' to complete first-time setup"
    Write-Host "  2. Re-run this installer"
    Write-Host ""
    Read-Host "Press Enter to exit"
    exit 0
  } else {
    # Already admin, install directly
    Info "Installing WSL..."
    & wsl --install --no-launch
    if ($LASTEXITCODE -ne 0) {
      Fail "WSL installation failed with exit code $LASTEXITCODE." $LASTEXITCODE
    }
    
    Write-Host ""
    Warn "WSL has been installed!"
    Warn "You MUST RESTART your computer before continuing."
    Write-Host ""
    Write-Host "After restarting:"
    Write-Host "  1. Open a terminal and run 'wsl' to complete first-time setup"
    Write-Host "  2. Re-run this installer"
    Write-Host ""
    exit 0
  }
}

# Ensure a WSL distro exists --------------------------------------------------
if (-not (Test-WslDistroExists)) {
  Warn "WSL is installed but no Linux distribution was found."
  Write-Host ""
  Write-Host "Please complete WSL setup:"
  Write-Host "  1. Open a terminal and run: wsl --install -d Ubuntu"
  Write-Host "  2. Follow the prompts to create a Linux user"
  Write-Host "  3. Re-run this installer"
  Write-Host ""
  Fail "No WSL distribution found." 1
}

# Ensure WSL is running -------------------------------------------------------
if (-not (Test-WslRunning)) {
  Warn "WSL is installed but failed to start."
  Write-Host ""
  Write-Host "Try the following:"
  Write-Host "  1. Open a terminal and run 'wsl' to start it manually"
  Write-Host "  2. If that fails, try: wsl --update"
  Write-Host "  3. Re-run this installer"
  Write-Host ""
  Fail "Failed to start WSL." 1
}

Info "WSL is installed and running."

# Ensure systemd is enabled (required for services) ---------------------------
if ($Service -eq 'true') {
  if (-not (Test-SystemdEnabled)) {
    if (-not (Test-SystemdConfigured)) {
      Info "Enabling systemd in WSL (required for service support)..."
      
      # Use wsl -u root to bypass sudo password requirement
      & wsl.exe -u root sh -c 'mkdir -p /etc && (grep -q "\[boot\]" /etc/wsl.conf 2>/dev/null && sed -i "/\[boot\]/a systemd=true" /etc/wsl.conf || printf "[boot]\nsystemd=true\n" >> /etc/wsl.conf)'
      
      if ($LASTEXITCODE -ne 0) {
        Warn "Could not automatically enable systemd."
        Write-Host ""
        Write-Host "Please enable it manually:"
        Write-Host "  1. In WSL, run: sudo sh -c 'echo -e \"[boot]\nsystemd=true\" >> /etc/wsl.conf'"
        Write-Host "  2. In PowerShell, run: wsl --shutdown"
        Write-Host "  3. Re-run this installer"
        Write-Host ""
        Fail "Systemd configuration failed." 1
      }
      
      Success "Systemd enabled in configuration."
    }
    
    # Restart WSL to apply systemd
    Info "Restarting WSL to enable systemd..."
    & wsl.exe --shutdown
    Start-Sleep -Seconds 2
    
    # Start WSL again
    $null = & wsl.exe -e true 2>&1
    
    if (-not (Test-SystemdEnabled)) {
      Warn "Systemd may not be fully active yet."
      Write-Host "If service installation fails, try:"
      Write-Host "  1. Run: wsl --shutdown"
      Write-Host "  2. Open a new WSL terminal"
      Write-Host "  3. Re-run this installer"
      Write-Host ""
    } else {
      Success "Systemd is now active."
    }
  } else {
    Info "Systemd is already enabled."
  }
}

# Run Linux installer inside WSL ----------------------------------------------
$linuxInstallCmd = "curl -fsSL ${ReleaseURL}install.sh | sh"
Write-Host ""
Info "Running Linux installer inside WSL..."
& $env:SystemRoot\System32\wsl.exe -e /bin/sh -lc $linuxInstallCmd
Write-Host ""
if ($LASTEXITCODE -ne 0) { Fail "Linux install command failed with exit code $LASTEXITCODE." $LASTEXITCODE }

# Create Windows shim for seamless CLI usage ----------------------------------
$shimRoot = Join-Path $env:LOCALAPPDATA "Programs"
$shimDir  = Join-Path $shimRoot $AppName
New-Item -ItemType Directory -Force -Path $shimDir | Out-Null

$shimPathCmd = Join-Path $shimDir "$AppName.cmd"
$shimContent = @"
@echo off
setlocal
set "WSL=%SystemRoot%\System32\wsl.exe"
set "APP=%~n0"
"%WSL%" -e /bin/sh -lc "exec %APP% \"`$@\"" -- %*
endlocal
"@
Set-Content -Path $shimPathCmd -Value $shimContent -Encoding ASCII

# Ensure shim directory is on USER PATH ---------------------------------------
function PathSplit([string]$path) {
  if ([string]::IsNullOrEmpty($path)) { @() } else { $path.Split(';') | Where-Object { $_ -ne "" } }
}
function PathHas([string]$path, [string]$dir) {
  (PathSplit $path | Where-Object { $_.TrimEnd('\') -ieq $dir.TrimEnd('\') }) -ne $null
}

$userPath = [Environment]::GetEnvironmentVariable("PATH","User")
if (-not (PathHas $userPath $shimDir)) {
  $newUserPath = if ([string]::IsNullOrEmpty($userPath)) { $shimDir } else { "$userPath;$shimDir" }
  [Environment]::SetEnvironmentVariable("PATH", $newUserPath, "User")
  if (-not (PathHas $env:PATH $shimDir)) { $env:PATH = "$env:PATH;$shimDir" }
  Info "Added to user PATH: $shimDir"
  Info "Open a new terminal for other shells to pick it up."
}

# DONE ------------------------------------------------------------------------
Write-Host ""
Success "Installation complete!"

if ($Service -eq 'true') {
  Write-Host ""
  Write-Host "Note: To manage the service, enter WSL first via 'wsl'"
  Write-Host "Otherwise, use the app as a native Windows CLI application."
}