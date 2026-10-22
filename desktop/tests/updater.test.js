"use strict";

const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const test = require("node:test");

const {
  AUTOMATIC_FIRST_CHECK_DELAY_MS,
  AUTOMATIC_RECHECK_DELAY_MS,
  DesktopUpdateError,
  createDesktopUpdater,
} = require("../src/updater");

class FakeAutoUpdater extends EventEmitter {
  constructor() {
    super();
    this.checkCalls = 0;
    this.feed = null;
  }

  setFeedURL(value) {
    this.feed = value;
  }

  checkForUpdates() {
    this.checkCalls += 1;
    return Promise.resolve();
  }
}

function harness({ automaticUpdates = true, configured = true } = {}) {
  const autoUpdater = new FakeAutoUpdater();
  const timers = [];
  const writes = [];
  let installCalls = 0;
  const updater = createDesktopUpdater({
    appVersion: "1.2.3",
    autoUpdater,
    isPackaged: true,
    onInstall: async () => {
      installCalls += 1;
      return true;
    },
    platform: "win32",
    preferenceStore: {
      read: () => ({ automaticUpdates }),
      write: (value) => writes.push(value),
    },
    setTimer: (callback, delay) => {
      const timer = { callback, delay };
      timers.push(timer);
      return timer;
    },
    clearTimer: () => {},
    updateFeedUrl: configured
      ? "https://updates.example.com/dev/win32/x64"
      : null,
  });
  return {
    autoUpdater,
    get installCalls() { return installCalls; },
    timers,
    updater,
    writes,
  };
}

test("packaged Windows updater schedules automatic checks by default", () => {
  const context = harness();
  assert.deepEqual(context.autoUpdater.feed, {
    url: "https://updates.example.com/dev/win32/x64",
  });
  assert.equal(context.updater.getState().automaticUpdates, true);
  context.updater.start();
  assert.equal(context.timers.at(-1).delay, AUTOMATIC_FIRST_CHECK_DELAY_MS);
  context.timers.at(-1).callback();
  assert.equal(context.autoUpdater.checkCalls, 1);
  assert.equal(context.updater.getState().phase, "checking");
});

test("update events expose bounded status and never install silently", async () => {
  const context = harness();
  context.updater.start();
  context.updater.checkForUpdates();
  context.autoUpdater.emit("update-available");
  assert.equal(context.updater.getState().phase, "downloading");
  assert.equal(context.installCalls, 0);

  context.autoUpdater.emit(
    "update-downloaded",
    {},
    "ignored notes",
    "v1.3.0",
  );
  assert.deepEqual(context.updater.getState(), {
    automaticUpdates: true,
    canCheck: false,
    canInstall: true,
    configured: true,
    currentCommit: null,
    currentVersion: "1.2.3",
    errorCode: null,
    phase: "ready",
    progressPercent: 100,
    releaseName: "v1.3.0",
    supported: true,
  });
  assert.equal(context.installCalls, 0);
  assert.deepEqual(await context.updater.installUpdate(), { accepted: true });
  assert.equal(context.installCalls, 1);
});

test("the verified sidecar commit is shown short and malformed values are dropped", () => {
  const context = harness();
  const states = [];
  context.updater.on("state", (state) => states.push(state.currentCommit));
  context.updater.setSourceCommit("0123456789abcdef0123456789abcdef01234567");
  assert.equal(context.updater.getState().currentCommit, "0123456");
  context.updater.setSourceCommit("0123456789abcdef0123456789abcdef01234567");
  for (const value of ["0123456", "0123456789ABCDEF0123456789ABCDEF01234567", null, 42]) {
    context.updater.setSourceCommit(value);
    assert.equal(context.updater.getState().currentCommit, null, String(value));
  }
  assert.deepEqual(states, ["0123456", null]);
});

test("automatic preference is persisted and disables background timers", () => {
  const context = harness();
  context.updater.start();
  context.updater.setAutomaticUpdates(false);
  assert.deepEqual(context.writes, [false]);
  assert.equal(context.updater.getState().automaticUpdates, false);
  context.autoUpdater.emit("update-not-available");
  assert.equal(context.updater.getState().phase, "up_to_date");
  assert.notEqual(context.timers.at(-1)?.delay, AUTOMATIC_RECHECK_DELAY_MS);
});

test("an absent feed is visible but cannot perform network checks", () => {
  const context = harness({ configured: false });
  assert.equal(context.updater.getState().phase, "unavailable");
  assert.equal(context.updater.getState().automaticUpdates, true);
  assert.throws(
    () => context.updater.checkForUpdates(),
    (error) =>
      error instanceof DesktopUpdateError &&
      error.code === "DESKTOP_UPDATE_UNAVAILABLE",
  );
  assert.equal(context.autoUpdater.checkCalls, 0);
});

test("a rejected native feed configuration degrades to unavailable", () => {
  const context = harness();
  context.updater.destroy();
  const autoUpdater = new FakeAutoUpdater();
  autoUpdater.setFeedURL = () => {
    throw new Error("native detail");
  };
  const updater = createDesktopUpdater({
    appVersion: "1.2.3",
    autoUpdater,
    isPackaged: true,
    onInstall: async () => true,
    platform: "win32",
    preferenceStore: {
      read: () => ({ automaticUpdates: true }),
      write: () => {},
    },
    updateFeedUrl: "https://updates.example.com/dev/win32/x64",
  });
  assert.equal(updater.getState().configured, false);
  assert.equal(updater.getState().phase, "unavailable");
  assert.equal(
    updater.getState().errorCode,
    "DESKTOP_UPDATE_CONFIGURATION_FAILED",
  );
});

test("updater errors expose only a stable local code and retry later", () => {
  const context = harness();
  context.updater.start();
  context.updater.checkForUpdates();
  context.autoUpdater.emit("error", new Error("https://secret.example/token"));
  const state = context.updater.getState();
  assert.equal(state.phase, "error");
  assert.equal(state.errorCode, "DESKTOP_UPDATE_CHECK_FAILED");
  assert.equal(context.timers.at(-1).delay, AUTOMATIC_RECHECK_DELAY_MS);
  assert.equal(JSON.stringify(state).includes("secret"), false);
});
