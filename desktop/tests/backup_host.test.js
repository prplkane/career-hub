"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { EventEmitter } = require("node:events");
const test = require("node:test");
const { createRequire } = require("node:module");
const { SidecarError } = require("../src/sidecar");
const { absentBackupResult, backupResult } = require("../src/backup_protocol");
const { absentRestoreResult, publicRestore } = require("../src/restore_protocol");
const entry = path.resolve(__dirname, "../src/main.js");
const requireHost = createRequire(entry);
const tick = () => new Promise((resolve) => setImmediate(resolve));

function successfulBackup() {
  return backupResult({ event: "backup_create_result", protocol_version: 1, success: true, error_code: "",
    snapshot_namespace: "created", snapshot_relative_path: `.career_hub_staging/backups/${"a".repeat(32)}`,
    staged_files: 0, last_file_publication: "not_published", last_file_temporary_cleanup_confirmed: true,
    archive_relative_path: "backups/test.zip", archive_publication: "published",
    archive_temporary_cleanup_confirmed: true, archive_files: 0, full_source_validated: true,
    snapshot_cleanup: { status: "complete", removed_files: 0, removed_directories: 1,
      last_removal: "removed", snapshot_removal: "removed" } });
}

function successfulRestore() {
  const stage = { namespace: "created", copiedFiles: 0, lastFilePublication: "not_published", temporaryCleanupConfirmed: true };
  const validation = { ...stage, snapshotRelativePath: `.career_hub_staging/backups/${"b".repeat(32)}`, validated: true,
    cleanup: { status: "complete", files: 0, removedFiles: 0, removedDirectories: 1, lastRemoval: "removed", snapshotRemoval: "removed" } };
  return publicRestore({ ...absentRestoreResult(false, "UNSTARTED"), success: true, errorCode: "", complete: true,
    restoreId: "a".repeat(32), stagingRelativePath: `.career_hub_staging/restores/${"a".repeat(32)}`,
    recoveryRelativePath: `backups/restore-recovery/restore_${"a".repeat(32)}`, semanticValidated: true, stagedValidated: true,
    initialValidation: validation, extraction: stage, finalValidation: validation, archiveValidation: validation,
    journal: { namespace: "created", evidenceFiles: 4, lastEvidencePublication: "published", evidenceTemporaryCleanupConfirmed: true,
      publication: "published", temporaryCleanupConfirmed: true },
    roots: { completedMoves: 0, lastMove: "not_moved", recoveryDirectoryMayHaveChanged: false, complete: true },
    receiptPublication: "published", receiptTemporaryCleanupConfirmed: true, journalRemoval: "removed" });
}

