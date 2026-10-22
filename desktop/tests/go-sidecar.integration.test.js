"use strict";

// Opt-in native integration: a reviewed Go binary and the unchanged Electron
// supervisor, using newly created synthetic roots only. Enable with
// CAREERHUB_TEST_GO_SIDECAR=1, CAREERHUB_GO_SIDECAR and CAREERHUB_GO_SIDECAR_SHA256.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const net = require("node:net");
const { spawn } = require("node:child_process");
const { EventEmitter } = require("node:events");
const test = require("node:test");
const { SidecarSupervisor } = require("../src/sidecar");

const enabled = process.env.CAREERHUB_TEST_GO_SIDECAR === "1";
const root = path.resolve(__dirname, "../..");
const digest = (data) => crypto.createHash("sha256").update(data).digest("hex");

function configuration() {
  const executablePath = process.env.CAREERHUB_GO_SIDECAR;
  const expected = process.env.CAREERHUB_GO_SIDECAR_SHA256;
  assert.ok(typeof executablePath === "string" && path.isAbsolute(executablePath), "absolute reviewed Go binary required");
  assert.ok(typeof expected === "string" && /^[a-f0-9]{64}$/.test(expected), "reviewed binary fingerprint required");
  assert.ok(digest(fs.readFileSync(executablePath)) === expected, "Go binary fingerprint mismatch");
  return { executablePath, expected };
}

function fixture(t, { initialized = true } = {}) {
  const cleanups = [];
  const temporaryRoot = fs.mkdtempSync(path.join(os.tmpdir(), "careerhub-go-sidecar-"));
  const resources = path.join(temporaryRoot, "resources");
  const data = path.join(temporaryRoot, "Données synthetic");
  const files = {
    "resources/VERSION": "0.1.7\n",
    "resources/src/intake_ui_static/index.html": "<!doctype html><title>Synthetic unused UI</title>\n",
    "resources/src/data/skill_groups.yaml": "- {id: tools, name: Tools}\n",
    "resources/src/data/job_technologies.yaml": "- {id: python, name: Python, category: tool, primary_group_id: tools, aliases: [Python3]}\n",
    "Données synthetic/.career_hub_data_home.json": JSON.stringify({ application: "CareerHub", format_version: 2, channel: "dev", data_home_id: `dh_${"1".repeat(32)}` }),
    "Données synthetic/career_vault/taxonomy/skill_groups.yaml": "- {id: user_automation, name: My automation}\n",
    "Données synthetic/career_vault/taxonomy/technologies.yaml": "- {id: user_vba, name: VBA, category: tool, primary_group_id: user_automation, aliases: [Excel VBA]}\n",
  };
  for (const [relative, content] of Object.entries(files)) {
    if (!initialized && relative.startsWith("Données synthetic/")) continue;
    const target = path.join(temporaryRoot, relative);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, content, { flag: "wx" });
  }
  t.after(async () => {
    // Reap every exact owned process before any fixture deletion, even when
    // node:test cancels while an asynchronous body has not reached finally.
    for (const cleanup of cleanups.toReversed()) await cleanup();
    // Delete only this invocation's mkdtemp fixture, never a supplied path.
    assert.equal(path.dirname(temporaryRoot), path.resolve(os.tmpdir()));
    assert.ok(path.basename(temporaryRoot).startsWith("careerhub-go-sidecar-"));
    fs.rmSync(temporaryRoot, { recursive: true, force: true });
  });
  return { temporaryRoot, resources, data, ownCleanup: (cleanup) => cleanups.push(cleanup) };
}

function snapshot(directory) {
  const result = {};
  function walk(current) {
    const info = fs.lstatSync(current);
    assert.ok(!info.isSymbolicLink(), "unexpected fixture link");
    const relative = path.relative(directory, current);
    // ADR SEC-250: session/ holds per-start connection state (the agent
    // connection file is rewritten at start and removed at shutdown); it is
    // not durable Home state. These tests never pair the extension.
    if (relative === "session" || relative.startsWith("session" + path.sep)) return;
    if (info.isDirectory()) {
      result[relative] = { mode: info.mode };
      for (const name of fs.readdirSync(current).sort()) walk(path.join(current, name));
    } else {
      assert.ok(info.isFile(), "unexpected fixture file type");
      result[relative] = { mode: info.mode, mtimeMs: info.mtimeMs, hash: digest(fs.readFileSync(current)) };
    }
  }
  walk(directory);
  return result;
}

