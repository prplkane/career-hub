import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { StepJournal, requestSummary } from "./workflows/journal.mjs";
import { buildFixture, withWorkflow } from "./workflows/fixture.mjs";
import { stopOwnedService } from "./workflows/cleanup.mjs";

test("workflow journal records completed steps and returns their results", async () => {
  const journal = new StepJournal("save profile");
  assert.equal(await journal.step("save", async () => 201), 201);
  assert.deepEqual(journal.report(), {
    scenario: "save profile", result: "PASS", failed_step: null, reason: null,
    steps: [{ name: "save", status: "passed" }],
  });
});

test("workflow failure preserves its cause and identifies the exact step", async () => {
  const journal = new StepJournal("save profile");
  const cause = new Error("expected 200, received 500");
  await journal.step("open form", async () => {});
  let failure;
  await assert.rejects(journal.step("persist change", async () => { throw cause; }), (error) => {
    failure = error;
    return error.cause === cause && error.message.includes('step "persist change" failed');
  });
  assert.equal(journal.report(failure).failed_step, "persist change");
  assert.equal(journal.report(failure).result, "FAIL");
  assert.deepEqual(journal.steps.map((step) => step.status), ["passed", "failed"]);
});

test("synchronous assertion failures also keep the failing workflow step", async () => {
  const journal = new StepJournal("invalid input");
  await assert.rejects(journal.step("reject empty name", () => assert.fail("save was accepted")),
    /reject empty name.*save was accepted/);
});

test("request diagnostics exclude host, query, headers and payload", () => {
  const request = {
    method: () => "PUT", url: () => "http://127.0.0.1:54321/api/vault/profile?token=secret",
    headers: () => { throw new Error("must not read headers"); },
    postData: () => { throw new Error("must not read body"); },
  };
  assert.deepEqual(requestSummary(request, 500), { method: "PUT", path: "/api/vault/profile", status: 500 });
});

test("workflow fixture reuses a supplied binary without invoking Go", async (t) => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "career-hub-build-unit-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const binary = path.join(root, "synthetic-sidecar");
  await fs.writeFile(binary, "synthetic executable", { mode: 0o700 });
  const build = await buildFixture({ env: { CAREER_HUB_SIDECAR_BINARY: binary },
    runGo: () => assert.fail("prebuilt mode must not invoke Go") });
  t.after(() => fs.rm(build.root, { recursive: true, force: true }));
  assert.equal(build.binary, binary);
  assert.notEqual(build.root, root);
  assert.equal(await fs.readFile(binary, "utf8"), "synthetic executable");
});

test("workflow fixture rejects a missing binary or directory without falling back to Go", async (t) => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "career-hub-build-unit-"));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  for (const binary of [path.join(root, "missing"), root]) {
    await assert.rejects(buildFixture({ env: { CAREER_HUB_SIDECAR_BINARY: binary },
      runGo: () => assert.fail("invalid explicit binary must not invoke Go") }),
    /Workflow setup: CAREER_HUB_SIDECAR_BINARY must name an existing executable file/);
  }
});

test("workflow fixture builds current source when no binary is supplied", async (t) => {
  const calls = [];
  const build = await buildFixture({ env: { CAREER_HUB_GO_EXECUTABLE: "synthetic-go" },
    runGo: async (...args) => { calls.push(args); } });
  t.after(() => fs.rm(build.root, { recursive: true, force: true }));
  assert.equal(calls.length, 1);
  const [go, args, options] = calls[0];
  assert.equal(go, "synthetic-go");
  assert.deepEqual(args, ["build", "-buildvcs=false", "-o", build.binary, "./cmd/career-hub-sidecar"]);
  assert.equal(path.basename(options.cwd), "backend");
  assert.equal(options.env.GOTOOLCHAIN, "local");
  assert.equal(options.env.CGO_ENABLED, "0");
});

