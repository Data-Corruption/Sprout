#Requires -Version 5.1

<#
Per-user Windows 11 installer. No administrator rights required: the binary
lives in %LOCALAPPDATA%\Programs, data in %LOCALAPPDATA%, the optional
background "service" is a per-user scheduled task that starts at logon, and
PATH changes are user-scoped. This mirrors the user-level Linux install.

Verification: a pinned cosign (bootstrapped per-user if not already on PATH,
its download sha256-pinned in this script) verifies the signed release
checksums, then the downloaded artifact is verified against those checksums.

Example: irm https://releases.sproutcli.dev/install.ps1 | iex

Mirrors: set APP_RELEASE_URL to install from a byte-for-byte mirror of the
official release artifacts; cosign signatures remain valid. Mirror installs do
not write the release-url file, which disables update checks and self-update.

Testing: set APP_SKIP_VERIFY=true to skip cosign signature verification (the
plain sha256 check still runs). Never use it for real installs.
#>

[CmdletBinding()]
param(
    [switch]$Update,
    [switch]$Uninstall
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$Error.Clear()

# Rendered by scripts/build.sh before release.
$AppName = "<APP_NAME>"
$DefaultReleaseUrl = "<RELEASE_URL>"
$ServiceEnabled = "<SERVICE>"
$ServiceDescription = "<SERVICE_DESC>"
$ServiceArgs = "<SERVICE_ARGS>"
$CertificateIdentity = "<CERT_IDENTITY>"
$OidcIssuer = "<OIDC_ISSUER>"
$CosignVersion = "<COSIGN_VERSION>"
$CosignSha256 = "<COSIGN_SHA_WINDOWS_AMD64>"

# The scheduled task name matches BuildInfo().Name; the Go service/uninstall
# commands manage the task under this exact name.
$TaskName = $AppName
$TaskPath = "\"

$SkipVerify = ($env:APP_SKIP_VERIFY -eq "true" -or $env:APP_SKIP_VERIFY -eq "1")
$SemVerPattern = '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'

if ($AppName -notmatch '^<.+>$' -and
    $AppName -cnotmatch '^[A-Za-z0-9_][A-Za-z0-9._-]*$') {
    throw "Application name must match [A-Za-z0-9_][A-Za-z0-9._-]*."
}

# "sprout" -> "Sprout"; must match internal/layout/path_windows.go.
$AppDirName = $AppName.Substring(0, 1).ToUpperInvariant() + $AppName.Substring(1)

$LocalAppData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
$AppDir = Join-Path (Join-Path $LocalAppData "Programs") $AppDirName
$AppBin = Join-Path $AppDir "$AppName.exe"
$StorageDir = Join-Path $LocalAppData $AppDirName
$DataDir = Join-Path $StorageDir "data"
$ControlDir = Join-Path $StorageDir "control"
$MaintenanceDir = Join-Path $StorageDir "maintenance"
$LogsDir = Join-Path $StorageDir "logs"
$InstancesDir = Join-Path $ControlDir "instances"
$OperationLockFile = Join-Path $ControlDir "operation.lock"
$LifecycleLockFile = Join-Path $ControlDir "lifecycle.lock"
$StateFile = Join-Path $ControlDir "state.json"
$CachedInstaller = Join-Path $MaintenanceDir "install.ps1"
$CachedInstallerBundle = Join-Path $MaintenanceDir "install.ps1.cosign.bundle"
# --- BEGIN update ---
$ReleaseUrlFile = Join-Path $MaintenanceDir "release-url"
# --- END update ---
$CosignDir = Join-Path (Join-Path $LocalAppData "Programs") "cosign"
$CosignBin = Join-Path $CosignDir "cosign.exe"
$LockTimeoutSeconds = 300
$ServiceReadyTimeoutSeconds = 90
# --- BEGIN service ---
$ServiceStopLease = Join-Path $ControlDir "service.stop"
# Keep this protocol duration in sync with ServiceStopLeaseDuration in Go.
$ServiceStopLeaseSeconds = 60
$ServiceGracefulStopTimeoutSeconds = 15
$ServiceForcedStopTimeoutSeconds = 30
$ServiceStopPollMilliseconds = 250
# --- END service ---

$TempDir = $null
$OperationLockStream = $null
$OperationLockHeld = $false
$LifecycleLockStream = $null
$LifecycleLockHeld = $false
$Succeeded = $false
$InstallFailure = $null
$BinaryChanged = $false
$OldBinary = $null
$InstallCandidate = $null
$TaskSnapshotXml = $null
$TaskExisted = $false
$TaskWasRunning = $false
$TaskWasEnabled = $false
$TaskChanged = $false
$ServiceTouched = $false
$FreshInstall = $false
$MigrationNonce = $null
$MigrationStarted = $false
$UninstallStarted = $false
$AppDirExisted = Test-Path -LiteralPath $AppDir -PathType Container
$DataDirExisted = Test-Path -LiteralPath $DataDir -PathType Container
$CosignDirExisted = Test-Path -LiteralPath $CosignDir -PathType Container
$CosignInstalled = $false
$OriginalUserPath = [Environment]::GetEnvironmentVariable("Path", "User")
$UserPathChanged = $false
$OriginalState = $null
$OriginalStateExisted = $false
$CurrentState = $null
$StateChanged = $false
$InstallationEpoch = ""
$TransitionPhase = ""
$RecoveringTransition = $false
$CachedInstallerChanged = $false
$CachedInstallerExisted = $false
$OldCachedInstaller = $null
$CachedInstallerBundleExisted = $false
$OldCachedInstallerBundle = $null
# --- BEGIN update ---
$OldReleaseUrl = $null
$ReleaseUrlExisted = $false
$ReleaseUrlChanged = $false
# --- END update ---

function Write-Step {
    param([Parameter(Mandatory = $true)][string]$Message)
    Write-Host $Message
}

function Write-Logo {
    $logo = @'
 ______     ______   ______     ______     __  __     ______
/\  ___\   /\  == \ /\  == \   /\  __ \   /\ \/\ \   /\__  _\
\ \___  \  \ \  _-/ \ \  __<   \ \ \/\ \  \ \ \_\ \  \/_/\ \/
 \/\_____\  \ \_\    \ \_\ \_\  \ \_____\  \ \_____\    \ \_\
  \/_____/   \/_/     \/_/ /_/   \/_____/   \/_____/     \/_/
'@
    Write-Host $logo
}

function Invoke-NativeChecked {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$FailureMessage
    )

    # Windows PowerShell 5.1 turns native stderr into PowerShell error records.
    # Some tools, including cosign, write successful status messages to stderr,
    # so capture both streams without letting ErrorActionPreference=Stop abort
    # before the native exit code can be checked.
    $savedErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $output = & $FilePath @Arguments 2>&1
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedErrorActionPreference
    }
    if ($exitCode -ne 0) {
        $detail = ($output | Out-String).Trim()
        if ($detail) {
            throw "$FailureMessage (exit $exitCode):`n$detail"
        }
        throw "$FailureMessage (exit $exitCode)"
    }
    return $output
}

function Normalize-ReleaseUrl {
    param([Parameter(Mandatory = $true)][string]$Url)

    $trimmed = $Url.Trim().TrimEnd("/")
    if ([string]::IsNullOrWhiteSpace($trimmed)) {
        throw "Release URL is empty."
    }
    $uri = $null
    if (-not [Uri]::TryCreate($trimmed + "/", [UriKind]::Absolute, [ref]$uri)) {
        throw "Release URL is not an absolute URL: $trimmed"
    }
    if ($uri.Scheme -ne "https" -and $uri.Scheme -ne "http") {
        throw "Release URL must use HTTP or HTTPS: $trimmed"
    }
    return $uri.AbsoluteUri
}

function Assert-ReleaseVersion {
    param([Parameter(Mandatory = $true)][string]$Version)

    if ($Version -cnotmatch $SemVerPattern) {
        throw "Release host returned an invalid version pointer: '$Version'."
    }
}

function Download-File {
    param(
        [Parameter(Mandatory = $true)][string]$Url,
        [Parameter(Mandatory = $true)][string]$Destination
    )

    Write-Step "Downloading $Url ..."
    Invoke-WebRequest -Uri $Url -OutFile $Destination -UseBasicParsing -TimeoutSec 300
    if (-not (Test-Path -LiteralPath $Destination -PathType Leaf)) {
        throw "Download did not create '$Destination'."
    }
}