async function harness({ capable = true, platform = "win32", restoreRecovery = false, restoreCapable = false, dialogMode = "valid", agent = false, goPreview = true } = {}) {
  const handlers = new Map();
  const app = Object.assign(new EventEmitter(), {
    isPackaged: false, enableSandbox() {}, setName() {}, setAppUserModelId() {}, setPath() {},
    getPath: () => path.resolve("unused-test-path"), requestSingleInstanceLock: () => true,
    whenReady: () => Promise.resolve(), quit() { this.quitCalls = (this.quitCalls || 0) + 1; },
  });
  const windows = [];
  class Window extends EventEmitter {
    constructor() {
      super(); this.url = "about:blank"; this.destroyed = false; this.loads = []; this.messages = [];
      this.webContents = Object.assign(new EventEmitter(), {
        mainFrame: { url: this.url }, setWindowOpenHandler() {}, getURL: () => this.url,
        send: (channel, state) => this.messages.push({ channel, state }),
      });
      windows.push(this);
    }
    async loadURL(url) { this.url = url; this.webContents.mainFrame.url = url; this.loads.push(url); }
    isDestroyed() { return this.destroyed; }
    isMinimized() { return false; }
    show() {} focus() {} setMenu() {}
  }
  let supervisor;
  const dataRoot = path.resolve("synthetic-home");
  let dialogCalls = 0, finishDialog;
  class Supervisor extends EventEmitter {
    constructor() { super(); supervisor = this; this.state = "idle"; this.ownsChild = false; this.starts = 0; this.backups = 0; }
    async start() {
      this.starts++;
      if (restoreRecovery && this.starts === 1) throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { reportedCode: "DATA_HOME_RECOVERY_REQUIRED" });
      this.state = "ready"; this.ownsChild = true; this.origin = "http://127.0.0.1:31001"; return { origin: this.origin };
    }
    async runMaintenance(request) {
      if (request.record.command === "data_home_recovery_inspect") return { recoveryKind: "restore" };
      assert.equal(request.record.command, "backup_restore_recover");
      assert.equal(request.record.confirm_writers_stopped, true);
      this.state = "maintenance"; this.ownsChild = true;
      return new Promise((_resolve, reject) => { this.finishRecovery = () => {
        this.state = "idle"; this.ownsChild = false;
        reject(new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { reportedCode: "BACKUP_RESTORE_UNCONFIRMED",
          restore: absentRestoreResult(true, "BACKUP_RESTORE_UNCONFIRMED", true) }));
      }; });
    }
    createBackup(request) {
      this.backups++; this.request = request; this.state = "maintenance"; this.origin = null;
      return new Promise((resolve, reject) => { this.finish = (success = true) => {
        this.state = "ready"; this.origin = "http://127.0.0.1:31002";
        if (success) resolve({ backup: successfulBackup(), backendRestarted: true, origin: this.origin });
        else reject(new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", {
          backup: absentBackupResult(true, "BACKUP_CREATE_UNCONFIRMED"), backendRestarted: true }));
      }; this.cancel = () => reject(new SidecarError("DESKTOP_BACKUP_CANCELLED")); });
    }
    restoreBackup(request) {
      this.restores = (this.restores || 0) + 1; this.restoreRequest = request;
      this.state = "maintenance"; this.origin = null;
      return new Promise((resolve, reject) => {
        this.finishRestore = (success = true) => {
          this.state = success ? "ready" : "idle"; this.ownsChild = success;
          this.origin = success ? "http://127.0.0.1:31003" : null;
          if (success) resolve({ restore: successfulRestore(), backendRestarted: true, origin: this.origin });
          else reject(new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { restore: absentRestoreResult(true, "BACKUP_RESTORE_UNCONFIRMED"), backendRestarted: false }));
        };
        this.cancel = () => reject(new SidecarError("DESKTOP_RESTORE_CANCELLED", { restore: absentRestoreResult(true, "DESKTOP_RESTORE_CANCELLED") }));
      });
    }
    async stop() { this.ownsChild = false; this.state = "idle"; this.origin = null; this.cancel?.(); }
  }
  let installCalls = 0;
  let updaterOptions;
  const updater = Object.assign(new EventEmitter(), {
    start() {}, destroy() {}, getState: () => ({}), sourceCommits: [],
    setSourceCommit(commit) { this.sourceCommits.push(commit); },
    async installUpdate() { installCalls++; return { accepted: await updaterOptions.onInstall() }; },
  });
  const paths = requireHost("./app_paths");
  const deployment = { runtimeCapabilities: capable ?
    ["backup_create_v1", ...(restoreRecovery ? ["backup_restore_v1", "data_home_recovery_v1"] : restoreCapable ? ["backup_restore_v1"] : [])] : [],
    deploymentRoot: "synthetic", executablePath: "synthetic", sourceCommit: "b".repeat(40),
    ...(agent ? { manifestSha256: "a".repeat(64), agentClient: { executablePath: path.resolve("synthetic/career-hub-agent.exe"),
      contractVersion: 4, commands: ["context"] } } : {}) };
  let ensureCalls = 0, verifyFailure = false, copyFailure = false;
  const copies = [];
  const overrides = {
    electron: { app, BrowserWindow: Window, clipboard: { writeText(text) {
      if (copyFailure) throw new Error("private clipboard error");
      copies.push(text);
    } }, ipcMain: { handle: (key, fn) => handlers.set(key, fn) },
      autoUpdater: { quitAndInstall() {} }, protocol: { registerSchemesAsPrivileged() {}, handle() {} },
      Menu: { setApplicationMenu() {} }, session: { defaultSession: { setPermissionCheckHandler() {}, setPermissionRequestHandler() {} } },
      dialog: { showErrorBox() { throw new Error("Unexpected host error"); }, showOpenDialog(_window, options) {
        dialogCalls++;
        assert.equal(options.defaultPath, path.join(dataRoot, "backups"));
        const result = { canceled: dialogMode === "cancel", filePaths: [path.join(dataRoot, dialogMode === "outside" ? "jobs" : "backups", "input.zip")] };
        return dialogMode === "pending" ? new Promise(resolve => { finishDialog = () => resolve(result); }) : Promise.resolve(result);
      } }, shell: {} },
    "./app_paths": { ...paths, ensurePrivateDirectory: (value) => { ensureCalls++; return value; },
      resolveProductPaths: () => ({ dataRoot, electronUserDataRoot: "synthetic", electronSessionRoot: "synthetic" }),
      resolveSidecarPaths: () => ({}), validateSidecarDeployment: () => {
        if (verifyFailure) throw new Error("private-path-or-credential-must-not-leak");
        return deployment;
      } },
    "./build_metadata": { loadBuildMetadata: () => ({ channel: "dev", appVersion: "0.1.0", sidecarRuntime: "go" }) },
    "./launch_configuration": { readLaunchConfiguration: () => ({ goPreview, extensionOrigins: [] }) },
    "./sidecar": { SidecarError, SidecarSupervisor: Supervisor },
    "./update_preferences": { createUpdatePreferenceStore: () => ({}) },
    "./updater": { createDesktopUpdater: (options) => { updaterOptions = options; return updater; } },
  };
  vm.runInNewContext(`(function () {\n${fs.readFileSync(entry, "utf8")}\n})();`, {
    require: (name) => overrides[name] || requireHost(name), __dirname: path.dirname(entry),
    process: { argv: [], platform, resourcesPath: "synthetic" }, console,
  }, { filename: entry });
  await tick(); await tick();
  assert.equal(windows.length, 1);
  const window = windows[0];
  const event = () => ({ sender: window.webContents, senderFrame: window.webContents.mainFrame });
  const invoke = (name, ...args) => handlers.get(`career-hub:${name}`)(event(), ...args);
  return { app, window, supervisor, invoke, handlers, event, deployment, copies, updater, failCopy: () => { copyFailure = true; }, ensureCount: () => ensureCalls,
    failVerification: () => { verifyFailure = true; }, installCount: () => installCalls, installPolicy: () => updaterOptions.onInstall(),
    dialogCount: () => dialogCalls, finishDialog: () => finishDialog() };
}

