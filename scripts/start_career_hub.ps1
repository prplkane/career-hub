<# Start the native Go-backed desktop development application. #>
[CmdletBinding()]
param(
    [switch]$EnableExtension,
    [ValidatePattern('^(?:(?:chrome|moz)-extension|safari-web-extension)://[A-Za-z0-9._-]+/?$')]
    [string]$ExtensionOrigin,
    [switch]$GoPreview
)
$ErrorActionPreference = 'Stop'
if ($EnableExtension -and $ExtensionOrigin) {
    throw '-EnableExtension and -ExtensionOrigin cannot be used together.'
}
$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$desktopRoot = Join-Path $projectRoot 'desktop'
$electronCli = Join-Path $desktopRoot 'node_modules/electron/cli.js'
if (-not (Test-Path -LiteralPath $electronCli -PathType Leaf)) {
    throw 'Desktop dependencies are missing. Run npm --prefix desktop ci with Node 24.19.0.'
}
$node = (Get-Command node -ErrorAction Stop).Source
if ((& $node --version) -ne 'v24.19.0') {
    throw 'Desktop development requires Node 24.19.0 on PATH.'
}
$desktopArgs = @($electronCli, $desktopRoot)
if ($GoPreview) { $desktopArgs += '--go-preview' }
if ($EnableExtension) { $desktopArgs += '--enable-extension' }
if ($ExtensionOrigin) { $desktopArgs += @('--extension-origin', $ExtensionOrigin) }
# The desktop host verifies the fixed Go artifact and owns Data Home selection,
# conversion confirmations and child lifecycle. There is no Python fallback.
& $node @desktopArgs
exit $LASTEXITCODE