function Get-ExpectedHash {
    param(
        [Parameter(Mandatory = $true)][string]$ChecksumsPath,
        [Parameter(Mandatory = $true)][string]$FileName
    )

    foreach ($line in [IO.File]::ReadAllLines($ChecksumsPath)) {
        if ($line -match "^\s*([0-9A-Fa-f]{64})\s+\*?(.+?)\s*$") {
            if ($Matches[2] -ceq $FileName) {
                return $Matches[1].ToUpperInvariant()
            }
        }
    }
    throw "No valid SHA256 entry for '$FileName' in checksums.txt."
}

function Assert-FileHash {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Expected
    )

    $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToUpperInvariant()
    if ($actual -cne $Expected.ToUpperInvariant()) {
        throw "SHA256 mismatch for '$(Split-Path -Leaf $Path)'. Expected $Expected, got $actual."
    }
}

function Test-FileHash {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Expected
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return $false
    }
    $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
    return [string]::Equals($actual, $Expected, [StringComparison]::OrdinalIgnoreCase)
}

function Expand-Gzip {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination
    )

    $sourceStream = [IO.File]::OpenRead($Source)
    try {
        $gzip = New-Object IO.Compression.GZipStream(
            $sourceStream, [IO.Compression.CompressionMode]::Decompress, $false
        )
        try {
            $output = [IO.File]::Create($Destination)
            try {
                $gzip.CopyTo($output)
                $output.Flush($true)
            } finally {
                $output.Dispose()
            }
        } finally {
            $gzip.Dispose()
        }
    } finally {
        $sourceStream.Dispose()
    }
}

function Get-NativeArchitecture {
    $architecture = $env:PROCESSOR_ARCHITEW6432
    if ([string]::IsNullOrWhiteSpace($architecture)) {
        $architecture = $env:PROCESSOR_ARCHITECTURE
    }

    switch ($architecture.ToUpperInvariant()) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { throw "Unsupported native Windows architecture '$architecture'. Only AMD64 and ARM64 are supported." }
    }
}

# Resolve a cosign to verify with: prefer an existing cosign on PATH, then a
# previously bootstrapped per-user copy, otherwise download the pinned release
# (sha256 baked in at release time) into %LOCALAPPDATA%\Programs\cosign.
function Resolve-Cosign {
    $existing = Get-Command -Name "cosign" -CommandType Application -ErrorAction SilentlyContinue
    if ($null -ne $existing) {
        $isManaged = [string]::Equals(
            [IO.Path]::GetFullPath($existing.Source),
            [IO.Path]::GetFullPath($CosignBin),
            [StringComparison]::OrdinalIgnoreCase
        )
        if (-not $isManaged) {
            return $existing.Source
        }
    }
    if (Test-Path -LiteralPath $CosignDir) {
        Protect-OwnedDirectory -Path $CosignDir -Expected $CosignDir
    }
    if (Test-FileHash -Path $CosignBin -Expected $CosignSha256) {
        return $CosignBin
    }
    if (Test-Path -LiteralPath $CosignBin -PathType Leaf) {
        Write-Warning "Cached managed cosign failed checksum verification; replacing it."
    }

    Write-Step "Installing cosign $CosignVersion to $CosignBin ..."
    $temporaryCosign = Join-Path $TempDir "cosign.exe"
    # cosign publishes no windows-arm64 asset; the amd64 binary runs under
    # Windows-on-ARM emulation.
    Download-File `
        -Url "https://github.com/sigstore/cosign/releases/download/$CosignVersion/cosign-windows-amd64.exe" `
        -Destination $temporaryCosign
    Assert-FileHash -Path $temporaryCosign -Expected $CosignSha256

    New-Item -ItemType Directory -Path $CosignDir -Force | Out-Null
    Protect-OwnedDirectory -Path $CosignDir -Expected $CosignDir
    Publish-FileAtomically -Source $temporaryCosign -Destination $CosignBin
    $script:CosignInstalled = $true
    Add-UserPath -Directories @($CosignDir)
    return $CosignBin
}

# Returns live app processes recorded in the instances directory whose
# executable still matches the installed binary. Stale and unrelated markers
# are deliberately ignored until lifecycle exclusivity proves they are safe to
# remove.
function Get-MatchingMarkerProcesses {
    param(
        [Parameter(Mandatory = $true)][string]$MarkersPath,
        [Parameter(Mandatory = $true)][string]$ExpectedExecutable
    )

    if (-not (Test-Path -LiteralPath $MarkersPath -PathType Container)) {
        return @()
    }

    $expected = [IO.Path]::GetFullPath($ExpectedExecutable)
    $matchingProcesses = New-Object System.Collections.Generic.List[object]
    foreach ($marker in Get-ChildItem -LiteralPath $MarkersPath -File -ErrorAction SilentlyContinue) {
        $pidValue = 0
        if (-not [int]::TryParse($marker.Name, [ref]$pidValue) -or $pidValue -le 0) {
            continue
        }

        $process = Get-Process -Id $pidValue -ErrorAction SilentlyContinue
        if ($null -eq $process) {
            continue
        }

        $actual = $null
        try {
            $actual = [IO.Path]::GetFullPath($process.Path)
        } catch {
            continue
        }
        if (-not [string]::Equals($actual, $expected, [StringComparison]::OrdinalIgnoreCase)) {
            continue
        }

        $matchingProcesses.Add([PSCustomObject]@{
            Id = $pidValue
            Path = $actual
        })
    }
    return $matchingProcesses.ToArray()
}

# The lifecycle transition is the graceful stop request: every Go process
# polls state.json and cancels its root context. Give those processes time to
# remove their marker, then revalidate executable identity immediately before
# force-stopping survivors.
function Stop-MarkerProcesses {
    param(
        [Parameter(Mandatory = $true)][string]$MarkersPath,
        [Parameter(Mandatory = $true)][string]$ExpectedExecutable,
        [int]$GraceSeconds = 15
    )

    $deadline = [DateTime]::UtcNow.AddSeconds($GraceSeconds)
    do {
        $matchingProcesses = @(Get-MatchingMarkerProcesses `
            -MarkersPath $MarkersPath `
            -ExpectedExecutable $ExpectedExecutable)
        if ($matchingProcesses.Count -eq 0) {
            return
        }
        if ([DateTime]::UtcNow -ge $deadline) {
            break
        }
        Start-Sleep -Milliseconds 250
    } while ($true)

    $expected = [IO.Path]::GetFullPath($ExpectedExecutable)
    foreach ($match in @(Get-MatchingMarkerProcesses `
        -MarkersPath $MarkersPath `
        -ExpectedExecutable $ExpectedExecutable)) {
        $pidValue = [int]$match.Id
        $process = Get-Process -Id $pidValue -ErrorAction SilentlyContinue
        if ($null -eq $process) {
            continue
        }
        try {
            $actual = [IO.Path]::GetFullPath($process.Path)
        } catch {
            continue
        }
        if (-not [string]::Equals($actual, $expected, [StringComparison]::OrdinalIgnoreCase)) {
            continue
        }

        Write-Step "Force-stopping unresponsive $AppName process $pidValue ..."
        Stop-Process -Id $pidValue -Force -ErrorAction SilentlyContinue
        $deadline = [DateTime]::UtcNow.AddSeconds(30)
        while ((Get-Process -Id $pidValue -ErrorAction SilentlyContinue) -and
            [DateTime]::UtcNow -lt $deadline) {
            Start-Sleep -Milliseconds 250
        }
        if (Get-Process -Id $pidValue -ErrorAction SilentlyContinue) {
            throw "Process $pidValue did not stop."
        }
    }
}

function Clear-MarkerFiles {
    param([Parameter(Mandatory = $true)][string]$MarkersPath)

    if (-not (Test-Path -LiteralPath $MarkersPath -PathType Container)) {
        return
    }
    Get-ChildItem -LiteralPath $MarkersPath -File -Force -ErrorAction Stop |
        Remove-Item -Force -ErrorAction Stop
}

