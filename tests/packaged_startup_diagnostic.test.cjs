"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { captureStartupDiagnostic } = require("./helpers/packaged_startup_diagnostic.cjs");

function fixture(t) {
  const outputRoot = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-startup-diagnostic-"));
  t.after(() => fs.rmSync(outputRoot, { recursive: true, force: true }));
  return { outputRoot, child: { exitCode: null, devtoolsURL: Promise.resolve("ws://127.0.0.1:1/synthetic") }, stage: "waiting for owned sidecar" };
}

test("startup failure preserves the typed code and screenshot, without session data", async (t) => {
  const input = fixture(t);
  let closed = false;
  const browser = {
    contexts: () => [{ pages: () => [{
      url: () => "career-hub://app/loading.html?state=error&error_code=DATA_HOME_PATH_REJECTED",
      screenshot: async ({ path: target }) => fs.writeFileSync(target, "synthetic image"),
    }, { url: () => "http://127.0.0.1:8766/?token=synthetic-private-value" }] }],
    close: async () => { closed = true; },
  };
  const result = await captureStartupDiagnostic({ ...input, browserType: { connectOverCDP: async () => browser } });
  assert.equal(result.windows[0].errorCode, "DATA_HOME_PATH_REJECTED");
  assert.equal(result.windows[0].state, "error");
  assert.equal(result.windows[0].screenshot, "startup-failure-0.png");
  assert.equal(closed, true);
  assert.doesNotMatch(fs.readFileSync(path.join(input.outputRoot, "startup-failure.json"), "utf8"), /token|synthetic-private-value/);
});

test("a missing DevTools endpoint is bounded and does not replace the startup failure", async (t) => {
  const input = fixture(t);
  input.child.devtoolsURL = new Promise(() => {});
  const result = await captureStartupDiagnostic({ ...input, timeout: 10,
    browserType: { connectOverCDP: async () => { throw new Error("must not connect"); } } });
  assert.equal(result.stage, input.stage);
  assert.equal(result.inspection_unavailable, true);
});

test("screenshot failure still reports the startup code and disconnects", async (t) => {
  const input = fixture(t);
  let closed = false;
  const result = await captureStartupDiagnostic({ ...input, browserType: { connectOverCDP: async () => ({
    contexts: () => [{ pages: () => [{
      url: () => "career-hub://app/loading.html?state=error&error_code=DESKTOP_SIDECAR_START_FAILED",
      screenshot: async () => { throw new Error("unavailable"); },
    }] }],
    close: async () => { closed = true; },
  }) } });
  assert.equal(result.windows[0].errorCode, "DESKTOP_SIDECAR_START_FAILED");
  assert.equal(result.windows[0].screenshot_unavailable, true);
  assert.equal(closed, true);
});