function ownChild(child, ownCleanup) {
  let closed = false;
  const closedPromise = new Promise((done) => { child.once("close", () => { closed = true; done(); }); });
  ownCleanup(async () => {
    if (closed) return;
    child.kill();
    let cleanupTimer;
    try {
      await Promise.race([closedPromise, new Promise((_, fail) => { cleanupTimer = setTimeout(() => fail(new Error("owned child not reaped; fixture retained")), 2000); })]);
    } finally { clearTimeout(cleanupTimer); }
  });
  return closedPromise;
}


// ADR SEC-250: no route returns the window token. The supervisor generated it
// and sent it in the start record; /api/session needs it and returns
// capabilities only.
async function windowSession(supervisor, origin) {
  assert.equal((await request(origin, "/api/session")).status, 403);
  const token = supervisor.sessionToken;
  assert.match(token, /^[A-Za-z0-9_-]{43}$/);
  const session = await request(origin, "/api/session", { "X-Career-Hub-Token": token });
  assert.equal(session.status, 200);
  assert.equal(session.body.token, undefined, "no route may return a token");
  return { ...session, body: { ...session.body, token } };
}

async function request(origin, target, headers = {}) {
  const response = await fetch(`${origin}${target}`, { headers, redirect: "error", signal: AbortSignal.timeout(5000) });
  const reader = response.body.getReader();
  const parts = [];
  let total = 0;
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      total += value.length;
      assert.ok(total <= 4 * 1024 * 1024, "HTTP response exceeded bound");
      parts.push(Buffer.from(value));
    }
  } finally {
    await reader.cancel();
  }
  return { status: response.status, headers: response.headers, body: JSON.parse(Buffer.concat(parts).toString("utf8")) };
}

function parseRecords(raw) {
  if (raw.length === 0) return [];
  if (raw.at(-1) !== 10) throw new Error("unterminated protocol record");
  const decoder = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });
  const records = [];
  let start = 0;
  for (let end = raw.indexOf(10); end !== -1; end = raw.indexOf(10, start)) {
    let line = raw.subarray(start, end);
    if (line.at(-1) === 13) line = line.subarray(0, line.length - 1);
    if (line.length === 0 || line.length > 8192) throw new Error("invalid protocol record size");
    const value = JSON.parse(decoder.decode(line));
    if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid protocol shape");
    records.push(value);
    start = end + 1;
  }
  return records;
}

test("native protocol harness rejects invalid UTF8, oversized and unterminated records", () => {
  for (const raw of [Buffer.from('{"event":"ready"}'), Buffer.from('{"event":"ready"}\n '), Buffer.from('\uFEFF{}\n'), Buffer.concat([Buffer.from('{"x":"'), Buffer.from([255]), Buffer.from('"}\n')]), Buffer.from(`{"x":"${"x".repeat(8192)}"}\n`)]) {
    assert.throws(() => parseRecords(raw));
  }
  assert.deepEqual(parseRecords(Buffer.from('{}\r\n{}\n')), [{}, {}]);
});

test("fixture cleanup awaits child close rather than only a rejected operation", async () => {
  const child = new EventEmitter();
  let killed = false, finished = false;
  child.kill = () => { killed = true; };
  let cleanup;
  ownChild(child, (value) => { cleanup = value; });
  const pending = cleanup().then(() => { finished = true; });
  await Promise.resolve();
  assert.ok(killed && !finished, "cleanup completed before process close");
  child.emit("close");
  await pending;
  assert.ok(finished);
});