test("agent connection is read-only, first-party only and revalidates the startup package", async () => {
  const h = await harness({ agent: true });
  const before = h.ensureCount();
  const result = h.invoke("agent:get-connection");
  assert.equal(result.available, true);
  assert.equal(result.server, "http://127.0.0.1:31001");
  assert.equal(result.executablePath, h.deployment.agentClient.executablePath);
  assert.equal(result.contractVersion, 4);
  assert.equal(h.ensureCount(), before);
  assert.equal(h.supervisor.starts, 1);
  assert.equal(h.copies.length, 0, "reading a connection must not change the clipboard");
  assert.equal(result.connection, path.join(path.resolve("synthetic-home"), "session", "agent-connection.json"));
  assert.deepEqual(Object.keys(result).sort(), ["available", "commands", "connection", "contractVersion", "executablePath", "server"]);
  assert.throws(() => h.invoke("agent:get-connection", { server: "http://evil.test" }), /NOT_ALLOWED/);
  assert.throws(() => h.invoke("agent:copy-connection", { text: "untrusted" }), /NOT_ALLOWED/);
  for (const event of [
    { ...h.event(), senderFrame: { url: h.window.url } },
    { ...h.event(), sender: { mainFrame: h.event().senderFrame } },
  ]) for (const channel of ["agent:get-connection", "agent:copy-connection"]) {
    assert.throws(() => h.handlers.get(`career-hub:${channel}`)(event), /NOT_ALLOWED/);
  }
  assert.equal(h.invoke("agent:copy-connection").copied, true);
  assert.equal(h.copies.length, 1);
  const { available, ...expected } = result;
  assert.equal(h.copies[0], JSON.stringify(expected, null, 2));
  h.failCopy();
  assert.equal(h.invoke("agent:copy-connection").copied, false);
  h.deployment.manifestSha256 = "b".repeat(64);
  assert.equal(h.invoke("agent:get-connection").reason, "package_changed");
  assert.equal(h.invoke("agent:copy-connection").reason, "package_changed");
  assert.equal(h.copies.length, 1);
  h.deployment.manifestSha256 = "a".repeat(64);
  h.failVerification();
  const denied = h.invoke("agent:get-connection");
  assert.equal(denied.reason, "package_changed");
  assert.doesNotMatch(JSON.stringify(denied), /private-path|credential/);
});

