[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$InstallerPath,
    [switch]$GoPreview,
    [string]$ExpectedInstallerSha256,
    [string]$NodeExecutable = "node"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot "../..")).Path
$versionText = [System.IO.File]::ReadAllText(
    (Join-Path $projectRoot "VERSION"),
    [System.Text.Encoding]::UTF8
)
if ($versionText -notmatch '^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)\r?\n$') {
    throw "VERSION must contain one canonical release SemVer."
}
$expectedAppVersion = $versionText.TrimEnd("`r", "`n")
$installerIdentity = if ($GoPreview) { "CHDesktopGoPreview" } else { "CHDesktopDev" }
$dataProfile = if ($GoPreview) { "dev-go-preview" } else { "dev" }
$shortcutFilter = if ($GoPreview) { "Career Hub Go Preview*.lnk" } else { "Career Hub*.lnk" }

function Test-DescendantPath {
    param(
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$Root
    )

    $candidateFull = [System.IO.Path]::GetFullPath($Candidate)
    $rootFull = [System.IO.Path]::GetFullPath($Root).TrimEnd('\') + '\'
    return $candidateFull.StartsWith(
        $rootFull,
        [System.StringComparison]::OrdinalIgnoreCase
    )
}

function Wait-ForCondition {
    param(
        [Parameter(Mandatory = $true)][string]$Description,
        [Parameter(Mandatory = $true)][scriptblock]$Condition,
        [int]$TimeoutSeconds = 120
    )

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        if (& $Condition) {
            return
        }
        Start-Sleep -Milliseconds 250
    }
    throw "Timed out waiting for $Description."
}

function Invoke-BoundedProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$Arguments = @(),
        [int[]]$AllowedExitCodes = @(0),
        [int]$TimeoutSeconds = 300
    )

    $process = Start-Process `
        -FilePath $FilePath `
        -ArgumentList $Arguments `
        -PassThru `
        -WindowStyle Hidden
    if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        throw "Process timed out: $FilePath"
    }
    if ($process.ExitCode -notin $AllowedExitCodes) {
        throw "Process exited with code $($process.ExitCode): $FilePath"
    }
}

function Get-OwnedProcesses {
    param([Parameter(Mandatory = $true)][string]$Root)

    # A packaged launcher may virtualize LocalAppData while the Go child uses
    # the validated physical resource path. Match both names of this test root.
    $nativeRoot = $Root
    if (Test-Path -LiteralPath $Root) {
        $nativeRoot = & $NodeExecutable -p "require('node:fs').realpathSync.native(process.argv[1])" $Root
        if ($LASTEXITCODE -ne 0 -or -not [IO.Path]::IsPathRooted($nativeRoot)) {
            throw "Cannot resolve the installed test process root."
        }
    }
    $owned = @()
    foreach ($process in Get-CimInstance Win32_Process) {
        if (
            $process.ExecutablePath -and
            ((Test-DescendantPath -Candidate $process.ExecutablePath -Root $Root) -or
             (Test-DescendantPath -Candidate $process.ExecutablePath -Root $nativeRoot))
        ) {
            $owned += $process
        }
    }
    return $owned
}

function Stop-OwnedProcesses {
    param([Parameter(Mandatory = $true)][string]$Root)

    foreach ($process in Get-OwnedProcesses -Root $Root) {
        Stop-Process -Id $process.ProcessId -Force -ErrorAction SilentlyContinue
    }
}