function Acquire-ExclusiveLock {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][int]$TimeoutSeconds,
        [Parameter(Mandatory = $true)][string]$Description
    )

    if (Test-Path -LiteralPath $Path) {
        $lockItem = Get-Item -LiteralPath $Path -Force
        if ($lockItem.PSIsContainer -or
            (($lockItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
            throw "$Description lock is not a regular file: '$Path'."
        }
    }
    $stream = New-Object IO.FileStream(
        $Path,
        [IO.FileMode]::OpenOrCreate,
        [IO.FileAccess]::ReadWrite,
        [IO.FileShare]::ReadWrite
    )
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        try {
            # Interoperates with the application's LockFileEx locks on byte zero.
            $stream.Lock(0, 1)
            return $stream
        } catch [IO.IOException] {
            if ([DateTime]::UtcNow -ge $deadline) {
                $stream.Dispose()
                throw "Timed out after $TimeoutSeconds seconds waiting for the $Description lock '$Path'."
            }
            Start-Sleep -Milliseconds 250
        }
    } while ($true)
}

function Release-LifecycleLock {
    if ($null -eq $script:LifecycleLockStream) {
        return
    }
    if ($script:LifecycleLockHeld) {
        $script:LifecycleLockStream.Unlock(0, 1)
        $script:LifecycleLockHeld = $false
    }
    $script:LifecycleLockStream.Dispose()
    $script:LifecycleLockStream = $null
}

function New-RandomToken {
    $bytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $rng.GetBytes($bytes)
    } finally {
        $rng.Dispose()
    }
    return ([BitConverter]::ToString($bytes)).Replace("-", "").ToLowerInvariant()
}

