param(
    [string]$InstallerCandidate = ""
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

# Native Windows lifecycle E2E. It builds a production-mode binary, serves the
# final release layout over loopback, and exercises install, update recovery,
# detached binary-driven update, fail-closed startup, cached offline uninstall,
# idempotency, and epoch fencing. Verification bypasses are confined to this
# unsigned local fixture.

$Version = "v0.0.0-test"
$FaultVersion = "v0.0.1-fault-test"
$NewerVersion = "v0.0.2-detached-test"
$Root = (Resolve-Path (Join-Path $PSScriptRoot "..")).ProviderPath
$Template = [IO.File]::ReadAllText((Join-Path $Root "scripts/install.ps1"))
$RunId = [Guid]::NewGuid().ToString("N")
$AppName = "sprout-lifecycle-test-" + $RunId.Substring(0, 8)
$ServiceEnabled = $false
$ServiceDescription = "test"
$ServiceArgs = "service run"
$ServiceDefaultPort = 0
# --- BEGIN service.https ---
$ServiceDefaultPort = 8484
# --- END service.https ---
$SmokeArgs = @("config", "show")
# --- BEGIN service.https ---
$SmokeArgs = @("users", "list")
# --- END service.https ---
if (-not [string]::IsNullOrWhiteSpace($InstallerCandidate)) {
    $CandidatePath = (Resolve-Path -LiteralPath $InstallerCandidate).Path
    $Template = [IO.File]::ReadAllText($CandidatePath)
    $nameMatch = [regex]::Match($Template, '(?m)^\$AppName = "([^"]+)"[ \t]*\r?$')
    $serviceMatch = [regex]::Match($Template, '(?m)^\$ServiceEnabled = "(true|false)"[ \t]*\r?$')
    $descriptionMatch = [regex]::Match($Template, '(?m)^\$ServiceDescription = "([^"]*)"[ \t]*\r?$')
    $argsMatch = [regex]::Match($Template, '(?m)^\$ServiceArgs = "([^"]*)"[ \t]*\r?$')
    if (-not $nameMatch.Success -or -not $serviceMatch.Success -or
        -not $descriptionMatch.Success -or -not $argsMatch.Success) {
        throw "Could not read build settings from rendered installer candidate."
    }
    # A release candidate may use the real application's name. Rewrite only
    # the fixture copy so this destructive E2E can never target a real install.
    $Template = $Template.Replace(
        $nameMatch.Value,
        ('$AppName = "' + $AppName + '"')
    )
    $ServiceEnabled = ($serviceMatch.Groups[1].Value -eq "true")
    $ServiceDescription = $descriptionMatch.Groups[1].Value
    $ServiceArgs = $argsMatch.Groups[1].Value
}

$Temp = Join-Path ([IO.Path]::GetTempPath()) ("sprout-lifecycle-test-" + $RunId)
$ReleaseRoot = Join-Path $Temp "release"
$VersionRoot = Join-Path (Join-Path $ReleaseRoot "releases") $Version
$ArtifactName = "windows-amd64.exe.gz"
$BuildPath = Join-Path $Temp "$AppName.exe"
$FaultBuildPath = Join-Path $Temp "$AppName-fault.exe"
$NewerBuildPath = Join-Path $Temp "$AppName-newer.exe"
$CosignStubBuildPath = Join-Path $Temp "cosign-stub.exe"
$ArtifactPath = Join-Path $VersionRoot $ArtifactName
$InstallerPath = Join-Path $ReleaseRoot "install.ps1"
$InstallerBundlePath = Join-Path $ReleaseRoot "install.ps1.cosign.bundle"
$FaultReleaseRoot = Join-Path $ReleaseRoot "fault"
$FaultVersionRoot = Join-Path (Join-Path $FaultReleaseRoot "releases") $FaultVersion
$FaultArtifactPath = Join-Path $FaultVersionRoot $ArtifactName
$FaultInstallerPath = Join-Path $FaultReleaseRoot "install-fault.ps1"
$FaultRecoveryInstallerPath = Join-Path $FaultReleaseRoot "install.ps1"
$FaultInstallerBundlePath = Join-Path $FaultReleaseRoot "install.ps1.cosign.bundle"
$NewerReleaseRoot = Join-Path $ReleaseRoot "detached"
$NewerVersionRoot = Join-Path (Join-Path $NewerReleaseRoot "releases") $NewerVersion
$NewerArtifactPath = Join-Path $NewerVersionRoot $ArtifactName
$NewerInstallerPath = Join-Path $NewerReleaseRoot "install.ps1"
$NewerInstallerBundlePath = Join-Path $NewerReleaseRoot "install.ps1.cosign.bundle"
$Server = $null
$OriginalUserPath = [Environment]::GetEnvironmentVariable("Path", "User")
$OriginalSkipVerify = $env:APP_SKIP_VERIFY
$OriginalReleaseUrl = $env:APP_RELEASE_URL
$OriginalExpectedEpoch = $env:APP_MAINTENANCE_EXPECT_EPOCH
$OriginalExpectedVersion = $env:APP_MAINTENANCE_EXPECT_VERSION
$PushedLocation = $false

$LocalAppData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
$AppDirName = $AppName.Substring(0, 1).ToUpperInvariant() + $AppName.Substring(1)
$InstalledDir = Join-Path (Join-Path $LocalAppData "Programs") $AppDirName
$InstalledBin = Join-Path $InstalledDir "$AppName.exe"
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
$ReleaseUrlFile = Join-Path $MaintenanceDir "release-url"
$MaintenanceLog = Join-Path $LogsDir "maintenance.log"
$RetainedLog = Join-Path $LogsDir "lifecycle-e2e.log"
$StopLeaseFile = Join-Path $ControlDir "service.stop"
$ManagedCosignDir = Join-Path (Join-Path $LocalAppData "Programs") "cosign"
$ManagedCosignBin = Join-Path $ManagedCosignDir "cosign.exe"
$ManagedCosignDirExisted = Test-Path -LiteralPath $ManagedCosignDir -PathType Container
$ManagedCosignExisted = Test-Path -LiteralPath $ManagedCosignBin -PathType Leaf
$ManagedCosignBackup = Join-Path $Temp "cosign.original.exe"
$Unrelated = $null
$OccupiedPortListener = $null
$InvocationNumber = 0

function Compress-Gzip {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination
    )
    $sourceStream = [IO.File]::OpenRead($Source)
    try {
        $output = [IO.File]::Create($Destination)
        try {
            $gzip = New-Object IO.Compression.GZipStream(
                $output, [IO.Compression.CompressionMode]::Compress, $false
            )
            try {
                $sourceStream.CopyTo($gzip)
            } finally {
                $gzip.Dispose()
            }
        } finally {
            $output.Dispose()
        }
    } finally {
        $sourceStream.Dispose()
    }
}

