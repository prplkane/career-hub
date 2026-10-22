"use strict";

// Runs the unmodified packaged EXE, not an Electron source-entry wrapper. Only
// fictional data in a fresh temporary LOCALAPPDATA/APPDATA is used. No mailbox,
// installer, update feed, private profile or remote-debugging port is enabled.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const crypto = require("node:crypto");
const { spawn, execFileSync } = require("node:child_process");
const repo = path.resolve(__dirname, "..");
const packageRoot = path.join(repo, "desktop/out/go-preview/career-hub-go-preview-win32-x64");
const executable = path.join(packageRoot, "career-hub.exe");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const hash = (file) => crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
function powershell(command) {
  return execFileSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", command],
    { encoding: "utf8", windowsHide: true, timeout: 12000, maxBuffer: 65536 }).trim();
}
function records(command) {
  const text = powershell(command + " | ConvertTo-Json -Compress");
  return text ? [].concat(JSON.parse(text)) : [];
}

function agentConnectionFile(root) {
  const base = path.join(root, "Local", "CareerHub");
  const found = fs.readdirSync(base).map((channel) => path.join(base, channel, "session", "agent-connection.json")).filter((file) => fs.existsSync(file));
  assert.equal(found.length, 1, "expected exactly one published agent connection file");
  return found[0];
}

async function run(root, index) {
  const environment = { ...process.env, LOCALAPPDATA: path.join(root, "Local"), APPDATA: path.join(root, "Roaming") };
  delete environment.ELECTRON_RUN_AS_NODE;
  const child = spawn(executable, [], { cwd: packageRoot, env: environment, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
  let logs = "", origin, sidecarPID;
  child.stdout.on("data", (chunk) => { logs = (logs + chunk).slice(-8000); });
  child.stderr.on("data", (chunk) => { logs = (logs + chunk).slice(-8000); });
  const exited = new Promise((resolve, reject) => { child.once("exit", resolve); child.once("error", reject); });
  try {
    const deadline = Date.now() + 30000;
    while (Date.now() < deadline && !origin) {
      assert.equal(child.exitCode, null, `packaged app exited: ${logs}`);
      const owned = records(`Get-CimInstance Win32_Process -Filter "ParentProcessId = ${child.pid} AND Name = 'career-hub-sidecar.exe'" | Select-Object ProcessId,ExecutablePath`);
      assert.ok(owned.length <= 1, "more than one owned sidecar");
      if (owned.length) {
        assert.equal(path.resolve(owned[0].ExecutablePath).toLowerCase(), path.join(packageRoot, "resources/career-hub-sidecar/career-hub-sidecar.exe").toLowerCase());
        sidecarPID = owned[0].ProcessId;
        const ports = records(`Get-NetTCPConnection -OwningProcess ${sidecarPID} -State Listen -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -eq '127.0.0.1' } | Select-Object LocalPort`);
        assert.ok(ports.length <= 1, "unexpected sidecar listeners");
        if (ports.length) origin = `http://127.0.0.1:${ports[0].LocalPort}`;
      }
      if (!origin) await sleep(200);
    }
    assert.ok(origin, `no packaged Go readiness: ${logs}`);
    // ADR SEC-250: an outside program gets no token from HTTP...
    const tokenless = await fetch(origin + "/api/session", { signal: AbortSignal.timeout(5000) });
    assert.equal(tokenless.status, 403);
    assert.equal(JSON.stringify(await tokenless.json()).includes("token\":"), false);
    // ...only the owner-only agent connection file, whose token is scoped.
    const connectionPath = agentConnectionFile(root);
    const connection = JSON.parse(fs.readFileSync(connectionPath, "utf8"));
    assert.equal(connection.server, origin);
    assert.match(connection.token, /^[A-Za-z0-9_-]{43}$/);
    const agentHeaders = { "X-Career-Hub-Token": connection.token };
    const capabilities = await (await fetch(origin + "/api/session", { headers: agentHeaders, signal: AbortSignal.timeout(5000) })).json();
    assert.equal(capabilities.token, undefined);
    assert.equal(capabilities.capabilities.email_connection, false);
    for (const [method, route] of [["GET", "/api/vault/setup"], ["POST", "/api/discoveries/email-imports"]]) {
      const outside = await fetch(origin + route, { method, headers: agentHeaders, signal: AbortSignal.timeout(5000) });
      assert.equal(outside.status, 403, `agent token reached ${route}`);
    }
    // Closing this exact test app's window traverses real Electron shutdown.
    assert.equal(powershell(`(Get-Process -Id ${child.pid}).CloseMainWindow()`), "True", "packaged window was not available for normal close");
    let timer;
    try {
      assert.equal(await Promise.race([exited, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("normal close timed out")), 12000); })]), 0);
    } finally { clearTimeout(timer); }
    await assert.rejects(fetch(origin + "/api/health", { signal: AbortSignal.timeout(1000) }), "owned listener survived app quit");
    assert.equal(records(`Get-CimInstance Win32_Process -Filter "ProcessId = ${sidecarPID}" | Select-Object ProcessId`).length, 0);
    assert.equal(fs.existsSync(connectionPath), false, "normal close left the agent connection file");
    return { token: connection.token };
  } finally {
    // Failure cleanup is limited to the still-owned test process tree. Never
    // terminate an existing application by name or touch an installed product.
    if (child.exitCode === null) {
      execFileSync("taskkill.exe", ["/PID", String(child.pid), "/T", "/F"], { windowsHide: true, stdio: "ignore" });
      await exited;
    }
  }
}

(async () => {
  assert.equal(process.platform, "win32");
  const { listPackage } = require(path.join(repo, "desktop/node_modules/@electron/asar"));
  const entries = listPackage(path.join(packageRoot, "resources/app.asar"));
  assert.ok(entries.length > 0);
  assert.equal(entries.some((entry) => /^\/(?:out|\.generated|scripts|tests)(?:\/|$)/.test(entry.replaceAll("\\", "/"))), false,
    "generated output or development-only files were embedded in app.asar");
  const metadata = JSON.parse(fs.readFileSync(path.join(packageRoot, "resources/career-hub/build-metadata.json"), "utf8"));
  assert.equal(metadata.sidecar_runtime, "go-preview");
  assert.equal(metadata.update_feed_url, null);
  const manifest = JSON.parse(fs.readFileSync(path.join(packageRoot, "resources/career-hub-sidecar/sidecar-manifest.json"), "utf8"));
  assert.equal(manifest.layout, "go-onedir");
  assert.equal(manifest.files.some((entry) => /\.py[co]?$/.test(entry.path)), false);
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-go-package-"));
  const marker = path.join(root, "Local/CareerHub/dev-go-preview/.career_hub_data_home.json");
  console.log(JSON.stringify({ stage: "starting", root, sourceCommit: manifest.source_commit }));
  const first = await run(root, 0);
  const identity = hash(marker);
  const second = await run(root, 1);
  assert.equal(hash(marker), identity);
  assert.notEqual(first.token, second.token, "the agent token did not rotate on restart");
  assert.equal(fs.existsSync(path.join(root, "Local/CareerHub/dev")), false);
  console.log(JSON.stringify({ result: "PASS", root, sourceCommit: manifest.source_commit,
    scope: "Unmodified packaged Windows Go app: synthetic first run, SEC-250 admission (no HTTP token, scoped agent file), restart token rotation, normal close; import/dedup are covered by smoke:native-desktop; no installer, live email, full UI or update qualification." }));
})().catch((error) => { console.error(error.stack); process.exitCode = 1; });