function rawRun(t, ownCleanup, executablePath, args, input, { reportTiming = false } = {}) {
  return new Promise((resolve, reject) => {
    t.signal.throwIfAborted();
    const began = performance.now();
    let spawnedMs = null, firstOutputMs = null, exitedMs = null;
    const child = spawn(executablePath, args, { windowsHide: true, shell: false, stdio: ["pipe", "pipe", "pipe"] });
    ownChild(child, ownCleanup);
    const stdout = [];
    let outBytes = 0, errBytes = 0, failureReason;
    const failAndKill = (reason) => { failureReason ??= reason; child.kill(); };
    const abort = () => failAndKill("test-canceled");
    t.signal.addEventListener("abort", abort, { once: true });
    const timer = setTimeout(() => failAndKill("harness-deadline"), 14000);
    child.once("spawn", () => { spawnedMs = Math.round(performance.now() - began); });
    child.once("exit", () => { exitedMs = Math.round(performance.now() - began); });
    child.stdin.on("error", () => {});
    child.stdout.on("data", (chunk) => {
      firstOutputMs ??= Math.round(performance.now() - began);
      outBytes += chunk.length;
      if (outBytes > 16384) failAndKill("stdout-limit"); else stdout.push(chunk);
    });
    child.stderr.on("data", (chunk) => {
      errBytes += chunk.length;
      if (errBytes > 4096) failAndKill("stderr-limit");
    });
    child.on("error", () => { clearTimeout(timer); reject(new Error("synthetic subprocess could not start")); });
    child.on("close", (code, signal) => {
      t.signal.removeEventListener("abort", abort);
      clearTimeout(timer);
      const timing = `elapsed_ms=${Math.round(performance.now() - began)} spawn_ms=${spawnedMs} first_output_ms=${firstOutputMs} exit_ms=${exitedMs} stdout_bytes=${outBytes} stderr_bytes=${errBytes}`;
      // Only fixed categories and numeric measurements, never raw pipe output,
      // paths, a readiness token or arbitrary operating-system error text.
      failureReason ??= signal ? "signaled-exit" : errBytes ? "stderr-present" : undefined;
      if (failureReason) return reject(new Error(`synthetic subprocess boundary failed: ${failureReason}; ${timing}`));
      if (reportTiming) t.diagnostic(timing);
      try {
        const lines = parseRecords(Buffer.concat(stdout));
        resolve({ code, lines });
      } catch { reject(new Error("synthetic protocol response invalid")); }
    });
    if (input !== null) child.stdin.end(input);
  });
}

test("Go subprocess rejects invalid framing and stops on parent EOF", { skip: !enabled, timeout: 15000 }, async (t) => {
  const { executablePath } = configuration();
  const { temporaryRoot, resources, data, ownCleanup } = fixture(t);
  const before = snapshot(temporaryRoot);
  const start = JSON.stringify({ command: "start", protocol_version: 1, port: 0, resource_root: resources, data_root: data, channel: "dev" }) + "\n";
  const eof = await rawRun(t, ownCleanup, executablePath, ["--sidecar"], start);
  assert.equal(eof.code, 0);
  // EOF is now observed during preparation. Depending on delivery scheduling it
  // can precede readiness; deterministic blocked-preparation tests cover that
  // ordering. A readiness already dispatched cannot be retracted.
  assert.ok(eof.lines.length <= 1);
  if (eof.lines.length) assert.equal(eof.lines[0].event, "ready");
  const duplicate = await rawRun(t, ownCleanup, executablePath, ["--sidecar"], start + start);
  assert.equal(duplicate.code, 3);
  assert.ok(duplicate.lines.length >= 1 && duplicate.lines.length <= 2);
  assert.equal(duplicate.lines.at(-1).error_code, "SIDECAR_RECORD_INVALID_FIELDS");
  if (duplicate.lines.length === 2) assert.equal(duplicate.lines[0].event, "ready");
  for (const [input, errorCode] of [["{", "SIDECAR_RECORD_TRUNCATED"], ["[]\n", "SIDECAR_RECORD_INVALID_SHAPE"], ["{invalid}\n", "SIDECAR_RECORD_INVALID_JSON"]]) {
    const result = await rawRun(t, ownCleanup, executablePath, ["--sidecar"], input);
    assert.equal(result.code, 3);
    assert.deepEqual(result.lines, [{ event: "error", protocol_version: 1, error_code: errorCode }]);
  }
  assert.deepEqual(await rawRun(t, ownCleanup, executablePath, [], ""), { code: 2, lines: [] });
  assert.deepEqual(snapshot(temporaryRoot), before);
});

test("Go subprocess closes silent parent pipes after the native startup bound", { skip: !enabled, timeout: 16000 }, async (t) => {
  const { executablePath } = configuration();
  const result = await rawRun(t, (cleanup) => t.after(cleanup), executablePath, ["--sidecar"], null, { reportTiming: true });
  assert.equal(result.code, 3);
  assert.deepEqual(result.lines, [{ event: "error", protocol_version: 1, error_code: "SIDECAR_START_TIMEOUT" }]);
});

