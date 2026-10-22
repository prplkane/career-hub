import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { execFile, spawn } from "node:child_process";
import { constants } from "node:fs";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { StepJournal, requestSummary } from "./journal.mjs";
import { stopOwnedService } from "./cleanup.mjs";

const repo = path.resolve(fileURLToPath(new URL("../../", import.meta.url)));
const runFile = promisify(execFile);

// env/runGo are internal test seams; normal runs use the process environment.
export async function buildFixture({ env = process.env, runGo = runFile } = {}) {
  const suppliedBinary = env.CAREER_HUB_SIDECAR_BINARY?.trim();
  if (suppliedBinary) {
    const binary = path.resolve(repo, suppliedBinary);
    try {
      if (!(await fs.stat(binary)).isFile()) throw new Error("not a file");
      await fs.access(binary, constants.R_OK | constants.X_OK);
    } catch (cause) {
      throw new Error("Workflow setup: CAREER_HUB_SIDECAR_BINARY must name an existing executable file; rebuild the sidecar or unset the variable to build current source.", { cause });
    }
    const root = await fs.mkdtemp(path.join(os.tmpdir(), "career-hub-workflows-"));
    return { root, binary };
  }
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "career-hub-workflows-"));
  const binary = path.join(root, process.platform === "win32" ? "sidecar.exe" : "sidecar");
  const go = env.CAREER_HUB_GO_EXECUTABLE || "go";
  try {
    // This disposable test binary is never packaged; VCS stamping is unnecessary.
    await runGo(go, ["build", "-buildvcs=false", "-o", binary, "./cmd/career-hub-sidecar"], {
      cwd: path.join(repo, "backend"), windowsHide: true, timeout: 120_000,
      maxBuffer: 64 * 1024, env: { ...env, CGO_ENABLED: "0", GOTOOLCHAIN: "local" },
    });
  } catch (cause) {
    throw new Error("Workflow setup: cannot build current Go source. Put the pinned Go toolchain on PATH or set CAREER_HUB_GO_EXECUTABLE. " + cause.message, { cause });
  }
  return { root, binary };
}

async function startNative(build, root) {
  const sessionToken = randomBytes(32).toString("base64url");
  const home = path.join(await fs.realpath(root), "home");
  await fs.mkdir(home);
  const child = spawn(build.binary, ["--sidecar-bootstrap"], {
    cwd: repo, stdio: ["pipe", "pipe", "pipe"], windowsHide: true,
  });
  let logs = "";
  child.stderr.on("data", (chunk) => { logs = (logs + chunk).slice(-4096); });
  // Install the exit listener immediately, including startup failure paths.
  const exited = new Promise((resolve) => {
    child.once("error", () => resolve(-1));
    child.once("exit", resolve);
  });
  const stop = () => stopOwnedService(child, exited);
  try {
    const ready = await new Promise((resolve, reject) => {
      let pending = "";
      const timer = setTimeout(() => reject(new Error("native startup timeout: " + logs)), 20_000);
      const fail = (error) => { clearTimeout(timer); reject(error); };
      child.once("error", fail);
      child.once("exit", (code) => fail(new Error(`native startup exited ${code}: ${logs}`)));
      child.stdout.on("data", (chunk) => {
        pending += chunk;
        if (pending.length > 65536) return fail(new Error("native startup output limit"));
        const end = pending.indexOf("\n");
        if (end < 0) return;
        clearTimeout(timer);
        try { resolve(JSON.parse(pending.slice(0, end))); } catch (error) { reject(error); }
      });
      child.stdin.on("error", fail);
      // Keep stdin open: EOF is the service's shutdown contract.
      child.stdin.write(JSON.stringify({ command: "start", protocol_version: 1, port: 0,
        channel: "dev", resource_root: repo, data_root: home, session_token: sessionToken }) + "\n");
    });
    assert.equal(ready.event, "ready", JSON.stringify(ready));
    assert.ok(Number.isInteger(ready.port) && ready.port > 0 && ready.port <= 65535);
    return { origin: `http://127.0.0.1:${ready.port}`, home, stop, sessionToken };
  } catch (error) {
    try { await stop(); } catch (cleanupError) {
      error.message += `; startup cleanup: ${cleanupError.message}`;
    }
    throw error;
  }
}