test("standard Go exposes verified agent connection; stopped service refuses and maintenance refreshes", async () => {
  const standard = await harness({ goPreview: false, agent: true, restoreCapable: true });
  assert.equal(standard.invoke("agent:get-connection").available, true);
  assert.deepEqual(standard.updater.sourceCommits, ["b".repeat(40)]);
  assert.equal(standard.invoke("restores:get-state").canChoose, true);
  const h = await harness({ agent: true });
  h.supervisor.ownsChild = false;
  assert.equal(h.invoke("agent:get-connection").reason, "service_unavailable");
  h.supervisor.ownsChild = true;
  h.supervisor.origin = "http://127.0.0.1:31999";
  assert.equal(h.invoke("agent:get-connection").reason, "service_unavailable");
  h.supervisor.origin = "http://127.0.0.1:31001";
  h.invoke("backups:start", { confirmWritersStopped: true }); await tick();
  assert.throws(() => h.invoke("agent:get-connection"), /NOT_ALLOWED/);
  h.supervisor.finish(); await tick();
  assert.throws(() => h.invoke("agent:get-connection"), /NOT_ALLOWED/);
  h.invoke("backups:return"); await tick();
  assert.equal(h.invoke("agent:get-connection").server, "http://127.0.0.1:31002");
  assert.equal(h.supervisor.starts, 1);
  h.app.emit("before-quit", { preventDefault() {} }); await tick();
  assert.equal(h.invoke("agent:get-connection").available, false);
});

