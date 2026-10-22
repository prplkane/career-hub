"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const { spawnSync } = require("node:child_process");
const test = require("node:test");

// The scripts finish in seconds, but a cold PowerShell start on a hosted CI
// runner has exceeded 15 seconds; bound a hang without failing on slow starts.
const POWERSHELL_TIMEOUT_MS = 60000;

test("installed runtime gate admits only matching native standard/preview payloads", { skip: process.platform !== "win32" }, () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-runtime-admission-"));
  const source = path.resolve(__dirname, "../scripts/smoke-windows-installer.ps1");
  const environment = { ...process.env, CH_RUNTIME_TEST_ROOT: root, CH_RUNTIME_TEST_SOURCE: source };
  for (const key of Object.keys(environment)) if (key.toLowerCase() === "psmodulepath") delete environment[key];
  // Execute the actual admission function in isolation, without installer,
  // registry, process or user Home operations.
  const script = `
$ErrorActionPreference = 'Stop'
$ast = [System.Management.Automation.Language.Parser]::ParseFile($env:CH_RUNTIME_TEST_SOURCE, [ref]$null, [ref]$null)
$definition = $ast.Find({param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Assert-NativeInstalledRuntime'}, $true)
Invoke-Expression $definition.Extent.Text
$root = $env:CH_RUNTIME_TEST_ROOT
New-Item -ItemType Directory -Path (Join-Path $root 'career-hub'), (Join-Path $root 'career-hub-sidecar') | Out-Null
foreach ($preview in @($false, $true)) {
  $GoPreview = $preview
  $schema = if ($preview) { 3 } else { 4 }
  $runtime = if ($preview) { 'go-preview' } else { 'go' }
  foreach ($mode in @('valid', 'python', 'wrong-schema', 'wrong-identity', 'python-file', 'empty-manifest', 'wrong-layout')) {
    $metadata = @{schema_version=$schema; sidecar_runtime=$runtime; channel='dev'; update_feed_url=$null}
    $manifest = @{layout='go-onedir'; files=@(@{path='career-hub-sidecar.exe'})}
    switch ($mode) {
      'python' { $metadata.sidecar_runtime = 'python' }
      'wrong-schema' { $metadata.schema_version = 2 }
      'wrong-identity' { $metadata.sidecar_runtime = if ($preview) { 'go' } else { 'go-preview' } }
      'python-file' { $manifest.files += @{path='hidden/runtime.pyc'} }
      'empty-manifest' { $manifest.files = @() }
      'wrong-layout' { $manifest.layout = 'pyinstaller-onedir' }
    }
    $metadata | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $root 'career-hub/build-metadata.json')
    $manifest | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $root 'career-hub-sidecar/sidecar-manifest.json')
    $admitted = $false
    try { $observed = Assert-NativeInstalledRuntime -Resources $root; $admitted = $true } catch {
      if ($mode -eq 'valid') { throw }
    }
    if ($admitted -ne ($mode -eq 'valid')) { throw "Unexpected admission: $preview/$mode" }
    if ($admitted -and $observed -ne $runtime) { throw 'Incorrect runtime result' }
  }
}
exit 0
`;
  try {
    const result = spawnSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", script],
      { encoding: "utf8", windowsHide: true, timeout: POWERSHELL_TIMEOUT_MS, env: environment });
    assert.ifError(result.error);
    assert.equal(result.status, 0, result.stderr || result.stdout);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test("Go installer gate refuses unverified artifacts before executing them", { skip: process.platform !== "win32" }, () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-installer-admission-"));
  const project = path.resolve(__dirname, "../..");
  const version = fs.readFileSync(path.join(project, "VERSION"), "utf8").trim();
  const installer = path.join(root, `Career Hub Go Preview-${version} Setup.exe`);
  const bytes = Buffer.from("not an executable: admission must reject before launch");
  fs.writeFileSync(installer, bytes);
  const environment = { ...process.env };
  // Windows PowerShell must discover its own built-in modules, not inherit a
  // different PowerShell host's module search path from the test runner.
  for (const key of Object.keys(environment)) {
    if (key.toLowerCase() === "psmodulepath") delete environment[key];
  }
  const run = (file, extra) => spawnSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
    "-File", path.join(project, "desktop/scripts/smoke-windows-installer.ps1"), "-InstallerPath", file, "-GoPreview", ...extra],
  { encoding: "utf8", windowsHide: true, timeout: POWERSHELL_TIMEOUT_MS, env: environment });
  try {
    for (const [file, extra, expected] of [
      [installer, [], /requires the verified ExpectedInstallerSha256/],
      [installer, ["-ExpectedInstallerSha256", "0".repeat(64)], /SHA256 does not match/],
      [path.join(root, "Other Setup.exe"), ["-ExpectedInstallerSha256", crypto.createHash("sha256").update(bytes).digest("hex")], /name does not match/],
    ]) {
      if (!fs.existsSync(file)) fs.writeFileSync(file, bytes);
      const result = run(file, extra);
      assert.ifError(result.error);
      assert.equal(result.status, 1);
      assert.match(result.stderr, expected);
    }
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