// Production test runs always create their own Home and context. startService
// is an internal unit-test seam; no CLI option accepts an existing URL or Home.
export async function withWorkflow(t, build, browser, action, {
  expectedHTTPFailures = [], startService = startNative,
} = {}) {
  const root = await fs.mkdtemp(path.join(build.root, "scenario-"));
  const journal = new StepJournal(t.name);
  const step = journal.step.bind(journal);
  const network = [], pageErrors = [], consoleErrors = [], artifactErrors = [];
  const networkEvents = [];
  const began = performance.now();
  let phase = "scenario";
  const recordNetwork = (summary) => {
    if (network.length >= 30) return;
    network.push(summary);
    networkEvents.push({ ...summary, phase, step: journal.steps.at(-1)?.name ?? null,
      elapsed_ms: Math.round(performance.now() - began) });
  };
  let native, context, page, failure, traceStarted = false;
  try {
    native = await step("start isolated native service", () => startService(build, root));
    context = await step("create isolated browser context", () =>
      browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: "block" }));
    await step("configure isolated browser context", async () => {
      // Match the desktop preload contract; never fetch a capability over HTTP.
      await context.addInitScript((token) => {
        window.careerHubDesktop = Object.freeze({ session: Object.freeze({ token: async () => token }) });
      }, native.sessionToken);
      await context.route("**/*", (route) => new URL(route.request().url()).origin === native.origin
        ? route.continue() : route.abort("blockedbyclient"));
      await context.tracing.start({ screenshots: true, snapshots: true, sources: true });
      traceStarted = true;
    });
    page = await context.newPage();
    page.setDefaultTimeout(8000);
    page.on("pageerror", (error) => {
      if (pageErrors.length < 20) pageErrors.push(error.message.slice(0, 500));
    });
    page.on("console", (message) => {
      if (message.type() === "error" && consoleErrors.length < 20) consoleErrors.push(message.text().slice(0, 500));
    });
    page.on("response", (response) => {
      if (response.status() >= 400) recordNetwork(requestSummary(response.request(), response.status()));
    });
    page.on("requestfailed", (request) => {
      recordNetwork({ ...requestSummary(request), error: request.failure()?.errorText });
    });
    await action({ page, step, origin: native.origin, home: native.home });
    await step("no uncaught browser errors", () => assert.deepEqual(pageErrors, []));
    await step("only declared HTTP failures", () => assert.deepEqual(network, expectedHTTPFailures));
    await step("no unexpected console errors", () => {
      const unexpected = consoleErrors.filter((message) => !expectedHTTPFailures.some(({ status }) =>
        message.startsWith("Failed to load resource:") && message.includes(`status of ${status} (`)));
      assert.deepEqual(unexpected, []);
    });
  } catch (error) { failure = error; }
  // Artifact/cleanup failures must not replace the original failing assertion.
  const capture = async (label, action) => {
    try { await step(label, action); } catch (error) {
      artifactErrors.push(`${label}: ${error.message}`);
      failure ||= error;
    }
  };
  phase = "artifacts";
  if (page) await capture("capture screenshot", () => page.screenshot({ path: path.join(root, "screen.png"), timeout: 5000 }));
  phase = "cleanup";
  if (native) {
    try { await step("stop owned native service", native.stop); } catch (error) { failure ||= error; }
  }
  const tracePath = path.join(root, "trace.zip");
  // Keep the trace until every cleanup step succeeds, including context.close.
  if (traceStarted) await capture("retain browser trace", () => context.tracing.stop({ path: tracePath }));
  if (context) await capture("close browser context", () => context.close());
  const writeResult = async () => {
    try {
      await fs.writeFile(path.join(root, "result.json"), JSON.stringify({
        ...journal.report(failure), network, network_events: networkEvents,
        page_errors: pageErrors, console_errors: consoleErrors, artifact_errors: artifactErrors,
      }, null, 2));
    } catch (cause) {
      const error = new Error(`[${t.name}] step "write workflow result" failed: ${cause.message}`, { cause });
      journal.steps.push({ name: "write workflow result", status: "failed" });
      artifactErrors.push(error.message);
      failure ||= error;
      t.diagnostic(error.message);
    }
  };
  t.diagnostic(`Workflow evidence (relative to OS temp): ${path.relative(os.tmpdir(), root)}`);
  await writeResult();
  if (!failure && traceStarted) {
    await capture("discard successful trace", () => fs.unlink(tracePath));
    if (failure) await writeResult();
  }
  if (failure) throw failure;
}