test("actual host restore uses native selection, consumes confirmation and preserves terminal facts across origins", async () => {
  for (const success of [true, false]) {
    const h = await harness({ restoreCapable: true });
    assert.equal(h.invoke("restores:get-state").canChoose, true);
    const frame = { ...h.event(), senderFrame: { url: h.window.url } };
    assert.throws(() => h.handlers.get("career-hub:restores:choose")(frame), /NOT_ALLOWED/);
    await h.invoke("restores:choose");
    const chosen = h.invoke("restores:get-state");
    assert.equal(chosen.archiveRelativePath, "backups/input.zip");
    assert.equal(chosen.canStart, true);
    assert.throws(() => h.invoke("restores:start", { confirmWritersStopped: true, selectionRevision: chosen.revision, archiveRelativePath: "backups/other.zip" }), /REQUEST_INVALID/);
    assert.equal(h.invoke("restores:start", { confirmWritersStopped: true, selectionRevision: chosen.revision }).accepted, true);
    await tick();
    assert.match(h.window.url, /state=restore/);
    assert.deepEqual(h.supervisor.restoreRequest, { confirmWritersStopped: true, archiveRelativePath: "backups/input.zip" });
    assert.throws(() => h.invoke("restores:start", { confirmWritersStopped: true, selectionRevision: chosen.revision }), /NOT_ALLOWED/);
    await assert.rejects(h.invoke("retry-startup"), /NOT_ALLOWED/);
    assert.equal(h.installPolicy(), false);
    h.app.emit("activate"); await tick(); assert.equal(h.supervisor.starts, 1);
    h.supervisor.finishRestore(success); await tick();
    const state = h.invoke("restores:get-state");
    assert.equal(state.phase, success ? "complete" : "error");
    assert.equal(state.canReturn, true);
    assert.equal(h.supervisor.starts, 1);
    assert.doesNotMatch(JSON.stringify(state), /synthetic|31003/);
    h.invoke("restores:return"); await tick();
    assert.equal(h.window.url, success ? "http://127.0.0.1:31003/" : "http://127.0.0.1:31001/");
    assert.equal(h.supervisor.starts, success ? 1 : 2);
    assert.equal(h.supervisor.restores, 1);
  }
});

test("restore dialog blocks competing actions; cancellation/outside paths/capability refusal never restore", async () => {
  for (const dialogMode of ["pending", "cancel", "outside"]) {
    const h = await harness({ restoreCapable: true, dialogMode });
    const pending = h.invoke("restores:choose");
    if (dialogMode === "pending") {
      assert.throws(() => h.invoke("restores:choose"), /NOT_ALLOWED/);
      assert.throws(() => h.invoke("backups:start", { confirmWritersStopped: true }), /NOT_ALLOWED/);
      await assert.rejects(h.invoke("updates:install"), /NOT_ALLOWED/);
      h.app.emit("activate"); await tick(); assert.equal(h.supervisor.starts, 1);
      h.finishDialog();
    }
    await pending;
    assert.equal(h.dialogCount(), 1);
    assert.equal(h.supervisor.restores, undefined);
    assert.equal(h.invoke("restores:get-state").phase, dialogMode === "pending" ? "ready" : dialogMode === "cancel" ? "idle" : "error");
  }
  for (const options of [{ restoreCapable: false }, { restoreCapable: true, platform: "darwin" }]) {
    const h = await harness(options);
    assert.equal(h.invoke("restores:get-state").available, false);
    assert.throws(() => h.invoke("restores:choose"), /NOT_ALLOWED/);
    assert.equal(h.dialogCount(), 0);
  }
});

test("quit during restore consumes restart authority and activation cannot reopen the app", async () => {
  const h = await harness({ restoreCapable: true });
  await h.invoke("restores:choose");
  h.invoke("restores:start", { confirmWritersStopped: true, selectionRevision: h.invoke("restores:get-state").revision });
  await tick();
  h.app.emit("before-quit", { preventDefault() {} }); await tick(); await tick();
  h.app.emit("activate"); await tick();
  assert.equal(h.supervisor.ownsChild, false);
  assert.equal(h.supervisor.starts, 1);
  assert.equal(h.supervisor.restores, 1);
  assert.equal(h.invoke("restores:get-state").canReturn, false);
});

test("actual host forwards restore quiescence confirmation and excludes retry/update during rollback", async () => {
  const h = await harness({ restoreRecovery: true });
  assert.match(h.window.url, /DATA_HOME_RECOVERY_REQUIRED/);
  assert.equal((await h.invoke("get-data-home-recovery-availability")).available, true);
  assert.equal((await h.invoke("plan-data-home-recovery")).recoveryKind, "restore");
  const pending = h.invoke("advance-data-home-recovery", { confirmWritersStopped: true });
  await tick();
  await assert.rejects(h.invoke("retry-startup"), /NOT_ALLOWED/);
  await assert.rejects(h.invoke("advance-data-home-recovery", { confirmWritersStopped: true }), /NOT_ALLOWED/);
  assert.equal(h.installPolicy(), false);
  h.app.emit("activate"); await tick();
  assert.equal(h.supervisor.starts, 1);
  h.supervisor.finishRecovery();
  const result = await pending;
  assert.equal(result.settled, false);
  assert.equal(result.facts.journalRemoval, "unknown");
  assert.equal(h.supervisor.starts, 1, "failed recovery must not restart");
  assert.doesNotMatch(JSON.stringify(result), /synthetic|31001/);
  await h.invoke("retry-startup"); await tick();
  assert.equal(h.supervisor.starts, 2);
});