test("native harness reaps an exact child when test input is canceled", { skip: !enabled, timeout: 5000 }, async (t) => {
  const { executablePath } = configuration();
  const controller = new AbortController();
  const signal = AbortSignal.any([t.signal, controller.signal]);
  const result = rawRun({ signal }, (cleanup) => t.after(cleanup), executablePath, ["--sidecar"], null);
  const timer = setTimeout(() => controller.abort(), 50);
  try { await assert.rejects(result, /synthetic subprocess boundary failed/); } finally { clearTimeout(timer); }
});

// Test-only executable-mode selection. Production supervisor source and its
// protocol, health validation, deadlines and exact-child ownership stay intact.
function createOwnedSidecarChild(executable, args, spawnOptions, signal, { bootstrap = false, spawnChild = spawn } = {}) {
  // A canceled node:test body may resume after fixture cleanup has started.
  // Never let that continuation register a new owned process too late to reap.
  signal.throwIfAborted();
  assert.deepEqual(args, ["--sidecar"]);
  assert.equal(spawnOptions.shell, false);
  assert.equal(spawnOptions.windowsHide, true);
  return spawnChild(executable, bootstrap ? ["--sidecar-bootstrap"] : args, spawnOptions);
}

test("sidecar harness refuses late child creation after cancellation in either mode", () => {
  for (const bootstrap of [false, true]) {
    const controller = new AbortController();
    const child = {};
    let calls = 0;
    const fakeSpawn = (executable, args) => {
      calls += 1;
      assert.equal(executable, "synthetic-executable");
      assert.deepEqual(args, bootstrap ? ["--sidecar-bootstrap"] : ["--sidecar"]);
      return child;
    };
    const start = () => createOwnedSidecarChild("synthetic-executable", ["--sidecar"], { shell: false, windowsHide: true }, controller.signal, { bootstrap, spawnChild: fakeSpawn });
    assert.equal(start(), child);
    controller.abort();
    assert.throws(start, { name: "AbortError" });
    assert.equal(calls, 1, "aborted continuation created an unowned child");
  }
});

function ownedSupervisor(ownCleanup, signal, { bootstrap = false, spawnChild = spawn, ...options } = {}) {
  const childrenClosed = [];
  const supervisor = new SidecarSupervisor({
    ...options,
    spawn(executable, args, spawnOptions) {
      const child = createOwnedSidecarChild(executable, args, spawnOptions, signal, { bootstrap, spawnChild });
      childrenClosed.push(ownChild(child, ownCleanup));
      return child;
    },
  });
  let stopping;
  const stop = () => {
    if (!stopping) {
      stopping = supervisor.stop().then(async () => {
        let timer;
        try {
          await Promise.race([Promise.all(childrenClosed), new Promise((_, reject) => {
            timer = setTimeout(() => reject(new Error("owned sidecar child not closed; fixture retained")), 2000);
          })]);
        } finally { clearTimeout(timer); }
      }).finally(() => { stopping = undefined; });
    }
    return stopping;
  };
  ownCleanup(stop);
  return { supervisor, stop };
}

test("unchanged supervisor reaps a timed-out Go child before safe restart", {
  skip: !enabled, timeout: 20000,
}, async (t) => {
  const { executablePath } = configuration();
  const { temporaryRoot, resources, data, ownCleanup } = fixture(t);
  const before = snapshot(temporaryRoot);
  const began = performance.now();
  let withholdStart = true, firstChild, firstInput, firstExitMs, firstCloseMs, firstOutputMs;
  let withheldWrites = 0;
  const runtime = ownedSupervisor(ownCleanup, t.signal, {
    spawnChild(executable, args, options) {
      const child = spawn(executable, args, options);
      if (withholdStart) {
        firstChild = child;
        firstInput = child.stdin;
        // Fault injection is only at this test-owned parent pipe. The actual
        // supervisor, child executable, timeouts and native pipe stay unchanged.
        // In particular destroy() still closes the real pipe during error cleanup.
        child.stdin.write = function (...values) {
          withheldWrites++;
          const callback = values.at(-1);
          if (typeof callback === "function") queueMicrotask(() => callback());
          return true;
        };
        child.stdout.once("data", () => { firstOutputMs = Math.round(performance.now() - began); });
        child.once("exit", () => { firstExitMs = Math.round(performance.now() - began); });
        child.once("close", () => { firstCloseMs = Math.round(performance.now() - began); });
      }
      return child;
    },
  });
  const start = { executablePath, deploymentRoot: resources, dataRoot: data, channel: "dev", appVersion: "0.1.7" };
  try {
    await assert.rejects(runtime.supervisor.start(start), {
      code: "DESKTOP_SIDECAR_REPORTED_ERROR", details: { reportedCode: "SIDECAR_START_TIMEOUT" },
    });
    assert.equal(withheldWrites, 1, "fixture did not withhold exactly one start record");
    assert.ok(firstInput.destroyed, "startup rejection left the native parent input open");
    assert.ok(Number.isInteger(firstExitMs), "startup rejection preceded exact child exit");
    assert.ok(firstChild.exitCode !== null || firstChild.signalCode !== null);
    assert.equal(runtime.supervisor.ownsChild, false);
    assert.equal(runtime.supervisor.state, "idle");
    await runtime.stop(); // Additionally await native pipe close before fixtures/retry.
    assert.ok(Number.isInteger(firstCloseMs), "test cleanup did not await exact child close");
    assert.deepEqual(snapshot(temporaryRoot), before, "silent startup touched the Data Home");
    t.diagnostic(`timeout_output_ms=${firstOutputMs} exit_ms=${firstExitMs} close_ms=${firstCloseMs}`);
    t.signal.throwIfAborted();
    withholdStart = false;
    const ready = await runtime.supervisor.start(start);
    assert.equal((await request(ready.origin, "/api/health")).status, 200);
    assert.equal(runtime.supervisor.state, "ready");
  } finally {
    await runtime.stop();
  }
  assert.deepEqual(snapshot(temporaryRoot), before);
});