function Wait-ForOwnedProcessesToExit {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [int]$TimeoutSeconds = 45
    )

    Wait-ForCondition `
        -Description "owned installer processes to exit" `
        -TimeoutSeconds $TimeoutSeconds `
        -Condition { @(Get-OwnedProcesses -Root $Root).Count -eq 0 }
}

function Get-InstalledExecutable {
    param([Parameter(Mandatory = $true)][string]$InstallRoot)

    $executables = @(
        Get-ChildItem -LiteralPath $InstallRoot -Directory -Filter "app-*" |
            ForEach-Object { Join-Path $_.FullName "career-hub.exe" } |
            Where-Object { Test-Path -LiteralPath $_ -PathType Leaf }
    )
    if ($executables.Count -ne 1) {
        throw "Expected exactly one installed Career Hub executable."
    }
    return $executables[0]
}

function Assert-BoundedInstallerPayload {
    param(
        [Parameter(Mandatory = $true)][string]$InstallRoot,
        [Parameter(Mandatory = $true)][string]$ExpectedAppVersion
    )

    $appDirectories = @(
        Get-ChildItem -LiteralPath $InstallRoot -Directory -Filter "app-*"
    )
    $currentDirectory = "app-$ExpectedAppVersion"
    if (
        $appDirectories.Count -lt 1 -or
        $appDirectories.Count -gt 2 -or
        $currentDirectory -notin $appDirectories.Name
    ) {
        throw "Squirrel retained an invalid set of installed application versions."
    }

    $packageRoot = Join-Path $InstallRoot "packages"
    $fullPackages = @(
        Get-ChildItem -LiteralPath $packageRoot -File -Filter "$installerIdentity-*-full.nupkg"
    )
    $currentPackage = "$installerIdentity-$ExpectedAppVersion-full.nupkg"
    if (
        $fullPackages.Count -lt 1 -or
        $fullPackages.Count -gt 2 -or
        $currentPackage -notin $fullPackages.Name
    ) {
        throw "Squirrel retained an invalid set of installer packages."
    }
    if (
        @(Get-ChildItem -LiteralPath $packageRoot -File -Filter "*-delta.nupkg").Count -ne 0
    ) {
        throw "The no-delta installer retained an unexpected delta package."
    }
}

function Assert-NativeInstalledRuntime {
    param([Parameter(Mandatory = $true)][string]$Resources)

    $metadata = Get-Content -LiteralPath (Join-Path $Resources "career-hub/build-metadata.json") -Raw | ConvertFrom-Json
    $manifest = Get-Content -LiteralPath (Join-Path $Resources "career-hub-sidecar/sidecar-manifest.json") -Raw | ConvertFrom-Json
    $expectedSchema = if ($GoPreview) { 3 } else { 4 }
    $expectedRuntime = if ($GoPreview) { "go-preview" } else { "go" }
    if ($metadata.schema_version -ne $expectedSchema -or $metadata.sidecar_runtime -ne $expectedRuntime -or
        $metadata.channel -ne "dev" -or $null -ne $metadata.update_feed_url -or
        $manifest.layout -ne "go-onedir" -or @($manifest.files).Count -eq 0 -or
        @($manifest.files | Where-Object { $_.path -match '\.py[co]?$' }).Count -ne 0) {
        throw "Installed payload is not the expected native Go runtime."
    }
    return $metadata.sidecar_runtime
}

function Start-And-VerifyApp {
    param(
        [Parameter(Mandatory = $true)][string]$ExecutablePath,
        [Parameter(Mandatory = $true)][string]$InstallRoot,
        [Parameter(Mandatory = $true)][string]$DataRoot,
        [Parameter(Mandatory = $true)][string]$ExpectedAppVersion
    )

    $resources = Join-Path (Split-Path -Parent $ExecutablePath) "resources"
    $script:verifiedRuntime = Assert-NativeInstalledRuntime -Resources $resources
    $mainProcess = Start-Process -FilePath $ExecutablePath -PassThru -WindowStyle Hidden
    Wait-ForCondition `
        -Description "the synthetic Career Hub data root" `
        -TimeoutSeconds 45 `
        -Condition { Test-Path -LiteralPath $DataRoot -PathType Container }

    $sidecar = $null
    Wait-ForCondition `
        -Description "one packaged sidecar" `
        -TimeoutSeconds 45 `
        -Condition {
            $matches = @(
                Get-OwnedProcesses -Root $InstallRoot |
                    Where-Object { $_.Name -eq "career-hub-sidecar.exe" }
            )
            if ($matches.Count -eq 1) {
                $script:sidecar = $matches[0]
                return $true
            }
            return $false
        }

    $listener = $null
    Wait-ForCondition `
        -Description "the sidecar loopback listener" `
        -TimeoutSeconds 30 `
        -Condition {
            $matches = @(
                Get-NetTCPConnection `
                    -OwningProcess $script:sidecar.ProcessId `
                    -State Listen `
                    -ErrorAction SilentlyContinue |
                    Where-Object { $_.LocalAddress -eq "127.0.0.1" }
            )
            if ($matches.Count -eq 1) {
                $script:listener = $matches[0]
                return $true
            }
            return $false
        }

    $health = Invoke-RestMethod `
        -Uri "http://127.0.0.1:$($script:listener.LocalPort)/api/health" `
        -Method Get `
        -TimeoutSec 5
    if (
        $health.status -ne "ok" -or
        $health.protocol_version -ne 1 -or
        $health.app_version -ne $ExpectedAppVersion
    ) {
        throw "The installed sidecar returned an invalid health record."
    }

    Wait-ForCondition `
        -Description "the Electron main window" `
        -TimeoutSeconds 30 `
        -Condition {
            $mainProcess.Refresh()
            return $mainProcess.MainWindowHandle -ne 0
        }
    if (-not $mainProcess.CloseMainWindow()) {
        throw "The installed app did not accept a normal window close."
    }
    Wait-ForOwnedProcessesToExit -Root $InstallRoot
}

function Get-TestUninstallKeys {
    param([Parameter(Mandatory = $true)][string]$InstallRoot)

    $matches = @()
    $uninstallRoot = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall"
    foreach ($key in Get-ChildItem -LiteralPath $uninstallRoot -ErrorAction SilentlyContinue) {
        $record = Get-ItemProperty -LiteralPath $key.PSPath -ErrorAction SilentlyContinue
        $uninstallProperty = if ($record) {
            $record.PSObject.Properties["UninstallString"]
        } else {
            $null
        }
        if ($uninstallProperty -and [string]$uninstallProperty.Value -like "*$InstallRoot*") {
            $matches += $key.PSPath
        }
    }
    return $matches
}

$installer = (Resolve-Path -LiteralPath $InstallerPath).Path
if ([System.IO.Path]::GetExtension($installer) -ne ".exe") {
    throw "InstallerPath must point to the generated Setup.exe."
}
if ($GoPreview) {
    # A filename alone is not an identity proof. Bind execution to an explicitly
    # inspected artifact before any install directory, environment or cleanup.
    if ($ExpectedInstallerSha256 -notmatch '^[0-9a-fA-F]{64}$') {
        throw "Go Preview requires the verified ExpectedInstallerSha256."
    }
    if ([System.IO.Path]::GetFileName($installer) -ne "Career Hub Go Preview-$expectedAppVersion Setup.exe") {
        throw "Go Preview installer name does not match the selected identity."
    }
    if ((Get-FileHash -LiteralPath $installer -Algorithm SHA256).Hash -ne $ExpectedInstallerSha256) {
        throw "Go Preview installer SHA256 does not match the verified artifact."
    }
}

$systemTemp = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
$testRoot = Join-Path $systemTemp (
    "career-hub-installer-smoke-" + [guid]::NewGuid().ToString("N")
)
if (-not (Test-DescendantPath -Candidate $testRoot -Root $systemTemp)) {
    throw "The disposable installer root escaped the system temp directory."
}

$localAppData = Join-Path $testRoot "localappdata"
$roamingAppData = Join-Path $testRoot "appdata"
$processTemp = Join-Path $testRoot "temp"
$windowsLocalAppData = [Environment]::GetFolderPath("LocalApplicationData")
$installRoot = Join-Path $windowsLocalAppData $installerIdentity
$dataProductRoot = Join-Path $localAppData "CareerHub"
$dataRoot = Join-Path $dataProductRoot $dataProfile
$markerPath = Join-Path $dataRoot "installer-preservation-marker.txt"
$markerValue = [guid]::NewGuid().ToString("N")

$desktopPath = [Environment]::GetFolderPath("Desktop")
$programsPath = [Environment]::GetFolderPath("Programs")
$shortcutRoots = @($desktopPath, $programsPath) | Where-Object { $_ }
$shortcutBefore = @{}
foreach ($root in $shortcutRoots) {
    foreach ($item in Get-ChildItem -LiteralPath $root -Filter $shortcutFilter -ErrorAction SilentlyContinue) {
        $shortcutBefore[$item.FullName] = $true
    }
}

$environmentNames = @("LOCALAPPDATA", "APPDATA", "TEMP", "TMP")
$previousEnvironment = @{}
foreach ($name in $environmentNames) {
    $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}

$result = $null
$failure = $null
$createdInstallRoot = $false
if (Test-Path -LiteralPath $installRoot) {
    throw "The isolated $installerIdentity install root already exists. Remove or preserve it explicitly before this test."
}
if ($GoPreview -and ($shortcutBefore.Count -ne 0 -or @(Get-TestUninstallKeys -InstallRoot $installRoot).Count -ne 0)) {
    throw "Existing Go Preview registration or shortcuts must be preserved; use a clean test identity."
}
try {
    foreach ($directory in @($localAppData, $roamingAppData, $processTemp)) {
        New-Item -ItemType Directory -Path $directory | Out-Null
    }
    $env:LOCALAPPDATA = $localAppData
    $env:APPDATA = $roamingAppData
    $env:TEMP = $processTemp
    $env:TMP = $processTemp

    Invoke-BoundedProcess -FilePath $installer -Arguments @("--silent")
    Wait-ForCondition `
        -Description "the Squirrel install root" `
        -TimeoutSeconds 180 `
        -Condition { Test-Path -LiteralPath $installRoot -PathType Container }
    $createdInstallRoot = $true
    Wait-ForOwnedProcessesToExit -Root $installRoot
    if (Test-Path -LiteralPath $dataProductRoot) {
        throw "Silent installation touched the synthetic CareerHub data root."
    }

    $appExecutable = Get-InstalledExecutable -InstallRoot $installRoot
    Assert-BoundedInstallerPayload `
        -InstallRoot $installRoot `
        -ExpectedAppVersion $expectedAppVersion
    Start-And-VerifyApp `
        -ExecutablePath $appExecutable `
        -InstallRoot $installRoot `
        -DataRoot $dataRoot `
        -ExpectedAppVersion $expectedAppVersion
    [System.IO.File]::WriteAllText($markerPath, $markerValue)
    $homeMarkerPath = Join-Path $dataRoot ".career_hub_data_home.json"
    $homeMarkerHash = (Get-FileHash -LiteralPath $homeMarkerPath -Algorithm SHA256).Hash

    Invoke-BoundedProcess -FilePath $installer -Arguments @("--silent")
    Wait-ForOwnedProcessesToExit -Root $installRoot
    if ([System.IO.File]::ReadAllText($markerPath) -ne $markerValue) {
        throw "Repair/reinstall did not preserve the synthetic Data Home marker."
    }
    $appExecutable = Get-InstalledExecutable -InstallRoot $installRoot
    Assert-BoundedInstallerPayload `
        -InstallRoot $installRoot `
        -ExpectedAppVersion $expectedAppVersion
    Start-And-VerifyApp `
        -ExecutablePath $appExecutable `
        -InstallRoot $installRoot `
        -DataRoot $dataRoot `
        -ExpectedAppVersion $expectedAppVersion

    if ((Get-FileHash -LiteralPath $homeMarkerPath -Algorithm SHA256).Hash -ne $homeMarkerHash) {
        throw "Reinstall or restart changed the Data Home identity."
    }
    if ($GoPreview -and (Test-Path -LiteralPath (Join-Path $dataProductRoot "dev"))) {
        throw "Go Preview created the regular development Data Home."
    }
    $updateExecutable = Join-Path $installRoot "Update.exe"
    Invoke-BoundedProcess `
        -FilePath $updateExecutable `
        -Arguments @("--uninstall", "--silent") `
        -AllowedExitCodes @(0, -1)
    Wait-ForCondition `
        -Description "the installed Career Hub payload to be removed" `
        -TimeoutSeconds 180 `
        -Condition {
            if (-not (Test-Path -LiteralPath $installRoot)) {
                return $true
            }
            $installedApps = @(
                Get-ChildItem `
                    -LiteralPath $installRoot `
                    -Recurse `
                    -File `
                    -Filter "career-hub.exe" `
                    -ErrorAction SilentlyContinue
            )
            return $installedApps.Count -eq 0
        }
    Wait-ForOwnedProcessesToExit -Root $installRoot
    if ([System.IO.File]::ReadAllText($markerPath) -ne $markerValue) {
        throw "Uninstall did not preserve the synthetic Data Home marker."
    }
    if ((Get-FileHash -LiteralPath $homeMarkerPath -Algorithm SHA256).Hash -ne $homeMarkerHash) {
        throw "Uninstall changed the Data Home identity."
    }
    if (@(Get-TestUninstallKeys -InstallRoot $installRoot).Count -ne 0) {
        throw "Uninstall left a current-user registry entry for the disposable install."
    }

    $newShortcuts = @()
    foreach ($root in $shortcutRoots) {
        foreach ($item in Get-ChildItem -LiteralPath $root -Filter $shortcutFilter -ErrorAction SilentlyContinue) {
            if (-not $shortcutBefore.ContainsKey($item.FullName)) {
                $newShortcuts += $item.FullName
            }
        }
    }
    if ($newShortcuts.Count -ne 0) {
        throw "Uninstall left a shortcut created during the disposable test."
    }

    $result = [pscustomobject]@{
        installer = $installer
        installer_identity = $installerIdentity
        runtime = $script:verifiedRuntime
        install_launch = "passed"
        reinstall_launch = "passed"
        installer_payload_retention = "current plus at most one Squirrel rollback payload"
        data_preserved = $true
        uninstall = "passed"
        owned_processes = 0
        squirrel_tombstone = (Test-Path -LiteralPath $installRoot)
        temporary_root_removed = $true
    }
} catch {
    $failure = $_
} finally {
    if (Test-Path -LiteralPath $installRoot) {
        $createdInstallRoot = $true
    }
    if ($createdInstallRoot) {
        Stop-OwnedProcesses -Root $installRoot
    }
    $updateExecutable = Join-Path $installRoot "Update.exe"
    if (Test-Path -LiteralPath $updateExecutable -PathType Leaf) {
        try {
            Invoke-BoundedProcess `
                -FilePath $updateExecutable `
                -Arguments @("--uninstall", "--silent") `
                -AllowedExitCodes @(0, -1) `
                -TimeoutSeconds 120
        } catch {
            if (-not $failure) {
                $failure = $_
            }
        }
    }
    if ($createdInstallRoot) {
        Stop-OwnedProcesses -Root $installRoot
    }

    foreach ($name in $environmentNames) {
        [Environment]::SetEnvironmentVariable(
            $name,
            $previousEnvironment[$name],
            "Process"
        )
    }

    if (Test-Path -LiteralPath $testRoot) {
        if (-not (Test-DescendantPath -Candidate $testRoot -Root $systemTemp)) {
            throw "Refusing to clean an unverified installer test path."
        }
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
    if ($createdInstallRoot -and (Test-Path -LiteralPath $installRoot)) {
        $resolvedInstallRoot = (Resolve-Path -LiteralPath $installRoot).Path
        if (
            (Split-Path -Parent $resolvedInstallRoot) -ne $windowsLocalAppData -or
            (Split-Path -Leaf $resolvedInstallRoot) -ne $installerIdentity
        ) {
            throw "Refusing to clean an unverified installer tombstone path."
        }
        Remove-Item -LiteralPath $resolvedInstallRoot -Recurse -Force
    }
}

if ($failure) {
    throw $failure
}
$result | ConvertTo-Json