test("actual host IPC keeps backup state across origin change and blocks competing controls", async () => {
  const h = await harness();
  assert.equal(h.invoke("backups:get-state").canStart, true);
  const frame = { ...h.event(), senderFrame: { url: h.window.url } };
  assert.throws(() => h.handlers.get("career-hub:backups:start")(frame, { confirmWritersStopped: true }), /NOT_ALLOWED/);
  assert.throws(() => h.invoke("backups:start", { confirmWritersStopped: true, dataRoot: "untrusted" }), /REQUEST_INVALID/);
  assert.equal(h.supervisor.backups, 0);
  assert.equal(h.invoke("backups:start", { confirmWritersStopped: true }).accepted, true);
  await tick();
  assert.match(h.window.url, /state=backup/);
  assert.equal(h.supervisor.backups, 1);
  assert.throws(() => h.invoke("backups:start", { confirmWritersStopped: true }), /NOT_ALLOWED/);
  await assert.rejects(h.invoke("retry-startup"), /NOT_ALLOWED/);
  await assert.rejects(h.invoke("updates:install"), /NOT_ALLOWED/);
  h.app.emit("activate"); await tick();
  assert.equal(h.supervisor.starts, 1);
  h.supervisor.finish(); await tick();
  const state = h.invoke("backups:get-state");
  assert.equal(state.phase, "complete");
  assert.equal(state.canReturn, true);
  assert.equal(state.canStart, false);
  assert.doesNotMatch(JSON.stringify(state), /31002|synthetic/);
  assert.match(h.window.url, /state=backup/);
  h.invoke("backups:return"); await tick();
  assert.equal(h.window.url, "http://127.0.0.1:31002/");
  assert.equal(h.invoke("backups:get-state").phase, "complete");
  assert.equal(h.invoke("backups:get-state").canStart, true);
  assert.equal(h.installCount(), 0);
});

test("old runtimes and non-Windows hosts do not activate backup", async () => {
  for (const options of [{ capable: false }, { platform: "darwin" }]) {
    const h = await harness(options);
    assert.equal(h.invoke("backups:get-state").available, false);
    assert.throws(() => h.invoke("backups:start", { confirmWritersStopped: true }), /NOT_ALLOWED/);
    assert.equal(h.supervisor.backups, 0);
  }
});

test("failed backup can return to restarted service without repeating maintenance", async () => {
  const h = await harness();
  h.invoke("backups:start", { confirmWritersStopped: true }); await tick();
  h.supervisor.finish(false); await tick();
  assert.equal(h.invoke("backups:get-state").backup.archivePublication, "unknown");
  h.invoke("backups:return"); await tick();
  assert.equal(h.window.url, "http://127.0.0.1:31002/");
  assert.equal(h.supervisor.backups, 1);
});

test("quit during backup cancels maintenance and cannot restart through activation", async () => {
  const h = await harness();
  h.invoke("backups:start", { confirmWritersStopped: true }); await tick();
  h.app.emit("before-quit", { preventDefault() {} }); await tick();
  h.app.emit("activate"); await tick();
  assert.equal(h.supervisor.state, "idle");
  assert.equal(h.supervisor.starts, 1);
  assert.equal(h.app.quitCalls, 1);
  assert.equal(h.invoke("backups:get-state").canReturn, false);
});