function Read-MaintenanceState {
    if (-not (Test-Path -LiteralPath $StateFile)) {
        return $null
    }
    $item = Get-Item -LiteralPath $StateFile -Force
    if ($item.PSIsContainer -or
        (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
        throw "Maintenance state is not a regular file: '$StateFile'."
    }
    try {
        $state = [IO.File]::ReadAllText($StateFile) | ConvertFrom-Json
    } catch {
        throw "Maintenance state is malformed: $($_.Exception.Message)"
    }
    $required = @("phase", "version", "targetVersion", "nonce", "changedAt", "installationEpoch")
    $names = @($state.PSObject.Properties.Name)
    if ($names.Count -ne $required.Count) {
        throw "Maintenance state contains unknown or duplicate fields."
    }
    foreach ($name in $required) {
        if ($names -cnotcontains $name -or $state.$name -isnot [string]) {
            throw "Maintenance state field '$name' is missing or is not a string."
        }
    }
    if (@("installing", "updating", "ready", "uninstalling", "uninstalled") -cnotcontains $state.phase) {
        throw "Maintenance state has unknown phase '$($state.phase)'."
    }
    if ($state.changedAt -cnotmatch '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$') {
        throw "Maintenance state changedAt is not RFC3339."
    }
    $parsedChangedAt = [DateTimeOffset]::MinValue
    if (-not [DateTimeOffset]::TryParse($state.changedAt, [ref]$parsedChangedAt)) {
        throw "Maintenance state has invalid changedAt '$($state.changedAt)'."
    }
    if ([string]::IsNullOrWhiteSpace($state.installationEpoch) -or
        $state.installationEpoch.Length -gt 256) {
        throw "Maintenance state has invalid installationEpoch."
    }
    if (-not [string]::IsNullOrEmpty($state.nonce) -and
        $state.nonce -cnotmatch '^[0-9a-f]{64}$') {
        throw "Maintenance state has invalid nonce."
    }
    if ((-not [string]::IsNullOrEmpty($state.version) -and $state.version -cnotmatch $SemVerPattern) -or
        (-not [string]::IsNullOrEmpty($state.targetVersion) -and $state.targetVersion -cnotmatch $SemVerPattern)) {
        throw "Maintenance state contains an invalid semantic version."
    }
    switch ($state.phase) {
        "installing" {
            if ([string]::IsNullOrEmpty($state.targetVersion) -or [string]::IsNullOrEmpty($state.nonce)) {
                throw "Installing state requires targetVersion and nonce."
            }
        }
        "updating" {
            if ([string]::IsNullOrEmpty($state.version) -or
                [string]::IsNullOrEmpty($state.targetVersion) -or
                [string]::IsNullOrEmpty($state.nonce)) {
                throw "Updating state requires version, targetVersion, and nonce."
            }
        }
        "ready" {
            if ([string]::IsNullOrEmpty($state.version) -or
                -not [string]::IsNullOrEmpty($state.targetVersion) -or
                -not [string]::IsNullOrEmpty($state.nonce)) {
                throw "Ready state requires version and empty targetVersion/nonce."
            }
        }
        "uninstalling" {
            if ([string]::IsNullOrEmpty($state.version) -or
                -not [string]::IsNullOrEmpty($state.targetVersion) -or
                -not [string]::IsNullOrEmpty($state.nonce)) {
                throw "Uninstalling state requires version and empty targetVersion/nonce."
            }
        }
        "uninstalled" {
            if (-not [string]::IsNullOrEmpty($state.version) -or
                -not [string]::IsNullOrEmpty($state.targetVersion) -or
                -not [string]::IsNullOrEmpty($state.nonce)) {
                throw "Uninstalled state must have empty version/targetVersion/nonce."
            }
        }
    }
    return $state
}

function Write-MaintenanceState {
    param(
        [Parameter(Mandatory = $true)][string]$Phase,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Version,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$TargetVersion,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Nonce,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$InstallationEpoch,
        [AllowEmptyString()][string]$ChangedAt = ""
    )

    if (@("installing", "updating", "ready", "uninstalling", "uninstalled") -cnotcontains $Phase) {
        throw "Cannot write unknown maintenance phase '$Phase'."
    }
    if ([string]::IsNullOrEmpty($ChangedAt)) {
        $ChangedAt = [DateTimeOffset]::UtcNow.ToString("o", [Globalization.CultureInfo]::InvariantCulture)
    }
    New-Item -ItemType Directory -Path $ControlDir -Force | Out-Null
    $payload = [ordered]@{
        phase = $Phase
        version = $Version
        targetVersion = $TargetVersion
        nonce = $Nonce
        changedAt = $ChangedAt
        installationEpoch = $InstallationEpoch
    } | ConvertTo-Json -Compress
    $stateTemp = Join-Path $ControlDir (".state." + [Guid]::NewGuid().ToString("N") + ".tmp")
    $stateBackup = Join-Path $ControlDir (".state." + [Guid]::NewGuid().ToString("N") + ".bak")
    try {
        $utf8 = New-Object Text.UTF8Encoding($false)
        $bytes = $utf8.GetBytes($payload + "`n")
        $stream = New-Object IO.FileStream(
            $stateTemp,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None,
            4096,
            [IO.FileOptions]::WriteThrough
        )
        try {
            $stream.Write($bytes, 0, $bytes.Length)
            $stream.Flush($true)
        } finally {
            $stream.Dispose()
        }

        if (Test-Path -LiteralPath $StateFile) {
            $existingState = Get-Item -LiteralPath $StateFile -Force
            if ($existingState.PSIsContainer -or
                (($existingState.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
                throw "Existing maintenance state is not a regular file: '$StateFile'."
            }
            [IO.File]::Replace($stateTemp, $StateFile, $stateBackup, $true)
            $script:StateChanged = $true
            Remove-Item -LiteralPath $stateBackup -Force -ErrorAction SilentlyContinue
        } else {
            [IO.File]::Move($stateTemp, $StateFile)
            $script:StateChanged = $true
        }
    } finally {
        if (Test-Path -LiteralPath $stateTemp) {
            Remove-Item -LiteralPath $stateTemp -Force -ErrorAction SilentlyContinue
        }
        if (Test-Path -LiteralPath $stateBackup) {
            Remove-Item -LiteralPath $stateBackup -Force -ErrorAction SilentlyContinue
        }
    }
}

function Restore-OriginalState {
    if (-not $StateChanged) {
        return
    }
    if ($OriginalStateExisted) {
        Write-MaintenanceState `
            -Phase $OriginalState.phase `
            -Version $OriginalState.version `
            -TargetVersion $OriginalState.targetVersion `
            -Nonce $OriginalState.nonce `
            -InstallationEpoch $OriginalState.installationEpoch `
            -ChangedAt $OriginalState.changedAt
    } elseif (Test-Path -LiteralPath $StateFile -PathType Leaf) {
        Remove-Item -LiteralPath $StateFile -Force
    }
    $script:StateChanged = $false
}

function Publish-FileAtomically {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination
    )

    $directory = Split-Path -Parent $Destination
    New-Item -ItemType Directory -Path $directory -Force | Out-Null
    $staged = Join-Path $directory ((Split-Path -Leaf $Destination) + "." + [Guid]::NewGuid().ToString("N") + ".tmp")
    $backup = $staged + ".bak"
    try {
        Copy-Item -LiteralPath $Source -Destination $staged
        if (Test-Path -LiteralPath $Destination -PathType Leaf) {
            $existing = Get-Item -LiteralPath $Destination -Force
            if (($existing.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "Refusing to replace reparse-point file '$Destination'."
            }
            [IO.File]::Replace($staged, $Destination, $backup, $true)
        } else {
            [IO.File]::Move($staged, $Destination)
        }
    } finally {
        if (Test-Path -LiteralPath $staged) {
            Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
        }
        if (Test-Path -LiteralPath $backup) {
            Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue
        }
    }
}

function Add-UserPath {
    param([Parameter(Mandatory = $true)][string[]]$Directories)

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = New-Object System.Collections.Generic.List[string]
    if (-not [string]::IsNullOrWhiteSpace($userPath)) {
        foreach ($part in $userPath.Split(";")) {
            if (-not [string]::IsNullOrWhiteSpace($part)) {
                $parts.Add($part.Trim())
            }
        }
    }

    foreach ($directory in $Directories) {
        $present = $false
        foreach ($part in $parts) {
            if ([string]::Equals(
                $part.TrimEnd("\"), $directory.TrimEnd("\"),
                [StringComparison]::OrdinalIgnoreCase
            )) {
                $present = $true
                break
            }
        }
        if (-not $present) {
            $parts.Insert(0, $directory)
            $script:UserPathChanged = $true
        }

        # also update this session so post-install commands work immediately
        $currentParts = $env:Path.Split(";")
        if (-not ($currentParts | Where-Object {
            [string]::Equals(
                $_.TrimEnd("\"), $directory.TrimEnd("\"),
                [StringComparison]::OrdinalIgnoreCase
            )
        })) {
            $env:Path = "$directory;$env:Path"
        }
    }

    if ($UserPathChanged) {
        [Environment]::SetEnvironmentVariable("Path", ($parts -join ";"), "User")
    }
}

function Remove-UserPathEntry {
    param([Parameter(Mandatory = $true)][string]$Directory)

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ([string]::IsNullOrWhiteSpace($userPath)) {
        return
    }
    $kept = New-Object System.Collections.Generic.List[string]
    $removed = $false
    foreach ($part in $userPath.Split([char]";")) {
        if ([string]::Equals(
            $part.Trim().TrimEnd("\"),
            $Directory.TrimEnd("\"),
            [StringComparison]::OrdinalIgnoreCase
        )) {
            $removed = $true
            continue
        }
        $kept.Add($part)
    }
    if ($removed) {
        [Environment]::SetEnvironmentVariable("Path", ($kept -join ";"), "User")
    }
}

function Assert-SafeOwnedDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Expected
    )

    $actualFull = [IO.Path]::GetFullPath($Path).TrimEnd("\")
    $expectedFull = [IO.Path]::GetFullPath($Expected).TrimEnd("\")
    if (-not [string]::Equals($actualFull, $expectedFull, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove unexpected path '$actualFull'; expected '$expectedFull'."
    }
    if (-not (Test-Path -LiteralPath $actualFull)) {
        return
    }
    $item = Get-Item -LiteralPath $actualFull -Force
    if (-not $item.PSIsContainer -or
        (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
        throw "Refusing to remove non-directory or reparse-point path '$actualFull'."
    }
    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $ownerSid = (Get-Acl -LiteralPath $actualFull).GetOwner(
        [Security.Principal.SecurityIdentifier]
    )
    if (-not [string]::Equals(
        $ownerSid.Value,
        $currentSid.Value,
        [StringComparison]::OrdinalIgnoreCase
    )) {
        throw "Refusing directory '$actualFull' owned by SID '$($ownerSid.Value)'; expected '$($currentSid.Value)'."
    }
}

function Protect-OwnedDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Expected
    )

    Assert-SafeOwnedDirectory -Path $Path -Expected $Expected
    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetSecurityDescriptorSddlForm(
        "D:P(A;OICI;FA;;;$currentSid)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
        [Security.AccessControl.AccessControlSections]::Access
    )
    ([IO.DirectoryInfo]$Path).SetAccessControl($acl)
}

function Remove-OwnedDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Expected
    )

    Assert-SafeOwnedDirectory -Path $Path -Expected $Expected
    if (Test-Path -LiteralPath $Path -PathType Container) {
        Remove-Item -LiteralPath $Path -Recurse -Force
    }
}

# --- BEGIN service ---
function Test-AppTaskStopped {
    $state = Get-TaskState
    return ($null -eq $state -or $state -eq "Ready" -or $state -eq "Disabled")
}

function Test-AppTaskActive {
    return (-not (Test-AppTaskStopped))
}

function Write-ServiceStopLease {
    New-Item -ItemType Directory -Path $ControlDir -Force | Out-Null
    $expires = [DateTimeOffset]::UtcNow.AddSeconds($ServiceStopLeaseSeconds).ToUnixTimeMilliseconds()
    [IO.File]::WriteAllText(
        $ServiceStopLease,
        $expires.ToString([Globalization.CultureInfo]::InvariantCulture) + "`n"
    )
}

function Clear-ServiceStopLease {
    if (Test-Path -LiteralPath $ServiceStopLease -PathType Leaf) {
        [IO.File]::WriteAllText($ServiceStopLease, "0`n")
    }
}

function Wait-AppTaskStopped {
    param([Parameter(Mandatory = $true)][int]$TimeoutSeconds)

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ((Test-AppTaskActive) -and [DateTime]::UtcNow -lt $deadline) {
        Start-Sleep -Milliseconds $ServiceStopPollMilliseconds
    }
    return (-not (Test-AppTaskActive))
}

function Get-TaskState {
    $task = Get-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName -ErrorAction SilentlyContinue
    if ($null -eq $task) {
        return $null
    }
    return $task.State.ToString()
}

function Stop-AppTask {
    if (-not (Test-AppTaskActive)) {
        Clear-ServiceStopLease
        return
    }

    $leaseWritten = $false
    try {
        try {
            Write-Step "Requesting graceful shutdown of scheduled task '$TaskName' ..."
            Write-ServiceStopLease
            $leaseWritten = $true
        } catch {
            Write-Warning "Could not write the graceful stop request; using Task Scheduler fallback. Detail: $($_.Exception.Message)"
        }

        if ($leaseWritten -and (Wait-AppTaskStopped -TimeoutSeconds $ServiceGracefulStopTimeoutSeconds)) {
            Write-Step "Scheduled task '$TaskName' stopped gracefully."
            return
        }

        if (Test-AppTaskActive) {
            Write-Step "Graceful shutdown timed out; forcing scheduled task '$TaskName' to stop ..."
            try {
                Stop-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName -ErrorAction Stop
            } catch {
                if (Test-AppTaskActive) {
                    throw
                }
            }
        }
        if (-not (Wait-AppTaskStopped -TimeoutSeconds $ServiceForcedStopTimeoutSeconds)) {
            throw "Scheduled task '$TaskName' did not stop."
        }
    } finally {
        Clear-ServiceStopLease
    }
}

# Registers (or replaces) the per-user logon task with the logged-in user's
# interactive token. The service's automatic update checker needs that token's
# network access. The direct app action avoids a visible PowerShell wrapper,
# stores no password, and retains a limited run level.
function Register-AppTask {
    $currentUser = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    $action = New-ScheduledTaskAction -Execute $AppBin -Argument $ServiceArgs.Trim() -WorkingDirectory $DataDir
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $currentUser
    $settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries `
        -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -RestartCount 3 `
        -RestartInterval (New-TimeSpan -Minutes 1) `
        -MultipleInstances IgnoreNew

    $principal = New-ScheduledTaskPrincipal `
        -UserId $currentUser `
        -LogonType Interactive `
        -RunLevel Limited
    Register-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName -Action $action -Trigger $trigger `
        -Settings $settings -Principal $principal -Description $ServiceDescription -Force | Out-Null
    $script:TaskChanged = $true
}
# --- END service ---

# --- BEGIN service.https ---
function Test-TcpPortInUse {
    param([Parameter(Mandatory = $true)][int]$Port)

    # Match Go's wildcard TCP listener with one dual-stack, exclusive bind.
    # This checks ownership rather than connecting to an unrelated service.
    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::IPv6Any, $Port)
    try {
        $listener.Server.DualMode = $true
        $listener.Server.ExclusiveAddressUse = $true
        $listener.Start()
        return $false
    } catch {
        $cause = $_.Exception.GetBaseException()
        if ($cause -is [Net.Sockets.SocketException] -and
            $cause.SocketErrorCode -eq [Net.Sockets.SocketError]::AddressAlreadyInUse) {
            return $true
        }
        throw "Failed to check TCP port $Port availability: $($cause.Message)"
    } finally {
        $listener.Stop()
    }
}

function Test-TcpReady {
    param(
        [Parameter(Mandatory = $true)][string]$HostName,
        [Parameter(Mandatory = $true)][int]$Port
    )

    $client = New-Object Net.Sockets.TcpClient
    try {
        $result = $client.BeginConnect($HostName, $Port, $null, $null)
        if (-not $result.AsyncWaitHandle.WaitOne(500)) {
            return $false
        }
        $client.EndConnect($result)
        return $true
    } catch {
        return $false
    } finally {
        $client.Dispose()
    }
}
# --- END service.https ---

# --- BEGIN service ---
function Wait-AppReady {
    param(
        [Parameter(Mandatory = $true)][int]$Port,
        [Parameter(Mandatory = $true)][int]$TimeoutSeconds
    )

    $seenRunning = $false
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        $state = Get-TaskState
        if ($state -eq "Running") {
            $seenRunning = $true
            $ready = $true
            # --- BEGIN service.https ---
            $ready = (-not $FreshInstall) -or (Test-TcpReady -HostName "127.0.0.1" -Port $Port)
            # --- END service.https ---
            if ($ready) {
                return
            }
        } elseif ($state -eq "Ready" -and $seenRunning) {
            # task instance started, then exited
            $target = "service readiness"
            # --- BEGIN service.https ---
            $target = "TCP $Port"
            # --- END service.https ---
            throw "Scheduled task '$TaskName' stopped before becoming ready on $target."
        }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)

    $target = "service readiness"
    # --- BEGIN service.https ---
    $target = "TCP $Port readiness"
    # --- END service.https ---
    throw "Timed out waiting for scheduled task '$TaskName' and $target."
}
# --- END service ---

function Assert-MaintenanceRequest {
    $expectedEpoch = ""
    if ($null -ne $env:APP_MAINTENANCE_EXPECT_EPOCH) {
        $expectedEpoch = $env:APP_MAINTENANCE_EXPECT_EPOCH.Trim()
    }
    $expectedVersion = ""
    if ($null -ne $env:APP_MAINTENANCE_EXPECT_VERSION) {
        $expectedVersion = $env:APP_MAINTENANCE_EXPECT_VERSION.Trim()
    }

    if ($Update) {
        if ([string]::IsNullOrEmpty($expectedEpoch) -or [string]::IsNullOrEmpty($expectedVersion)) {
            throw "-Update requires APP_MAINTENANCE_EXPECT_EPOCH and APP_MAINTENANCE_EXPECT_VERSION."
        }
        if ($null -eq $CurrentState -or $CurrentState.phase -cne "ready") {
            throw "Update requires maintenance state phase 'ready'."
        }
        if ($CurrentState.installationEpoch -cne $expectedEpoch) {
            throw "Update request belongs to a different installation epoch."
        }
        if ($CurrentState.version -cne $expectedVersion) {
            throw "Update request expected version '$expectedVersion', but state contains '$($CurrentState.version)'."
        }
        return
    }

    if ($Uninstall) {
        if (-not [string]::IsNullOrEmpty($expectedVersion) -and [string]::IsNullOrEmpty($expectedEpoch)) {
            throw "APP_MAINTENANCE_EXPECT_VERSION cannot be used without APP_MAINTENANCE_EXPECT_EPOCH."
        }
        if ($null -eq $CurrentState) {
            throw "Uninstall requires an existing maintenance state."
        }
        if (-not [string]::IsNullOrEmpty($expectedEpoch)) {
            if ($CurrentState.phase -cne "ready") {
                throw "Detached uninstall requires maintenance state phase 'ready'."
            }
            if ($CurrentState.installationEpoch -cne $expectedEpoch) {
                throw "Uninstall request belongs to a different installation epoch."
            }
        }
        return
    }

    if ($null -eq $CurrentState) {
        if (Test-Path -LiteralPath $AppBin -PathType Leaf) {
            throw "Installed binary exists without maintenance state; refusing an ambiguous install."
        }
        return
    }
    if ($CurrentState.phase -ceq "uninstalling") {
        throw "Uninstall is incomplete; rerun the cached installer with -Uninstall first."
    }
}

function Start-InstallTransition {
    param([Parameter(Mandatory = $true)][string]$TargetVersion)

    $currentVersion = ""
    if ($Update) {
        $script:TransitionPhase = "updating"
        $script:InstallationEpoch = $CurrentState.installationEpoch
        $currentVersion = $CurrentState.version
    } elseif ($null -eq $CurrentState -or $CurrentState.phase -ceq "uninstalled") {
        $script:TransitionPhase = "installing"
        $script:InstallationEpoch = New-RandomToken
    } elseif ($CurrentState.phase -ceq "ready") {
        $script:TransitionPhase = "updating"
        $script:InstallationEpoch = $CurrentState.installationEpoch
        $currentVersion = $CurrentState.version
    } elseif ($CurrentState.phase -ceq "installing" -or $CurrentState.phase -ceq "updating") {
        $script:TransitionPhase = $CurrentState.phase
        $script:RecoveringTransition = $true
        $script:InstallationEpoch = $CurrentState.installationEpoch
        $currentVersion = $CurrentState.version
    } else {
        throw "Cannot install while maintenance state is '$($CurrentState.phase)'."
    }
    if ([string]::IsNullOrEmpty($InstallationEpoch)) {
        throw "Maintenance state is missing its installation epoch."
    }
    $script:MigrationNonce = New-RandomToken
    Write-MaintenanceState `
        -Phase $TransitionPhase `
        -Version $currentVersion `
        -TargetVersion $TargetVersion `
        -Nonce $MigrationNonce `
        -InstallationEpoch $InstallationEpoch
}

function Invoke-Uninstall {
    if ($CurrentState.phase -ceq "uninstalled") {
        Write-Step "$AppName is already uninstalled."
        return
    }
    if ([string]::IsNullOrEmpty($CurrentState.installationEpoch)) {
        throw "Maintenance state is missing its installation epoch."
    }

    $uninstallVersion = $CurrentState.version
    if ([string]::IsNullOrEmpty($uninstallVersion) -and
        $CurrentState.phase -ceq "installing") {
        $uninstallVersion = $CurrentState.targetVersion
    }
    if ([string]::IsNullOrEmpty($uninstallVersion)) {
        throw "Maintenance state has no installed or target version to uninstall."
    }

    Write-MaintenanceState `
        -Phase "uninstalling" `
        -Version $uninstallVersion `
        -TargetVersion "" `
        -Nonce "" `
        -InstallationEpoch $CurrentState.installationEpoch
    $script:UninstallStarted = $true

    Write-Step "Draining running $AppName processes ..."
    Stop-MarkerProcesses `
        -MarkersPath $InstancesDir `
        -ExpectedExecutable $AppBin `
        -GraceSeconds 15

    # --- BEGIN service ---
    if ($TaskExisted) {
        $script:ServiceTouched = $true
        Stop-AppTask
    }
    # --- END service ---

    # Revalidate after the service controller ran, then force any remaining
    # CLI or unmanaged service process before taking lifecycle exclusivity.
    Stop-MarkerProcesses `
        -MarkersPath $InstancesDir `
        -ExpectedExecutable $AppBin `
        -GraceSeconds 0

    Write-Step "Acquiring lifecycle lock ..."
    $script:LifecycleLockStream = Acquire-ExclusiveLock `
        -Path $LifecycleLockFile `
        -TimeoutSeconds $LockTimeoutSeconds `
        -Description "lifecycle"
    $script:LifecycleLockHeld = $true
    Clear-MarkerFiles -MarkersPath $InstancesDir

    # --- BEGIN service ---
    if ($TaskExisted) {
        Write-Step "Removing scheduled task '$TaskName' ..."
        Unregister-ScheduledTask `
            -TaskPath $TaskPath `
            -TaskName $TaskName `
            -Confirm:$false `
            -ErrorAction Stop
    }
    # --- END service ---

    Write-Step "Removing application PATH entry ..."
    Remove-UserPathEntry -Directory $AppDir

    if (Test-Path -LiteralPath $DataDir) {
        Write-Step "Removing application data '$DataDir' ..."
        Remove-OwnedDirectory -Path $DataDir -Expected (Join-Path $StorageDir "data")
    }
    if (Test-Path -LiteralPath $AppDir) {
        Write-Step "Removing application binary directory '$AppDir' ..."
        Remove-OwnedDirectory `
            -Path $AppDir `
            -Expected (Join-Path (Join-Path $LocalAppData "Programs") $AppDirName)
    }

    Write-MaintenanceState `
        -Phase "uninstalled" `
        -Version "" `
        -TargetVersion "" `
        -Nonce "" `
        -InstallationEpoch $CurrentState.installationEpoch
    Release-LifecycleLock

    Write-Host ""
    Write-Host "Uninstalled: $AppName"
    Write-Host "Retained:    $ControlDir"
    Write-Host "Retained:    $MaintenanceDir"
    Write-Host "Retained:    $LogsDir"
}

function Rollback-Install {
    $rollbackErrors = New-Object System.Collections.Generic.List[string]
    Write-Warning "Installation failed; rolling back changes..."

    # --- BEGIN service ---
    if ($ServiceTouched) {
        try {
            Stop-AppTask
        } catch {
            $rollbackErrors.Add("stop task: $($_.Exception.Message)")
        }
    }
    # --- END service ---

    try {
        if ($BinaryChanged) {
            if ($null -ne $OldBinary -and (Test-Path -LiteralPath $OldBinary -PathType Leaf)) {
                Copy-Item -LiteralPath $OldBinary -Destination $AppBin -Force
            } elseif (Test-Path -LiteralPath $AppBin -PathType Leaf) {
                Remove-Item -LiteralPath $AppBin -Force
            }
        }
        if ($null -ne $InstallCandidate -and (Test-Path -LiteralPath $InstallCandidate -PathType Leaf)) {
            Remove-Item -LiteralPath $InstallCandidate -Force
        }
    } catch {
        $rollbackErrors.Add("binary: $($_.Exception.Message)")
    }

    # --- BEGIN service ---
    if ($ServiceTouched) {
        try {
            if ($TaskChanged) {
                if ($TaskExisted -and $null -ne $TaskSnapshotXml) {
                    Register-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName -Xml $TaskSnapshotXml -Force | Out-Null
                } elseif (-not $TaskExisted) {
                    Unregister-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
                }
            }
        } catch {
            $rollbackErrors.Add("scheduled task: $($_.Exception.Message)")
        }
    }
    # --- END service ---

    # --- BEGIN update ---
    try {
        if ($ReleaseUrlChanged) {
            if ($ReleaseUrlExisted -and $null -ne $OldReleaseUrl) {
                [IO.File]::WriteAllText($ReleaseUrlFile, $OldReleaseUrl)
            } elseif (-not $ReleaseUrlExisted -and (Test-Path -LiteralPath $ReleaseUrlFile -PathType Leaf)) {
                Remove-Item -LiteralPath $ReleaseUrlFile -Force
            }
        }
    } catch {
        $rollbackErrors.Add("release-url: $($_.Exception.Message)")
    }
    # --- END update ---

    try {
        if ($CachedInstallerChanged) {
            if ($CachedInstallerBundleExisted -and $null -ne $OldCachedInstallerBundle) {
                Publish-FileAtomically -Source $OldCachedInstallerBundle -Destination $CachedInstallerBundle
            } elseif (Test-Path -LiteralPath $CachedInstallerBundle -PathType Leaf) {
                Remove-Item -LiteralPath $CachedInstallerBundle -Force
            }
            if ($CachedInstallerExisted -and $null -ne $OldCachedInstaller) {
                Publish-FileAtomically -Source $OldCachedInstaller -Destination $CachedInstaller
            } elseif (Test-Path -LiteralPath $CachedInstaller -PathType Leaf) {
                Remove-Item -LiteralPath $CachedInstaller -Force
            }
        }
    } catch {
        $rollbackErrors.Add("maintenance cache: $($_.Exception.Message)")
    }

    try {
        if ($CosignInstalled -and (Test-Path -LiteralPath $CosignBin -PathType Leaf)) {
            Remove-Item -LiteralPath $CosignBin -Force
        }
    } catch {
        $rollbackErrors.Add("cosign: $($_.Exception.Message)")
    }

    if ($UserPathChanged) {
        try {
            [Environment]::SetEnvironmentVariable("Path", $OriginalUserPath, "User")
        } catch {
            $rollbackErrors.Add("user PATH: $($_.Exception.Message)")
        }
    }

    try {
        Restore-OriginalState
    } catch {
        $rollbackErrors.Add("maintenance state: $($_.Exception.Message)")
    }

    try {
        Release-LifecycleLock
    } catch {
        $rollbackErrors.Add("lifecycle lock: $($_.Exception.Message)")
    }

    # --- BEGIN service ---
    if ($ServiceTouched -and $TaskExisted -and $TaskWasRunning -and -not $LifecycleLockHeld) {
        try {
            Clear-ServiceStopLease
            Start-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName
        } catch {
            $rollbackErrors.Add("restart task: $($_.Exception.Message)")
        }
    }
    # --- END service ---

    if ($FreshInstall) {
        try {
            if (-not $AppDirExisted -and (Test-Path -LiteralPath $AppDir -PathType Container)) {
                Remove-Item -LiteralPath $AppDir -Recurse -Force
            }
            if (-not $DataDirExisted -and (Test-Path -LiteralPath $DataDir -PathType Container)) {
                Remove-Item -LiteralPath $DataDir -Recurse -Force
            }
            if (-not $CosignDirExisted -and $CosignInstalled -and (Test-Path -LiteralPath $CosignDir -PathType Container)) {
                Remove-Item -LiteralPath $CosignDir -Recurse -Force
            }
        } catch {
            $rollbackErrors.Add("directories: $($_.Exception.Message)")
        }
    }

    if ($rollbackErrors.Count -gt 0) {
        [Console]::Error.WriteLine("Rollback encountered errors:")
        foreach ($rollbackError in $rollbackErrors) {
            [Console]::Error.WriteLine("  - $rollbackError")
        }
    } else {
        Write-Warning "Rollback completed."
    }
}

try {
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT -or
        [Environment]::OSVersion.Version.Build -lt 22000) {
        throw "This installer requires Windows 11 (build 22000 or newer)."
    }
    if ($AppName -match "^<.+>$" -or
        $CertificateIdentity -match "^<.+>$" -or
        $OidcIssuer -match "^<.+>$" -or
        $CosignVersion -match "^<.+>$") {
        throw "This installer template has not been rendered by the release build."
    }

    Write-Logo

    if ($Update -and $Uninstall) {
        throw "-Update and -Uninstall are mutually exclusive."
    }

    New-Item -ItemType Directory -Path $StorageDir -Force | Out-Null
    Protect-OwnedDirectory `
        -Path $StorageDir `
        -Expected (Join-Path $LocalAppData $AppDirName)
    if (Test-Path -LiteralPath $AppDir) {
        Protect-OwnedDirectory `
            -Path $AppDir `
            -Expected (Join-Path (Join-Path $LocalAppData "Programs") $AppDirName)
    }
    foreach ($ownedDirectory in @($ControlDir, $MaintenanceDir, $LogsDir)) {
        New-Item -ItemType Directory -Path $ownedDirectory -Force | Out-Null
        Assert-SafeOwnedDirectory -Path $ownedDirectory -Expected $ownedDirectory
    }
    New-Item -ItemType Directory -Path $InstancesDir -Force | Out-Null
    Assert-SafeOwnedDirectory -Path $InstancesDir -Expected (Join-Path $ControlDir "instances")

    Write-Step "Acquiring maintenance operation lock ..."
    $OperationLockStream = Acquire-ExclusiveLock `
        -Path $OperationLockFile `
        -TimeoutSeconds $LockTimeoutSeconds `
        -Description "maintenance operation"
    $OperationLockHeld = $true

    $CurrentState = Read-MaintenanceState
    if ($null -ne $CurrentState) {
        $OriginalState = $CurrentState
        $OriginalStateExisted = $true
    }
    Assert-MaintenanceRequest

    # --- BEGIN service ---
    # Snapshot the existing scheduled task before either transaction mutates it.
    $existingTask = Get-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName -ErrorAction SilentlyContinue
    if ($null -ne $existingTask) {
        $TaskExisted = $true
        $TaskWasRunning = ($existingTask.State.ToString() -eq "Running")
        $TaskWasEnabled = [bool]$existingTask.Settings.Enabled
        $TaskSnapshotXml = Export-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName
    }
    # --- END service ---

    if ($Uninstall) {
        Invoke-Uninstall
        $Succeeded = $true
    } else {
        New-Item -ItemType Directory -Path $DataDir -Force | Out-Null
        Assert-SafeOwnedDirectory -Path $DataDir -Expected (Join-Path $StorageDir "data")

    [Net.ServicePointManager]::SecurityProtocol =
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $architecture = Get-NativeArchitecture
    $artifactName = "windows-$architecture.exe.gz"
    $releaseUrlSource = $DefaultReleaseUrl
    if (-not [string]::IsNullOrWhiteSpace($env:APP_RELEASE_URL)) {
        $releaseUrlSource = $env:APP_RELEASE_URL
    }
    $releaseUrl = Normalize-ReleaseUrl -Url $releaseUrlSource
    $officialReleaseUrl = Normalize-ReleaseUrl -Url $DefaultReleaseUrl

    $FreshInstall = ($null -eq $CurrentState -or $CurrentState.phase -ceq "uninstalled")
    # --- BEGIN service ---
    $FreshInstall = $FreshInstall -and (-not $TaskExisted)
    # --- END service ---

    $TempDir = Join-Path ([IO.Path]::GetTempPath()) ("$AppName-install-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $TempDir | Out-Null

    $promotedVersionPath = Join-Path $TempDir "promoted-version"
    $versionPath = Join-Path $TempDir "version"
    $artifactPath = Join-Path $TempDir $artifactName
    $checksumsPath = Join-Path $TempDir "checksums.txt"
    $bundlePath = Join-Path $TempDir "checksums.txt.cosign.bundle"
    $candidatePath = Join-Path $TempDir "$AppName.exe"
    $installerPath = Join-Path $TempDir "install.ps1"
    $installerBundlePath = Join-Path $TempDir "install.ps1.cosign.bundle"

    # Read the mutable root pointer exactly once, then pin every release object
    # to its immutable version prefix.
    Download-File -Url ($releaseUrl + "version") -Destination $promotedVersionPath
    $pinnedVersion = ([IO.File]::ReadAllText($promotedVersionPath)).Trim()
    Assert-ReleaseVersion -Version $pinnedVersion
    $releasePrefix = $releaseUrl + "releases/" + $pinnedVersion + "/"

    Download-File -Url ($releasePrefix + "version") -Destination $versionPath
    Download-File -Url ($releasePrefix + $artifactName) -Destination $artifactPath
    Download-File -Url ($releasePrefix + "checksums.txt") -Destination $checksumsPath
    Download-File -Url ($releaseUrl + "install.ps1") -Destination $installerPath
    Download-File -Url ($releaseUrl + "install.ps1.cosign.bundle") -Destination $installerBundlePath

    if ($SkipVerify) {
        Write-Warning "APP_SKIP_VERIFY set: SKIPPING cosign signature verification. Testing only!"
    } else {
        $cosign = Resolve-Cosign
        Download-File -Url ($releasePrefix + "checksums.txt.cosign.bundle") -Destination $bundlePath
        Write-Step "Verifying signed checksums..."
        [void](Invoke-NativeChecked -FilePath $cosign -Arguments @(
            "verify-blob",
            "--bundle", $bundlePath,
            "--certificate-identity", $CertificateIdentity,
            "--certificate-oidc-issuer", $OidcIssuer,
            $checksumsPath
        ) -FailureMessage "Cosign verification of checksums.txt failed")
        Write-Step "Verifying maintenance installer..."
        [void](Invoke-NativeChecked -FilePath $cosign -Arguments @(
            "verify-blob",
            "--bundle", $installerBundlePath,
            "--certificate-identity", $CertificateIdentity,
            "--certificate-oidc-issuer", $OidcIssuer,
            $installerPath
        ) -FailureMessage "Cosign verification of install.ps1 failed")
    }

    Assert-FileHash -Path $versionPath -Expected (
        Get-ExpectedHash -ChecksumsPath $checksumsPath -FileName "version"
    )
    Assert-FileHash -Path $artifactPath -Expected (
        Get-ExpectedHash -ChecksumsPath $checksumsPath -FileName $artifactName
    )

    $version = ([IO.File]::ReadAllText($versionPath)).Trim()
    if ($version -cne $pinnedVersion) {
        throw "Pinned version '$pinnedVersion' does not match signed release version '$version'."
    }
    Write-Step "Installing $AppName $version for Windows $architecture ..."

    Expand-Gzip -Source $artifactPath -Destination $candidatePath
    if ((Get-Item -LiteralPath $candidatePath).Length -le 0) {
        throw "Decompressed application binary is empty."
    }

    # Preflight the staged executable before stopping the running installation.
    $candidateBuildVarsOutput = (Invoke-NativeChecked -FilePath $candidatePath -Arguments @(
        "--build-vars"
    ) -FailureMessage "Staged candidate --build-vars failed" | Out-String)
    try {
        $candidateBuildVars = $candidateBuildVarsOutput | ConvertFrom-Json
    } catch {
        throw "Failed to parse staged candidate build vars: $($_.Exception.Message)"
    }
    if ($candidateBuildVars.name -cne $AppName) {
        throw "Staged candidate name '$($candidateBuildVars.name)' does not match installer name '$AppName'."
    }
    if ($candidateBuildVars.version -cne $version) {
        throw "Staged candidate version '$($candidateBuildVars.version)' does not match signed version '$version'."
    }
    $defaultPort = 0
    # --- BEGIN service.https ---
    $defaultPort = [int]$candidateBuildVars.serviceDefaultPort
    if ($defaultPort -le 0) {
        throw "Staged candidate returned invalid serviceDefaultPort '$($candidateBuildVars.serviceDefaultPort)'."
    }
    # --- END service.https ---
    # --- BEGIN service ---
    # --- BEGIN service.https ---
    if ($ServiceEnabled -eq "true" -and $FreshInstall -and (Test-TcpPortInUse -Port $defaultPort)) {
        throw "Default port $defaultPort is already in use. Free the port, then run the installer again."
    }
    # --- END service.https ---
    # --- END service ---

    # Publish the fail-closed transition before asking existing processes to
    # drain. New processes reject it; existing Go processes poll it and cancel.
    Start-InstallTransition -TargetVersion $version

    Write-Step "Draining running $AppName processes ..."
    Stop-MarkerProcesses `
        -MarkersPath $InstancesDir `
        -ExpectedExecutable $AppBin `
        -GraceSeconds 15

    # --- BEGIN service ---
    if ($TaskExisted) {
        $ServiceTouched = $true
        Stop-AppTask
    }
    # --- END service ---

    New-Item -ItemType Directory -Path $AppDir -Force | Out-Null
    Protect-OwnedDirectory `
        -Path $AppDir `
        -Expected (Join-Path (Join-Path $LocalAppData "Programs") $AppDirName)
    Stop-MarkerProcesses `
        -MarkersPath $InstancesDir `
        -ExpectedExecutable $AppBin `
        -GraceSeconds 0

    Write-Step "Acquiring lifecycle lock ..."
    $LifecycleLockStream = Acquire-ExclusiveLock `
        -Path $LifecycleLockFile `
        -TimeoutSeconds $LockTimeoutSeconds `
        -Description "lifecycle"
    $LifecycleLockHeld = $true

    # Every compliant instance takes a shared lock before writing its marker.
    # Exclusive ownership therefore proves that every marker left here is stale.
    Clear-MarkerFiles -MarkersPath $InstancesDir

    $InstallCandidate = Join-Path $AppDir ("$AppName.install-" + [Guid]::NewGuid().ToString("N") + ".exe")
    Copy-Item -LiteralPath $candidatePath -Destination $InstallCandidate
    if (Test-Path -LiteralPath $AppBin -PathType Leaf) {
        $OldBinary = Join-Path $TempDir "$AppName.old.exe"
        [IO.File]::Replace($InstallCandidate, $AppBin, $OldBinary, $true)
    } else {
        Move-Item -LiteralPath $InstallCandidate -Destination $AppBin
    }
    $BinaryChanged = $true

    # --- BEGIN update ---
    # The release-url file enables update checks and self-update. It is only
    # written for installs from the official release URL; mirrors must not
    # self-check.
    if (Test-Path -LiteralPath $ReleaseUrlFile -PathType Leaf) {
        $ReleaseUrlExisted = $true
        $OldReleaseUrl = [IO.File]::ReadAllText($ReleaseUrlFile)
    }
    if ($releaseUrl -eq $officialReleaseUrl) {
        Write-Step "Writing release source to $ReleaseUrlFile ..."
        $ReleaseUrlChanged = $true
        [IO.File]::WriteAllText($ReleaseUrlFile, $releaseUrl + "`n")
    } elseif ($ReleaseUrlExisted) {
        Write-Step "Mirror install: removing release source file (disables in-app update checks) ..."
        $ReleaseUrlChanged = $true
        Remove-Item -LiteralPath $ReleaseUrlFile -Force
    }
    # --- END update ---

    # Cache the independently signed controller beside retained maintenance
    # state. Publish the bundle first so a crash never exposes a new script
    # paired only with an old bundle.
    if (Test-Path -LiteralPath $CachedInstaller -PathType Leaf) {
        $CachedInstallerExisted = $true
        $OldCachedInstaller = Join-Path $TempDir "install.ps1.old"
        Copy-Item -LiteralPath $CachedInstaller -Destination $OldCachedInstaller
    }
    if (Test-Path -LiteralPath $CachedInstallerBundle -PathType Leaf) {
        $CachedInstallerBundleExisted = $true
        $OldCachedInstallerBundle = Join-Path $TempDir "install.ps1.cosign.bundle.old"
        Copy-Item -LiteralPath $CachedInstallerBundle -Destination $OldCachedInstallerBundle
    }
    $CachedInstallerChanged = $true
    Publish-FileAtomically -Source $installerBundlePath -Destination $CachedInstallerBundle
    Publish-FileAtomically -Source $installerPath -Destination $CachedInstaller

    # --- BEGIN service ---
    # Register the new service definition before migration so all installed
    # state crosses the point of no return together.
    if ($ServiceEnabled -eq "true") {
        $ServiceTouched = $true
        if ($TaskExisted) {
            Write-Step "Updating scheduled task '$TaskName' ..."
        } else {
            Write-Step "Registering logon scheduled task '$TaskName' ..."
        }
        Register-AppTask
        if ($TaskExisted -and -not $TaskWasEnabled) {
            Disable-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName | Out-Null
        }
    }
    # --- END service ---

    Write-Step "Verifying installation (this may take a few moments if migrating) ..."
    $hadMigrationNonce = Test-Path -LiteralPath "Env:\APP_MAINTENANCE_NONCE"
    $oldMigrationNonce = $env:APP_MAINTENANCE_NONCE
    $env:APP_MAINTENANCE_NONCE = $MigrationNonce
    try {
        $MigrationStarted = $true
        $migrationOutput = @(Invoke-NativeChecked -FilePath $AppBin -Arguments @(
            "--migrate"
        ) -FailureMessage "$AppBin --migrate failed")
    } finally {
        if ($hadMigrationNonce) {
            $env:APP_MAINTENANCE_NONCE = $oldMigrationNonce
        } else {
            Remove-Item -LiteralPath "Env:\APP_MAINTENANCE_NONCE" -ErrorAction SilentlyContinue
        }
    }
    $migrationLine = (($migrationOutput | Select-Object -First 1) | Out-String).Trim()
    if ([string]::IsNullOrWhiteSpace($migrationLine)) {
        throw "Migration command returned no version output."
    }
    $effectiveVersion = ($migrationLine -split '\s+')[-1]
    if ($effectiveVersion -cne $version) {
        throw "Migration reported version '$effectiveVersion', expected '$version'."
    }

    # Ready is published while lifecycle exclusivity is still held. A new
    # process may observe it and wait, then rechecks after acquiring its shared
    # lock before touching data.
    Write-MaintenanceState `
        -Phase "ready" `
        -Version $effectiveVersion `
        -TargetVersion "" `
        -Nonce "" `
        -InstallationEpoch $InstallationEpoch
    Release-LifecycleLock

    # --- BEGIN service ---
    if ($ServiceEnabled -eq "true") {
        # Start only after releasing the exclusive migration lock.
        if ($TaskExisted -and -not $TaskWasEnabled) {
            Write-Step "Task updated; leaving it disabled."
        } elseif ($TaskExisted -and -not $TaskWasRunning -and
            -not ($RecoveringTransition -and $TaskWasEnabled)) {
            Write-Step "Task updated; leaving it stopped (was not running)."
        } else {
            Write-Step "Starting scheduled task '$TaskName' ..."
            Clear-ServiceStopLease
            Start-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName
            Wait-AppReady -Port $defaultPort -TimeoutSeconds $ServiceReadyTimeoutSeconds
        }
    }
    # --- END service ---

    Add-UserPath -Directories @($AppDir)

    $Succeeded = $true
    Write-Host ""
    if ($Update) {
        Write-Host "Updated:   $AppName ($effectiveVersion)"
    } else {
        Write-Host "Installed: $AppName ($effectiveVersion)"
    }
    # --- BEGIN service ---
    if ($ServiceEnabled -eq "true") {
        Write-Host "Service:   scheduled task '$TaskName' (starts at logon, current user)"
        # --- BEGIN service.https ---
        Write-Host "Dashboard: https://localhost:$defaultPort"
        # --- END service.https ---
        Write-Host "Run:       $AppName service   # service management cheat sheet"
    }
    # --- END service ---
    Write-Host "Run:       $AppName -h          # help"
    Write-Host "Open a new terminal to pick up the updated PATH."
    }
} catch {
    $InstallFailure = $_
    if ($UninstallStarted) {
        Write-Warning "Uninstall began; retaining phase 'uninstalling' so rerunning cached -Uninstall can finish safely."
    } elseif ($MigrationStarted) {
        Write-Warning "Migration was invoked; retaining the new binary, maintenance state, release, and service state for recovery."
    } else {
        Rollback-Install
    }
} finally {
    if ($null -ne $LifecycleLockStream) {
        try {
            if ($LifecycleLockHeld) {
                $LifecycleLockStream.Unlock(0, 1)
            }
        } catch {
            # Preserve the original installation error.
        }
        $LifecycleLockStream.Dispose()
        $LifecycleLockStream = $null
        $LifecycleLockHeld = $false
    }
    if ($null -ne $OperationLockStream) {
        try {
            if ($OperationLockHeld) {
                $OperationLockStream.Unlock(0, 1)
            }
        } catch {
            # Preserve the original controller result.
        }
        $OperationLockStream.Dispose()
        $OperationLockStream = $null
        $OperationLockHeld = $false
    }
    if ($null -ne $TempDir -and (Test-Path -LiteralPath $TempDir)) {
        Remove-Item -LiteralPath $TempDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

if (-not $Succeeded) {
    $message = "Installation failed."
    if ($null -ne $InstallFailure) {
        $message = $InstallFailure.Exception.Message
    }
    $Error.Clear()
    [Console]::Error.WriteLine($message)
    exit 1
}

$Error.Clear()
exit 0