function Invoke-Installer {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [string[]]$Arguments = @()
    )

    $script:InvocationNumber++
    $stdout = Join-Path $Temp ("installer-{0}.out" -f $InvocationNumber)
    $stderr = Join-Path $Temp ("installer-{0}.err" -f $InvocationNumber)
    $processArguments = @(
        "-NoProfile",
        "-NonInteractive",
        "-ExecutionPolicy", "Bypass",
        "-File", "`"$Path`""
    ) + $Arguments
    $process = Start-Process -FilePath "powershell" -ArgumentList $processArguments `
        -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru -Wait
    $detail = ((Get-Content -LiteralPath $stdout, $stderr -ErrorAction SilentlyContinue) -join "`n").Trim()
    return [PSCustomObject]@{
        ExitCode = $process.ExitCode
        Detail = $detail
    }
}

function Read-LifecycleState {
    if (-not (Test-Path -LiteralPath $StateFile -PathType Leaf)) {
        throw "Lifecycle state is missing: $StateFile"
    }
    $state = [IO.File]::ReadAllText($StateFile) | ConvertFrom-Json
    $required = @("phase", "version", "targetVersion", "nonce", "changedAt", "installationEpoch")
    $names = @($state.PSObject.Properties.Name)
    if ($names.Count -ne $required.Count) {
        throw "Lifecycle state has an unexpected field count."
    }
    foreach ($name in $required) {
        if ($names -cnotcontains $name -or $state.$name -isnot [string]) {
            throw "Lifecycle state field '$name' is missing or is not a string."
        }
    }
    if ([string]::IsNullOrWhiteSpace($state.changedAt) -or
        [string]::IsNullOrWhiteSpace($state.installationEpoch)) {
        throw "Lifecycle state is missing changedAt or installationEpoch."
    }
    return $state
}

