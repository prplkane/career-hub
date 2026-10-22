[CmdletBinding()]
param(
    [switch]$Apply,
    [switch]$IncludeDependencies
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path

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

$relativeTargets = @(
    "desktop\.generated",
    "desktop\out",
    "outputs\desktop",
    "packaging\build",
    "packaging\dist",
    "playwright-report",
    "test-results"
)
if ($IncludeDependencies) {
    $relativeTargets += @("desktop\node_modules", "node_modules")
}

$records = @()
foreach ($relative in $relativeTargets) {
    $candidate = [System.IO.Path]::GetFullPath((Join-Path $projectRoot $relative))
    if (
        -not (Test-DescendantPath -Candidate $candidate -Root $projectRoot) -or
        $candidate -eq $projectRoot
    ) {
        throw "Generated-artifact target escaped the repository boundary."
    }
    if (-not (Test-Path -LiteralPath $candidate)) {
        continue
    }
    $item = Get-Item -LiteralPath $candidate -Force
    if ($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) {
        throw "Refusing to clean a linked generated-artifact target: $relative"
    }
    $bytes = @(
        Get-ChildItem -LiteralPath $candidate -Force -File -Recurse -ErrorAction Stop
    ) | Measure-Object -Property Length -Sum
    $byteCount = if ($null -eq $bytes.Sum) { 0 } else { [long]$bytes.Sum }
    $records += [pscustomobject]@{
        path = $relative
        bytes = $byteCount
        action = if ($Apply) { "removed" } else { "would_remove" }
    }
    if ($Apply) {
        Remove-Item -LiteralPath $candidate -Recurse -Force
    }
}

[pscustomobject]@{
    mode = if ($Apply) { "apply" } else { "preview" }
    dependencies_included = [bool]$IncludeDependencies
    targets = $records
} | ConvertTo-Json -Depth 4
