"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const {
  FINALIZING,
  RUNNING,
  STOPPING,
  createLifecycleController,
} = require("../src/lifecycle");

function deferred() {
  let reject;
  let resolve;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    reject = rejectPromise;
    resolve = resolvePromise;
  });
  return { promise, reject, resolve };
}

function quitEvent() {
  return {
    prevented: false,
    preventDefault() {
      this.prevented = true;
    },
  };
}

test("Windows window close requests an orderly app quit", () => {
  let quitCalls = 0;
  const lifecycle = createLifecycleController({
    app: { quit: () => quitCalls += 1 },
    ensureWindow: async () => {},
    platform: "win32",
    supervisor: { stop: async () => {} },
  });

  lifecycle.handleWindowAllClosed();
  assert.equal(quitCalls, 1);
  assert.equal(lifecycle.state, RUNNING);
});

test("macOS window close retains the sidecar and activation recreates one window", async () => {
  let quitCalls = 0;
  let ensureCalls = 0;
  const lifecycle = createLifecycleController({
    app: { quit: () => quitCalls += 1 },
    ensureWindow: async () => {
      ensureCalls += 1;
      return { id: ensureCalls };
    },
    platform: "darwin",
    supervisor: { stop: async () => assert.fail("close must not stop the sidecar") },
  });

  lifecycle.handleWindowAllClosed();
  assert.equal(quitCalls, 0);
  assert.deepEqual(await lifecycle.handleActivate(), { id: 1 });
  assert.deepEqual(await lifecycle.handleSecondInstance("--ignored", "unsafe-cwd"), {
    id: 2,
  });
});

test("quit prevents Electron exit until one bounded sidecar stop settles", async () => {
  const stop = deferred();
  let quitCalls = 0;
  let stopCalls = 0;
  const lifecycle = createLifecycleController({
    app: { quit: () => quitCalls += 1 },
    ensureWindow: async () => assert.fail("quitting must not create a window"),
    platform: "win32",
    supervisor: {
      stop() {
        stopCalls += 1;
        return stop.promise;
      },
    },
  });

  const firstEvent = quitEvent();
  const repeatedEvent = quitEvent();
  lifecycle.handleBeforeQuit(firstEvent);
  lifecycle.handleBeforeQuit(repeatedEvent);
  assert.equal(firstEvent.prevented, true);
  assert.equal(repeatedEvent.prevented, true);
  assert.equal(lifecycle.state, STOPPING);
  assert.equal(stopCalls, 0);

  await Promise.resolve();
  assert.equal(stopCalls, 1);
  assert.equal(await lifecycle.handleActivate(), null);
  assert.equal(await lifecycle.handleSecondInstance("--untrusted"), null);

  stop.resolve();
  await lifecycle.shutdownPromise;
  assert.equal(lifecycle.state, FINALIZING);
  assert.equal(quitCalls, 1);

  const finalEvent = quitEvent();
  lifecycle.handleBeforeQuit(finalEvent);
  assert.equal(finalEvent.prevented, false);
  assert.equal(stopCalls, 1);
});

test("a sidecar stop error keeps the owner alive and permits a later safe quit", async () => {
  const errors = [];
  let ensureCalls = 0;
  let quitCalls = 0;
  let stopCalls = 0;
  const lifecycle = createLifecycleController({
    app: { quit: () => quitCalls += 1 },
    ensureWindow: async () => ensureCalls += 1,
    onShutdownError: (error) => errors.push(error.code),
    supervisor: {
      async stop() {
        stopCalls += 1;
        if (stopCalls === 1) {
          const error = new Error("private process detail");
          error.code = "DESKTOP_SIDECAR_STOP_TIMEOUT";
          throw error;
        }
      },
    },
  });

  const event = quitEvent();
  lifecycle.handleBeforeQuit(event);
  await lifecycle.shutdownPromise;
  assert.equal(event.prevented, true);
  assert.deepEqual(errors, ["DESKTOP_SIDECAR_STOP_TIMEOUT"]);
  assert.equal(lifecycle.state, RUNNING);
  assert.equal(lifecycle.shutdownPromise, null);
  assert.equal(quitCalls, 0);
  assert.equal(ensureCalls, 1);

  const retryEvent = quitEvent();
  lifecycle.handleBeforeQuit(retryEvent);
  await lifecycle.shutdownPromise;
  assert.equal(retryEvent.prevented, true);
  assert.equal(lifecycle.state, FINALIZING);
  assert.equal(quitCalls, 1);
});

test("an update install reuses the ordered shutdown with a custom finalizer", async () => {
  let appQuitCalls = 0;
  let installCalls = 0;
  let stopCalls = 0;
  const lifecycle = createLifecycleController({
    app: { quit: () => appQuitCalls += 1 },
    ensureWindow: async () => {},
    platform: "win32",
    supervisor: {
      async stop() {
        stopCalls += 1;
      },
    },
  });

  assert.equal(
    await lifecycle.beginShutdown(() => {
      installCalls += 1;
    }),
    true,
  );
  assert.equal(lifecycle.state, FINALIZING);
  assert.equal(stopCalls, 1);
  assert.equal(installCalls, 1);
  assert.equal(appQuitCalls, 0);
});