// Fake lifecycle resources test failure reporting without launching Go or Chromium.
async function fakeWorkflow(t, {
  routeError = null, stopError = null, closeError = null, screenshotError = null, blockResult = false,
  stopAction = () => {},
} = {}) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "career-hub-workflow-unit-"));
  let evidence, closed = 0, stopped = 0;
  const diagnostics = [];
  const handlers = {};
  const page = {
    setDefaultTimeout() {}, on(name, handler) { handlers[name] = handler; },
    screenshot: async ({ path: target }) => {
      if (screenshotError) throw screenshotError;
      await fs.writeFile(target, "synthetic screenshot");
      if (blockResult) await fs.mkdir(path.join(path.dirname(target), "result.json"));
    },
  };
  const context = {
    addInitScript: async () => {},
    route: async () => { if (routeError) throw routeError; },
    tracing: { start: async () => {}, stop: async (options) => {
      if (options.path) await fs.writeFile(options.path, "synthetic trace");
    } },
    newPage: async () => page,
    close: async () => { closed++; if (closeError) throw closeError; },
  };
  return {
    run: (action = async () => {}) => withWorkflow({ name: t.name,
      diagnostic: (message) => {
        diagnostics.push(message);
        if (message.startsWith("Workflow evidence (relative to OS temp): ")) {
          evidence = path.join(os.tmpdir(), message.slice("Workflow evidence (relative to OS temp): ".length));
        }
      },
    }, { root }, { newContext: async () => context }, action, {
      startService: async () => ({ origin: "http://127.0.0.1:12345", home: root,
        stop: async () => { stopped++; stopAction(handlers); if (stopError) throw stopError; } }),
    }),
    report: async () => JSON.parse(await fs.readFile(path.join(evidence, "result.json"), "utf8")),
    trace: async () => fs.readFile(path.join(evidence, "trace.zip"), "utf8"),
    counts: () => ({ closed, stopped }),
    diagnostics,
    handlers,
  };
}

test("network evidence separates scenario failures from cleanup without weakening assertions", async (t) => {
  const request = { method: () => "GET", url: () => "http://127.0.0.1:12345/navigation.js?token=synthetic" };
  const response = { status: () => 500, request: () => request };
  const fixture = await fakeWorkflow(t, { stopAction: (handlers) => handlers.response(response) });
  await assert.rejects(fixture.run(async ({ step }) => {
    await step("reload profile", () => fixture.handlers.response(response));
  }), /only declared HTTP failures/);
  const report = await fixture.report();
  assert.deepEqual(report.network_events.map(({ phase, step }) => ({ phase, step })), [
    { phase: "scenario", step: "reload profile" },
    { phase: "cleanup", step: "stop owned native service" },
  ]);
  assert.ok(report.network_events.every((entry) => entry.elapsed_ms >= 0 && entry.path === "/navigation.js"));
  assert.equal(report.network.length, 2);
  assert.ok(!JSON.stringify(report.network_events).includes("token"));
});

test("partially configured browser contexts are closed when routing fails", async (t) => {
  const fixture = await fakeWorkflow(t, { routeError: new Error("synthetic route failure") });
  await assert.rejects(fixture.run(), /synthetic route failure/);
  assert.deepEqual(fixture.counts(), { closed: 1, stopped: 1 });
});

test("native shutdown failures retain browser trace and exact failed step", async (t) => {
  const fixture = await fakeWorkflow(t, { stopError: new Error("synthetic shutdown failure") });
  await assert.rejects(fixture.run(), /synthetic shutdown failure/);
  const report = await fixture.report();
  assert.equal(report.failed_step, "stop owned native service");
  assert.equal(await fixture.trace(), "synthetic trace");
  assert.deepEqual(fixture.counts(), { closed: 1, stopped: 1 });
});

test("browser close failures retain trace and name the failed cleanup", async (t) => {
  const fixture = await fakeWorkflow(t, { closeError: new Error("synthetic close failure") });
  await assert.rejects(fixture.run(), /synthetic close failure/);
  assert.equal((await fixture.report()).failed_step, "close browser context");
  assert.equal(await fixture.trace(), "synthetic trace");
});