for (const bootstrap of [false, true]) {
  test(`Data Home ownership excludes the other startup mode after ${bootstrap ? "bootstrap" : "read-only"} readiness`, {
    skip: !enabled || process.platform !== "win32", timeout: 20000,
  }, async (t) => {
    const { executablePath } = configuration();
    const { resources, data, ownCleanup } = fixture(t, { initialized: false });
    const resourceBefore = snapshot(resources);
    const owner = ownedSupervisor(ownCleanup, t.signal, { bootstrap });
    const contender = ownedSupervisor(ownCleanup, t.signal, { bootstrap: !bootstrap });
    const start = { executablePath, deploymentRoot: resources, dataRoot: data, channel: "dev", appVersion: "0.1.7" };
    let before;
    try {
      if (!bootstrap) {
        // Use a fully initialized home: a later bootstrap may legitimately
        // fill missing defaults in the registry-only read fixture.
        await contender.supervisor.start(start);
        await contender.stop();
        t.signal.throwIfAborted();
      }
      const { origin } = await owner.supervisor.start(start);
      t.signal.throwIfAborted();
      before = snapshot(data);
      // Channel is not an ownership key. Initialized read admission also
      // accepts case aliases; creation intentionally requires stored spelling.
      // For either valid path the claim precedes reading the private dev marker.
      const contenderRoot = bootstrap ? data.toUpperCase() : data;
      await assert.rejects(contender.supervisor.start({ ...start, dataRoot: contenderRoot, channel: "stable" }), {
        code: "DESKTOP_SIDECAR_REPORTED_ERROR", details: { reportedCode: "DATA_HOME_IN_USE" },
      });
      await contender.stop();
      t.signal.throwIfAborted();
      assert.equal(contender.supervisor.ownsChild, false);
      assert.equal(owner.supervisor.state, "ready");
      assert.equal((await request(origin, "/api/health")).status, 200);
      assert.deepEqual(snapshot(data), before, "contending backend modified private files");
      await owner.stop(); // Includes exact-child close, not just Run returning.
      t.signal.throwIfAborted();
      const reopened = await contender.supervisor.start(start);
      assert.equal((await request(reopened.origin, "/api/health")).status, 200);
      assert.deepEqual(snapshot(data), before, "restart modified the initialized home");
    } finally {
      await contender.stop();
      await owner.stop();
    }
    assert.deepEqual(snapshot(data), before);
    assert.deepEqual(snapshot(resources), resourceBefore);
  });
}