function Get-UserPathEntryCount {
    param([Parameter(Mandatory = $true)][string]$Directory)

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ([string]::IsNullOrWhiteSpace($userPath)) {
        return 0
    }
    $count = 0
    foreach ($entry in $userPath.Split(";")) {
        if ([string]::Equals(
            $entry.Trim().TrimEnd("\"),
            $Directory.TrimEnd("\"),
            [StringComparison]::OrdinalIgnoreCase
        )) {
            $count++
        }
    }
    return $count
}

function Assert-ServiceTaskPrincipal {
    if (-not $ServiceEnabled) {
        return
    }
    $task = Get-ScheduledTask -TaskPath "\" -TaskName $AppName -ErrorAction SilentlyContinue
    if ($null -eq $task) {
        throw "Expected scheduled task '$AppName' is missing."
    }
    if ($task.Principal.LogonType.ToString() -ne "Interactive" -or
        $task.Principal.RunLevel.ToString() -ne "Limited") {
        throw "Scheduled task must use an Interactive, Limited principal."
    }
}

try {
    Remove-Item -LiteralPath $InstalledDir -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $StorageDir -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Path $VersionRoot -Force | Out-Null

    Push-Location $Root
    $PushedLocation = $true
    $ModulePath = & go list -m
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($ModulePath)) {
        throw "Failed to read the Go module path."
    }
    $BuildPackage = "$($ModulePath.Trim())/internal/build"
    $ldflags = @(
        "-X '$BuildPackage.name=$AppName'",
        "-X '$BuildPackage.version=$Version'",
        "-X '$BuildPackage.contactURL=https://example.invalid'",
        "-X '$BuildPackage.defaultLogLevel=warn'",
        "-X '$BuildPackage.serviceEnabled=$($ServiceEnabled.ToString().ToLowerInvariant())'",
        "-X '$BuildPackage.serviceDesc=$ServiceDescription'",
        "-X '$BuildPackage.serviceArgs=$ServiceArgs'",
        "-X '$BuildPackage.serviceDefaultPort=$ServiceDefaultPort'",
        "-X '$BuildPackage.certIdentity=test-identity'",
        "-X '$BuildPackage.oidcIssuer=test-issuer'",
        "-X '$BuildPackage.devMode=false'"
    ) -join " "
    & go build -trimpath -buildvcs=false "-ldflags=$ldflags" -o $BuildPath ./cmd
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build Windows installer fixture."
    }
    $faultLdflags = $ldflags.Replace(
        "-X '$BuildPackage.version=$Version'",
        "-X '$BuildPackage.version=$FaultVersion'"
    )
    & go build -trimpath -buildvcs=false "-ldflags=$faultLdflags" -o $FaultBuildPath ./cmd
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build Windows post-boundary fault fixture."
    }
    $newerLdflags = $ldflags.Replace(
        "-X '$BuildPackage.version=$Version'",
        "-X '$BuildPackage.version=$NewerVersion'"
    )
    & go build -trimpath -buildvcs=false "-ldflags=$newerLdflags" -o $NewerBuildPath ./cmd
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build Windows detached-update fixture."
    }
    $cosignStubSource = Join-Path $Temp "cosign-stub.go"
    [IO.File]::WriteAllText($cosignStubSource, "package main`r`nfunc main() {}`r`n")
    & go build -trimpath -buildvcs=false -o $CosignStubBuildPath $cosignStubSource
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build the Windows cosign test stub."
    }
    Pop-Location
    $PushedLocation = $false

    Compress-Gzip -Source $BuildPath -Destination $ArtifactPath
    New-Item -ItemType Directory -Path $FaultVersionRoot -Force | Out-Null
    Compress-Gzip -Source $FaultBuildPath -Destination $FaultArtifactPath
    New-Item -ItemType Directory -Path $NewerVersionRoot -Force | Out-Null
    Compress-Gzip -Source $NewerBuildPath -Destination $NewerArtifactPath
    [IO.File]::WriteAllText((Join-Path $ReleaseRoot "version"), "$Version`n")
    [IO.File]::WriteAllText((Join-Path $VersionRoot "version"), "$Version`n")
    [IO.File]::WriteAllText((Join-Path $FaultReleaseRoot "version"), "$FaultVersion`n")
    [IO.File]::WriteAllText((Join-Path $FaultVersionRoot "version"), "$FaultVersion`n")
    [IO.File]::WriteAllText((Join-Path $NewerReleaseRoot "version"), "$NewerVersion`n")
    [IO.File]::WriteAllText((Join-Path $NewerVersionRoot "version"), "$NewerVersion`n")
    $artifactHash = (Get-FileHash -LiteralPath $ArtifactPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $versionHash = (Get-FileHash -LiteralPath (Join-Path $VersionRoot "version") -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText(
        (Join-Path $VersionRoot "checksums.txt"),
        "$artifactHash  $ArtifactName`n$versionHash  version`n"
    )
    $faultArtifactHash = (Get-FileHash -LiteralPath $FaultArtifactPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $faultVersionHash = (Get-FileHash -LiteralPath (Join-Path $FaultVersionRoot "version") -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText(
        (Join-Path $FaultVersionRoot "checksums.txt"),
        "$faultArtifactHash  $ArtifactName`n$faultVersionHash  version`n"
    )
    $newerArtifactHash = (Get-FileHash -LiteralPath $NewerArtifactPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $newerVersionHash = (Get-FileHash -LiteralPath (Join-Path $NewerVersionRoot "version") -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText(
        (Join-Path $NewerVersionRoot "checksums.txt"),
        "$newerArtifactHash  $ArtifactName`n$newerVersionHash  version`n"
    )

    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = ([Net.IPEndPoint]$listener.LocalEndpoint).Port
    $listener.Stop()
    $ReleaseUrl = "http://127.0.0.1:$port/"
    $DetachedReleaseUrl = $ReleaseUrl + "detached/"

    $rendered = $Template
    if ([string]::IsNullOrWhiteSpace($InstallerCandidate)) {
        $rendered = $rendered.Replace("<APP_NAME>", $AppName)
        $rendered = $rendered.Replace("<RELEASE_URL>", "https://official.invalid/")
        $rendered = $rendered.Replace("<SERVICE>", "false")
        $rendered = $rendered.Replace("<SERVICE_DESC>", $ServiceDescription)
        $rendered = $rendered.Replace("<SERVICE_ARGS>", $ServiceArgs)
        $rendered = $rendered.Replace("<CERT_IDENTITY>", "test-identity")
        $rendered = $rendered.Replace("<OIDC_ISSUER>", "test-issuer")
        $rendered = $rendered.Replace("<COSIGN_VERSION>", "v0.0.0")
        $rendered = $rendered.Replace("<COSIGN_SHA_WINDOWS_AMD64>", ("0" * 64))
    }
    if ($rendered -match '<[A-Z][A-Z0-9_]*>') {
        throw "Rendered Windows installer contains an unresolved placeholder."
    }
    [IO.File]::WriteAllText($InstallerPath, $rendered)
    [IO.File]::WriteAllText($InstallerBundlePath, "unsigned test bundle`n")
    [IO.File]::WriteAllText($FaultRecoveryInstallerPath, $rendered)
    [IO.File]::WriteAllText($FaultInstallerBundlePath, "unsigned fault test bundle`n")
    $skipNeedle = '$SkipVerify = ($env:APP_SKIP_VERIFY -eq "true" -or $env:APP_SKIP_VERIFY -eq "1")'
    $detachedRendered = $rendered.Replace($skipNeedle, '$SkipVerify = $true # unsigned lifecycle E2E fixture')
    if ($detachedRendered -ceq $rendered) {
        throw "Could not force verification skip in the detached-update fixture."
    }
    $defaultReleaseMatch = [regex]::Match(
        $detachedRendered,
        '(?m)^\$DefaultReleaseUrl = "[^"]*"[ \t]*\r?$'
    )
    if (-not $defaultReleaseMatch.Success) {
        throw "Could not set the detached controller's local release source."
    }
    $detachedRendered = $detachedRendered.Replace(
        $defaultReleaseMatch.Value,
        ('$DefaultReleaseUrl = "' + $DetachedReleaseUrl + '"')
    )
    $strictModeNeedle = 'Set-StrictMode -Version Latest'
    $detachedRendered = $detachedRendered.Replace(
        $strictModeNeedle,
        $strictModeNeedle + "`r`nif (`$Update) { Start-Sleep -Seconds 3 } # keep the maintenance task observable"
    )
    [IO.File]::WriteAllText($NewerInstallerPath, $detachedRendered)
    [IO.File]::WriteAllText($NewerInstallerBundlePath, "unsigned detached test bundle`n")
    $faultNeedle = '        $MigrationStarted = $true'
    if (-not $rendered.Contains($faultNeedle)) {
        throw "Could not find the migration boundary in the rendered Windows installer."
    }
    $faultRendered = $rendered.Replace(
        $faultNeedle,
        $faultNeedle + "`r`n" + '        throw "injected post-migration-boundary failure"'
    )
    [IO.File]::WriteAllText($FaultInstallerPath, $faultRendered)

    $serverLog = Join-Path $Temp "server.log"
    $serverOut = Join-Path $Temp "server.out"
    $quotedReleaseRoot = '"' + $ReleaseRoot + '"'
    $Server = Start-Process -FilePath "python" -ArgumentList @(
        "-m", "http.server", "$port", "--bind", "127.0.0.1", "--directory", $quotedReleaseRoot
    ) -RedirectStandardOutput $serverOut -RedirectStandardError $serverLog -PassThru -WindowStyle Hidden
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        try {
            Invoke-WebRequest -Uri ($ReleaseUrl + "version") -UseBasicParsing -TimeoutSec 1 | Out-Null
            break
        } catch {
            Start-Sleep -Milliseconds 100
        }
    } while ([DateTime]::UtcNow -lt $deadline)
    if ([DateTime]::UtcNow -ge $deadline) {
        throw "Local fixture server did not start."
    }

    $baselineRootReads = @(Select-String -LiteralPath $serverLog -SimpleMatch '"GET /version HTTP/' -ErrorAction SilentlyContinue).Count
    $env:APP_SKIP_VERIFY = "true"
    $env:APP_RELEASE_URL = $ReleaseUrl

    if ($ServiceEnabled -and $ServiceDefaultPort -gt 0) {
        $OccupiedPortListener = New-Object Net.Sockets.TcpListener(
            [Net.IPAddress]::IPv6Any, $ServiceDefaultPort
        )
        $OccupiedPortListener.Server.DualMode = $true
        $OccupiedPortListener.Server.ExclusiveAddressUse = $true
        $OccupiedPortListener.Start()
        try {
            $blockedOut = Join-Path $Temp "blocked-installer.out"
            $blockedErr = Join-Path $Temp "blocked-installer.err"
            $blockedProcess = Start-Process -FilePath "powershell" -ArgumentList @(
                "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "`"$InstallerPath`""
            ) -RedirectStandardOutput $blockedOut -RedirectStandardError $blockedErr -PassThru -Wait
            $blockedDetail = ((Get-Content -LiteralPath $blockedOut, $blockedErr -ErrorAction SilentlyContinue) -join "`n").Trim()
            if ($blockedProcess.ExitCode -eq 0) {
                throw "Fresh Windows install succeeded while default port $ServiceDefaultPort was occupied."
            }
            if ($blockedDetail -notmatch "Default port $ServiceDefaultPort is already in use") {
                throw "Fresh Windows install did not report the occupied default port:`n$blockedDetail"
            }
            if ((Test-Path -LiteralPath $InstalledBin) -or
                $null -ne (Get-ScheduledTask -TaskPath "\" -TaskName $AppName -ErrorAction SilentlyContinue)) {
                throw "Failed fresh Windows install left application or task state behind."
            }
            if (Test-Path -LiteralPath $StateFile) {
                throw "Failed fresh Windows install published lifecycle state."
            }
        } finally {
            $OccupiedPortListener.Stop()
            $OccupiedPortListener = $null
        }
    }

    $install = Invoke-Installer -Path $InstallerPath
    if ($install.ExitCode -ne 0) {
        throw "Windows install failed (exit $($install.ExitCode)):`n$($install.Detail)"
    }
    foreach ($path in @(
        $InstalledBin, $OperationLockFile, $LifecycleLockFile,
        $StateFile, $CachedInstaller, $CachedInstallerBundle
    )) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "Windows install did not create '$path'."
        }
    }
    foreach ($path in @($DataDir, $ControlDir, $MaintenanceDir, $LogsDir, $InstancesDir)) {
        if (-not (Test-Path -LiteralPath $path -PathType Container)) {
            throw "Windows install did not create '$path'."
        }
    }
    $state = Read-LifecycleState
    if ($state.phase -cne "ready" -or $state.version -cne $Version -or
        $state.targetVersion -cne "" -or $state.nonce -cne "") {
        throw "Fresh install did not publish ready state for $Version."
    }
    $InitialEpoch = $state.installationEpoch
    if ((Get-UserPathEntryCount -Directory $InstalledDir) -ne 1) {
        throw "Fresh install did not add exactly one application PATH entry."
    }
    Assert-ServiceTaskPrincipal
    $cachedInstallerInitialHash = (Get-FileHash -LiteralPath $CachedInstaller -Algorithm SHA256).Hash
    $publishedInstallerHash = (Get-FileHash -LiteralPath $InstallerPath -Algorithm SHA256).Hash
    $cachedBundleInitialHash = (Get-FileHash -LiteralPath $CachedInstallerBundle -Algorithm SHA256).Hash
    $publishedBundleHash = (Get-FileHash -LiteralPath $InstallerBundlePath -Algorithm SHA256).Hash
    if ($cachedInstallerInitialHash -cne $publishedInstallerHash -or
        $cachedBundleInitialHash -cne $publishedBundleHash) {
        throw "Fresh install did not cache the published maintenance controller and bundle."
    }

    # A post-boundary fault must retain the new binary and an updating state so
    # normal startup fails closed until the clean controller completes recovery.
    $faultReleaseUrl = $ReleaseUrl + "fault/"
    $env:APP_RELEASE_URL = $faultReleaseUrl
    $env:APP_MAINTENANCE_EXPECT_EPOCH = $InitialEpoch
    $env:APP_MAINTENANCE_EXPECT_VERSION = $Version
    $fault = Invoke-Installer -Path $FaultInstallerPath -Arguments @("-Update")
    if ($fault.ExitCode -eq 0 -or
        $fault.Detail -notmatch "injected post-migration-boundary failure") {
        throw "Windows post-boundary fault did not fail as expected:`n$($fault.Detail)"
    }
    $state = Read-LifecycleState
    if ($state.phase -cne "updating" -or $state.version -cne $Version -or
        $state.targetVersion -cne $FaultVersion -or
        $state.nonce -cnotmatch '^[0-9a-f]{64}$' -or
        $state.installationEpoch -cne $InitialEpoch) {
        throw "Windows post-boundary fault did not retain the expected updating state."
    }
    $installedHash = (Get-FileHash -LiteralPath $InstalledBin -Algorithm SHA256).Hash
    $faultBuildHash = (Get-FileHash -LiteralPath $FaultBuildPath -Algorithm SHA256).Hash
    if ($installedHash -cne $faultBuildHash) {
        throw "Windows post-boundary fault restored or replaced the new binary."
    }

    $pendingOut = Join-Path $Temp "pending-state.out"
    $pendingErr = Join-Path $Temp "pending-state.err"
    $pendingProcess = Start-Process -FilePath $InstalledBin -ArgumentList $SmokeArgs `
        -RedirectStandardOutput $pendingOut -RedirectStandardError $pendingErr -PassThru -Wait
    $pendingDetail = ((Get-Content -LiteralPath $pendingOut, $pendingErr -ErrorAction SilentlyContinue) -join "`n").Trim()
    if ($pendingProcess.ExitCode -eq 0 -or $pendingDetail -notmatch "not ready") {
        throw "Normal Windows startup did not fail closed on updating state:`n$pendingDetail"
    }

    Remove-Item Env:APP_MAINTENANCE_EXPECT_EPOCH -ErrorAction SilentlyContinue
    Remove-Item Env:APP_MAINTENANCE_EXPECT_VERSION -ErrorAction SilentlyContinue
    $recovery = Invoke-Installer -Path $FaultRecoveryInstallerPath
    if ($recovery.ExitCode -ne 0) {
        throw "Windows lifecycle recovery failed (exit $($recovery.ExitCode)):`n$($recovery.Detail)"
    }
    $state = Read-LifecycleState
    if ($state.phase -cne "ready" -or $state.version -cne $FaultVersion -or
        $state.targetVersion -cne "" -or $state.nonce -cne "" -or
        $state.installationEpoch -cne $InitialEpoch) {
        throw "Windows lifecycle recovery did not publish ready state for $FaultVersion."
    }
    if ($ServiceEnabled) {
        $task = Get-ScheduledTask -TaskPath "\" -TaskName $AppName -ErrorAction SilentlyContinue
        if ($null -eq $task) {
            throw "Windows lifecycle recovery lost the scheduled task."
        }
        if ($task.State.ToString() -ne "Running") {
            throw "Recovered scheduled task did not restart automatically."
        }
    }

    # Admit a genuinely newer update through the real binary. The detached
    # maintenance task verifies the controller with this fixture-only cosign
    # stub, then the controller's baked test switch skips release signatures.
    New-Item -ItemType Directory -Path $InstancesDir -Force | Out-Null
    $Unrelated = Start-Process -FilePath "powershell" -ArgumentList @(
        "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 300"
    ) -PassThru -WindowStyle Hidden
    $UnrelatedMarker = Join-Path $InstancesDir $Unrelated.Id.ToString()
    $DeadMarker = Join-Path $InstancesDir "999999999"
    $MalformedMarker = Join-Path $InstancesDir "not-a-pid"
    [IO.File]::WriteAllText($UnrelatedMarker, "")
    [IO.File]::WriteAllText($DeadMarker, "")
    [IO.File]::WriteAllText($MalformedMarker, "")

    if ($ManagedCosignExisted) {
        Copy-Item -LiteralPath $ManagedCosignBin -Destination $ManagedCosignBackup
    }
    New-Item -ItemType Directory -Path $ManagedCosignDir -Force | Out-Null
    Copy-Item -LiteralPath $CosignStubBuildPath -Destination $ManagedCosignBin -Force

    [IO.File]::WriteAllText($ReleaseUrlFile, $DetachedReleaseUrl + "`n")
    Remove-Item Env:APP_MAINTENANCE_EXPECT_EPOCH -ErrorAction SilentlyContinue
    Remove-Item Env:APP_MAINTENANCE_EXPECT_VERSION -ErrorAction SilentlyContinue
    $detachedOut = Join-Path $Temp "detached-update.out"
    $detachedErr = Join-Path $Temp "detached-update.err"
    $detachedProcess = Start-Process -FilePath $InstalledBin -ArgumentList @("update", "--yes") `
        -RedirectStandardOutput $detachedOut -RedirectStandardError $detachedErr -PassThru -Wait
    $detachedDetail = ((Get-Content -LiteralPath $detachedOut, $detachedErr -ErrorAction SilentlyContinue) -join "`n").Trim()
    $maintenanceLogPattern = [regex]::Escape($MaintenanceLog)
    if ($detachedProcess.ExitCode -ne 0 -or
        $detachedDetail -notmatch "Update accepted" -or
        $detachedDetail -notmatch $maintenanceLogPattern) {
        throw "Binary did not admit the detached update:`n$detachedDetail"
    }

    $maintenanceTask = $null
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        $maintenanceTask = @(Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object {
            $_.TaskName -like "$AppName Maintenance *"
        } | Select-Object -First 1)
        if ($maintenanceTask.Count -gt 0 -and
            $maintenanceTask[0].State.ToString() -eq "Running") {
            $maintenanceTask = $maintenanceTask[0]
            break
        }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    if (@($maintenanceTask).Count -eq 0 -or
        $maintenanceTask.State.ToString() -ne "Running") {
        throw "Detached update did not expose a running maintenance task."
    }
    $maintenanceAction = @($maintenanceTask.Actions)[0]
    if ([IO.Path]::GetFileName($maintenanceAction.Execute) -ine "powershell.exe" -or
        $maintenanceAction.Arguments -notmatch '(?i)-WindowStyle\s+Hidden' -or
        $maintenanceAction.Arguments -notmatch '(?i)-NonInteractive') {
        throw "Detached maintenance task does not use hidden, non-interactive PowerShell."
    }
    if (-not $maintenanceTask.Settings.Hidden -or
        $maintenanceTask.Settings.ExecutionTimeLimit.ToString() -cne "PT0S" -or
        $maintenanceTask.Principal.LogonType.ToString() -ne "Interactive" -or
        $maintenanceTask.Principal.RunLevel.ToString() -ne "Limited") {
        throw "Detached maintenance task is not hidden and unlimited with an Interactive, Limited principal."
    }

    $deadline = [DateTime]::UtcNow.AddMinutes(3)
    $terminalReady = $false
    do {
        $state = Read-LifecycleState
        $remainingMaintenanceTasks = @(Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object {
            $_.TaskName -like "$AppName Maintenance *"
        })
        $maintenanceLogText = ""
        if (Test-Path -LiteralPath $MaintenanceLog -PathType Leaf) {
            try {
                $maintenanceLogText = [IO.File]::ReadAllText($MaintenanceLog)
            } catch [IO.IOException] {
                # The runner may be between Add-Content open/close operations.
            }
        }
        if ($maintenanceLogText -match "Maintenance update failed") {
            throw "Detached update failed. Maintenance log:`n$maintenanceLogText"
        }
        if ($state.phase -ceq "ready" -and $state.version -ceq $NewerVersion -and
            $remainingMaintenanceTasks.Count -eq 0 -and
            $maintenanceLogText -match "Maintenance update job started" -and
            $maintenanceLogText -match "Maintenance update completed") {
            $terminalReady = $true
            break
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    if (-not $terminalReady) {
        throw "Detached update did not reach terminal ready state. Maintenance log:`n$maintenanceLogText"
    }
    if ($state.targetVersion -cne "" -or $state.nonce -cne "" -or
        $state.installationEpoch -cne $InitialEpoch) {
        throw "Detached update changed the installation epoch or retained transition fields."
    }
    if (-not (Test-Path -LiteralPath $ReleaseUrlFile -PathType Leaf) -or
        [IO.File]::ReadAllText($ReleaseUrlFile).Trim() -cne $DetachedReleaseUrl) {
        throw "Detached update did not preserve its release source."
    }
    $installedHash = (Get-FileHash -LiteralPath $InstalledBin -Algorithm SHA256).Hash
    $newerBuildHash = (Get-FileHash -LiteralPath $NewerBuildPath -Algorithm SHA256).Hash
    if ($installedHash -cne $newerBuildHash) {
        throw "Detached update did not install the newer binary."
    }
    $Unrelated.Refresh()
    if ($Unrelated.HasExited) {
        throw "Windows update stopped an unrelated process from a stale marker."
    }
    foreach ($marker in @($UnrelatedMarker, $DeadMarker, $MalformedMarker)) {
        if (Test-Path -LiteralPath $marker) {
            throw "Windows update left stale marker '$marker'."
        }
    }

    & $InstalledBin @SmokeArgs | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "Installed application failed after lifecycle recovery."
    }
    if ($ServiceEnabled) {
        Assert-ServiceTaskPrincipal
        if ((Get-ScheduledTask -TaskPath "\" -TaskName $AppName).State.ToString() -ne "Running") {
            throw "Detached update did not restart the scheduled task."
        }
        if (Test-Path -LiteralPath $StopLeaseFile -PathType Leaf) {
            $leaseValue = 0L
            $leaseText = [IO.File]::ReadAllText($StopLeaseFile).Trim()
            if (-not [long]::TryParse($leaseText, [ref]$leaseValue) -or
                $leaseValue -gt [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()) {
                throw "Windows update left an active or malformed service stop lease: '$leaseText'."
            }
        }
    }

    Start-Sleep -Milliseconds 250
    $rootReads = @(Select-String -LiteralPath $serverLog -SimpleMatch '"GET /version HTTP/' -ErrorAction SilentlyContinue).Count - $baselineRootReads
    $expectedRootReads = 1
    if ($ServiceEnabled -and $ServiceDefaultPort -gt 0) {
        $expectedRootReads++
    }
    if ($rootReads -ne $expectedRootReads) {
        throw "Installer read mutable root version $rootReads times; expected $expectedRootReads."
    }

    # Keep a neighboring PATH entry to prove uninstall removes only the exact
    # application directory inserted by the installer.
    $PathSentinel = Join-Path $Temp "path-sentinel"
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $OddPathSegment = "  $PathSentinel  "
    $pathBeforeUninstall = "$OddPathSegment;;$userPath;"
    [Environment]::SetEnvironmentVariable("Path", $pathBeforeUninstall, "User")
    $expectedPathSegments = New-Object System.Collections.Generic.List[string]
    foreach ($entry in $pathBeforeUninstall.Split([char]";")) {
        if (-not [string]::Equals(
            $entry.Trim().TrimEnd("\"),
            $InstalledDir.TrimEnd("\"),
            [StringComparison]::OrdinalIgnoreCase
        )) {
            $expectedPathSegments.Add($entry)
        }
    }
    $expectedPathAfterUninstall = $expectedPathSegments -join ";"
    [IO.File]::WriteAllText($RetainedLog, "retain across uninstall`n")
    $cachedInstallerHash = (Get-FileHash -LiteralPath $CachedInstaller -Algorithm SHA256).Hash
    $cachedBundleHash = (Get-FileHash -LiteralPath $CachedInstallerBundle -Algorithm SHA256).Hash

    # Take the fixture server offline. The retained cached controller must be
    # sufficient for uninstall and for an idempotent repeated uninstall.
    $Server.Refresh()
    if (-not $Server.HasExited) {
        Stop-Process -Id $Server.Id -Force
    }
    $Server.WaitForExit()
    $Server = $null
    Remove-Item Env:APP_MAINTENANCE_EXPECT_EPOCH -ErrorAction SilentlyContinue
    Remove-Item Env:APP_MAINTENANCE_EXPECT_VERSION -ErrorAction SilentlyContinue
    $savedErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $uninstallDetail = ("y" | & $InstalledBin uninstall 2>&1 | Out-String).Trim()
        $uninstallExitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedErrorActionPreference
    }
    if ($uninstallExitCode -ne 0 -or $uninstallDetail -notmatch "Uninstall accepted") {
        throw "Detached offline uninstall was not admitted (exit $uninstallExitCode):`n$uninstallDetail"
    }
    $deadline = [DateTime]::UtcNow.AddMinutes(3)
    do {
        $state = Read-LifecycleState
        $maintenanceTasks = @(Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object {
            $_.TaskName -like "$AppName Maintenance *"
        })
        if ($state.phase -ceq "uninstalled" -and $maintenanceTasks.Count -eq 0) {
            break
        }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    if ($state.phase -cne "uninstalled" -or $maintenanceTasks.Count -ne 0) {
        throw "Detached offline uninstall did not finish; see '$MaintenanceLog'."
    }
    $maintenanceDetail = [IO.File]::ReadAllText($MaintenanceLog)
    if ($maintenanceDetail -notmatch "Remote installer selection failed" -or
        $maintenanceDetail -notmatch "Maintenance uninstall completed") {
        throw "Detached uninstall did not log remote failure and cached completion."
    }
    if ((Test-Path -LiteralPath $DataDir) -or (Test-Path -LiteralPath $InstalledDir)) {
        throw "Uninstall left application data or binary artifacts behind."
    }
    if ($null -ne (Get-ScheduledTask -TaskPath "\" -TaskName $AppName -ErrorAction SilentlyContinue)) {
        throw "Uninstall left the scheduled task behind."
    }
    foreach ($path in @($ControlDir, $MaintenanceDir, $LogsDir)) {
        if (-not (Test-Path -LiteralPath $path -PathType Container)) {
            throw "Uninstall removed retained directory '$path'."
        }
    }
    foreach ($path in @(
        $StateFile, $OperationLockFile, $LifecycleLockFile,
        $CachedInstaller, $CachedInstallerBundle, $RetainedLog, $ManagedCosignBin
    )) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "Uninstall removed retained maintenance file '$path'."
        }
    }
    if ((Get-FileHash -LiteralPath $CachedInstaller -Algorithm SHA256).Hash -cne $cachedInstallerHash -or
        (Get-FileHash -LiteralPath $CachedInstallerBundle -Algorithm SHA256).Hash -cne $cachedBundleHash) {
        throw "Uninstall changed the retained maintenance cache."
    }
    $state = Read-LifecycleState
    if ($state.phase -cne "uninstalled" -or $state.version -cne "" -or
        $state.targetVersion -cne "" -or $state.nonce -cne "" -or
        $state.installationEpoch -cne $InitialEpoch) {
        throw "Uninstall did not publish the expected uninstalled state."
    }
    if ((Get-UserPathEntryCount -Directory $InstalledDir) -ne 0 -or
        (Get-UserPathEntryCount -Directory $PathSentinel) -ne 1) {
        throw "Uninstall did not remove only the exact application PATH entry."
    }
    $actualPathAfterUninstall = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($actualPathAfterUninstall -cne $expectedPathAfterUninstall) {
        throw "Uninstall rewrote unrelated PATH segments."
    }

    Remove-Item Env:APP_MAINTENANCE_EXPECT_EPOCH -ErrorAction SilentlyContinue
    $repeated = Invoke-Installer -Path $CachedInstaller -Arguments @("-Uninstall")
    if ($repeated.ExitCode -ne 0) {
        throw "Repeated uninstall was not idempotent (exit $($repeated.ExitCode)):`n$($repeated.Detail)"
    }
    if ((Read-LifecycleState).phase -cne "uninstalled") {
        throw "Repeated uninstall changed the uninstalled lifecycle phase."
    }

    # Restart the fixture and reinstall. An old detached job fenced by the
    # previous installation epoch must not affect the replacement install.
    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $reinstallPort = ([Net.IPEndPoint]$listener.LocalEndpoint).Port
    $listener.Stop()
    $ReinstallReleaseUrl = "http://127.0.0.1:$reinstallPort/"
    $serverOut2 = Join-Path $Temp "server-2.out"
    $serverLog2 = Join-Path $Temp "server-2.log"
    $Server = Start-Process -FilePath "python" -ArgumentList @(
        "-m", "http.server", "$reinstallPort", "--bind", "127.0.0.1", "--directory", $quotedReleaseRoot
    ) -RedirectStandardOutput $serverOut2 -RedirectStandardError $serverLog2 -PassThru -WindowStyle Hidden
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        try {
            Invoke-WebRequest -Uri ($ReinstallReleaseUrl + "version") -UseBasicParsing -TimeoutSec 1 | Out-Null
            break
        } catch {
            Start-Sleep -Milliseconds 100
        }
    } while ([DateTime]::UtcNow -lt $deadline)
    if ([DateTime]::UtcNow -ge $deadline) {
        throw "Local fixture server did not restart."
    }

    Remove-Item Env:APP_MAINTENANCE_EXPECT_EPOCH -ErrorAction SilentlyContinue
    $env:APP_RELEASE_URL = $ReinstallReleaseUrl
    $reinstall = Invoke-Installer -Path $InstallerPath
    if ($reinstall.ExitCode -ne 0) {
        throw "Windows reinstall failed (exit $($reinstall.ExitCode)):`n$($reinstall.Detail)"
    }
    Assert-ServiceTaskPrincipal
    $replacementState = Read-LifecycleState
    if ($replacementState.phase -cne "ready" -or
        $replacementState.installationEpoch -ceq $InitialEpoch) {
        throw "Reinstall did not create a new ready installation epoch."
    }
    $env:APP_MAINTENANCE_EXPECT_EPOCH = $InitialEpoch
    $stale = Invoke-Installer -Path $CachedInstaller -Arguments @("-Uninstall")
    if ($stale.ExitCode -eq 0 -or $stale.Detail -notmatch "different installation epoch") {
        throw "Stale-epoch uninstall was not rejected:`n$($stale.Detail)"
    }
    $replacementState = Read-LifecycleState
    if ($replacementState.phase -cne "ready" -or
        -not (Test-Path -LiteralPath $InstalledBin -PathType Leaf) -or
        -not (Test-Path -LiteralPath $DataDir -PathType Container)) {
        throw "Stale-epoch uninstall changed the replacement installation."
    }

    Write-Host "Windows lifecycle E2E passed."
} finally {
    if ($PushedLocation) {
        Pop-Location
    }
    Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object {
        $_.TaskName -like "$AppName Maintenance *"
    } | ForEach-Object {
        Stop-ScheduledTask -InputObject $_ -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -InputObject $_ -Confirm:$false -ErrorAction SilentlyContinue
    }
    if ($ServiceEnabled) {
        & schtasks.exe /End /TN $AppName 2>$null | Out-Null
        & schtasks.exe /Delete /F /TN $AppName 2>$null | Out-Null
        Get-Process -Name $AppName -ErrorAction SilentlyContinue |
            Stop-Process -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $Server -and -not $Server.HasExited) {
        Stop-Process -Id $Server.Id -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $Unrelated -and -not $Unrelated.HasExited) {
        Stop-Process -Id $Unrelated.Id -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $OccupiedPortListener) {
        $OccupiedPortListener.Stop()
    }
    [Environment]::SetEnvironmentVariable("Path", $OriginalUserPath, "User")
    if ($null -eq $OriginalSkipVerify) {
        Remove-Item Env:APP_SKIP_VERIFY -ErrorAction SilentlyContinue
    } else {
        $env:APP_SKIP_VERIFY = $OriginalSkipVerify
    }
    if ($null -eq $OriginalReleaseUrl) {
        Remove-Item Env:APP_RELEASE_URL -ErrorAction SilentlyContinue
    } else {
        $env:APP_RELEASE_URL = $OriginalReleaseUrl
    }
    if ($null -eq $OriginalExpectedEpoch) {
        Remove-Item Env:APP_MAINTENANCE_EXPECT_EPOCH -ErrorAction SilentlyContinue
    } else {
        $env:APP_MAINTENANCE_EXPECT_EPOCH = $OriginalExpectedEpoch
    }
    if ($null -eq $OriginalExpectedVersion) {
        Remove-Item Env:APP_MAINTENANCE_EXPECT_VERSION -ErrorAction SilentlyContinue
    } else {
        $env:APP_MAINTENANCE_EXPECT_VERSION = $OriginalExpectedVersion
    }
    if ($ManagedCosignExisted -and (Test-Path -LiteralPath $ManagedCosignBackup -PathType Leaf)) {
        Copy-Item -LiteralPath $ManagedCosignBackup -Destination $ManagedCosignBin -Force
    } elseif (-not $ManagedCosignExisted) {
        Remove-Item -LiteralPath $ManagedCosignBin -Force -ErrorAction SilentlyContinue
        if (-not $ManagedCosignDirExisted) {
            Remove-Item -LiteralPath $ManagedCosignDir -Force -ErrorAction SilentlyContinue
        }
    }
    Remove-Item -LiteralPath $InstalledDir -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $StorageDir -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $Temp -Recurse -Force -ErrorAction SilentlyContinue
}