test("artifact and cleanup failures preserve the original assertion and still release resources", async (t) => {
  const fixture = await fakeWorkflow(t, {
    screenshotError: new Error("synthetic screenshot failure"), stopError: new Error("synthetic stop failure"),
  });
  await assert.rejects(fixture.run(async ({ step }) => {
    await step("check saved value", () => assert.fail("synthetic wrong value"));
  }), /check saved value.*synthetic wrong value/);
  const report = await fixture.report();
  assert.equal(report.failed_step, "check saved value");
  assert.match(report.artifact_errors[0], /synthetic screenshot failure/);
  assert.equal(await fixture.trace(), "synthetic trace");
  assert.deepEqual(fixture.counts(), { closed: 1, stopped: 1 });
});

test("successful workflows discard their trace only after cleanup", async (t) => {
  const fixture = await fakeWorkflow(t);
  await fixture.run();
  assert.equal((await fixture.report()).result, "PASS");
  await assert.rejects(fixture.trace(), { code: "ENOENT" });
  assert.deepEqual(fixture.counts(), { closed: 1, stopped: 1 });
});

test("workflow evidence receipt omits the OS temp prefix and still locates results", async (t) => {
  const fixture = await fakeWorkflow(t);
  await fixture.run();
  const receipt = fixture.diagnostics.find((line) => line.startsWith("Workflow evidence (relative to OS temp): "));
  assert.ok(receipt, "receipt must state how to locate its relative path");
  const relative = receipt.slice("Workflow evidence (relative to OS temp): ".length);
  assert.ok(!path.isAbsolute(relative));
  assert.ok(!relative.includes(".."));
  assert.ok(!receipt.includes(os.tmpdir()));
  assert.equal((await fixture.report()).result, "PASS");
});

test("owned service stops through EOF without termination on a clean exit", async () => {
  let ended = false;
  await stopOwnedService({ stdin: { end: () => { ended = true; } },
    kill: () => assert.fail("clean exit must not be terminated"),
  }, Promise.resolve(0));
  assert.equal(ended, true);
});

test("owned service nonzero exit remains a failure", async () => {
  await assert.rejects(stopOwnedService({ stdin: { end() {} } }, Promise.resolve(9)), /native service exit 9/);
});

test("owned service cleanup remains bounded if termination never produces exit", async () => {
  let kills = 0;
  await assert.rejects(stopOwnedService({ stdin: { end() {} }, kill: () => { kills++; return false; } },
    new Promise(() => {}), { graceMs: 1, killMs: 1 }), /did not exit after forced termination/);
  assert.equal(kills, 1);
});

test("a forced service exit is reported even when its exit code is zero", async () => {
  let finish;
  const exited = new Promise((resolve) => { finish = resolve; });
  await assert.rejects(stopOwnedService({ stdin: { end() {} }, kill: () => { finish(0); return true; } },
    exited, { graceMs: 1, killMs: 1 }), /required forced termination/);
});

test("result-file failure preserves the original assertion, evidence path and trace", async (t) => {
  const fixture = await fakeWorkflow(t, { blockResult: true });
  await assert.rejects(fixture.run(async ({ step }) => {
    await step("check saved value", () => assert.fail("synthetic wrong value"));
  }), /check saved value.*synthetic wrong value/);
  assert.equal(await fixture.trace(), "synthetic trace");
  assert.ok(fixture.diagnostics.some((message) => message.includes('step "write workflow result" failed')));
  assert.deepEqual(fixture.counts(), { closed: 1, stopped: 1 });
});

test("result-file failure cannot pass an otherwise successful workflow", async (t) => {
  const fixture = await fakeWorkflow(t, { blockResult: true });
  await assert.rejects(fixture.run(), /write workflow result.*failed/);
  assert.equal(await fixture.trace(), "synthetic trace");
});