test("bootstrap subprocess refuses invalid resources, overlap and recovery without seeding", {
  skip: !enabled || process.platform !== "win32", timeout: 15000,
}, async (t) => {
  const { executablePath } = configuration();
  const { temporaryRoot, resources, data, ownCleanup } = fixture(t, { initialized: false });
  const { supervisor, stop } = ownedSupervisor(ownCleanup, t.signal, { bootstrap: true });
  const start = { executablePath, deploymentRoot: resources, dataRoot: data, channel: "dev", appVersion: "0.1.7" };
  const before = snapshot(temporaryRoot);
  await assert.rejects(supervisor.start({ ...start, dataRoot: path.join(resources, "uncreated-data") }), {
    code: "DESKTOP_SIDECAR_REPORTED_ERROR", details: { reportedCode: "SIDECAR_PATH_BOUNDARY_INVALID" },
  });
  await stop();
  t.signal.throwIfAborted();
  assert.deepEqual(snapshot(temporaryRoot), before);
  fs.writeFileSync(path.join(resources, "VERSION"), "not-a-version\n");
  const invalidBefore = snapshot(temporaryRoot);
  await assert.rejects(supervisor.start(start), {
    code: "DESKTOP_SIDECAR_REPORTED_ERROR", details: { reportedCode: "SIDECAR_RESOURCE_INVALID" },
  });
  await stop();
  t.signal.throwIfAborted();
  assert.equal(fs.existsSync(data), false);
  assert.deepEqual(snapshot(temporaryRoot), invalidBefore);
  fs.writeFileSync(path.join(resources, "VERSION"), "0.1.7\n");
  const journal = path.join(data, "backups", "document-encryption-migration", "active.json");
  fs.mkdirSync(path.dirname(journal), { recursive: true });
  fs.writeFileSync(journal, "synthetic unparsed recovery marker", { flag: "wx" });
  const recoveryBefore = snapshot(temporaryRoot);
  await assert.rejects(supervisor.start(start), {
    code: "DESKTOP_SIDECAR_REPORTED_ERROR", details: { reportedCode: "DATA_HOME_RECOVERY_REQUIRED" },
  });
  await stop();
  t.signal.throwIfAborted();
  assert.deepEqual(snapshot(temporaryRoot), recoveryBefore);
  assert.equal(supervisor.ownsChild, false);
});

test("bootstrap failure after marker publication preserves private input and restart identity", {
  skip: !enabled || process.platform !== "win32", timeout: 15000,
}, async (t) => {
  const { executablePath } = configuration();
  const { resources, data, ownCleanup } = fixture(t, { initialized: false });
  const resourceBefore = snapshot(resources);
  const privateCatalog = path.join(data, "career_vault", "taxonomy", "technologies.yaml");
  fs.mkdirSync(path.dirname(privateCatalog), { recursive: true });
  const invalid = Buffer.from("synthetic-invalid-model: true\n");
  fs.writeFileSync(privateCatalog, invalid, { flag: "wx" });
  const { supervisor, stop } = ownedSupervisor(ownCleanup, t.signal, { bootstrap: true });
  const start = { executablePath, deploymentRoot: resources, dataRoot: data, channel: "dev", appVersion: "0.1.7" };
  await assert.rejects(supervisor.start(start), {
    code: "DESKTOP_SIDECAR_REPORTED_ERROR", details: { reportedCode: "SIDECAR_SCHEMA_INITIALIZATION_FAILED" },
  });
  await stop();
  t.signal.throwIfAborted();
  assert.equal(supervisor.ownsChild, false);
  assert.deepEqual(fs.readFileSync(privateCatalog), invalid, "bootstrap rewrote private invalid input");
  const markerPath = path.join(data, ".career_hub_data_home.json");
  const markerBefore = fs.readFileSync(markerPath);
  assert.match(JSON.parse(markerBefore).data_home_id, /^dh_[a-f0-9]{32}$/);
  // Correct only this invocation's synthetic private file, after exact-child
  // close. Retry must not reset a home whose startup failed after publication.
  fs.writeFileSync(privateCatalog, "[]\n");
  const beforeRetry = snapshot(data);
  let afterRetry;
  try {
    const { origin } = await supervisor.start(start);
    const session = await windowSession(supervisor, origin);
    assert.equal((await request(origin, "/api/skills-registry", { "X-Career-Hub-Token": session.body.token })).status, 200);
    assert.deepEqual(fs.readFileSync(markerPath), markerBefore);
    afterRetry = snapshot(data);
    const earlierFiles = { ...afterRetry };
    assert.ok(earlierFiles[path.join("applications", "career_hub.sqlite3")], "successful retry did not prepare SQLite");
    delete earlierFiles[path.join("applications", "career_hub.sqlite3")];
    assert.deepEqual(earlierFiles, beforeRetry);
  } finally { await stop(); }
  assert.equal(supervisor.ownsChild, false);
  assert.deepEqual(snapshot(resources), resourceBefore);
  assert.deepEqual(snapshot(data), afterRetry);
});
