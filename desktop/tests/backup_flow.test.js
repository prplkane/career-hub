"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const { DesktopBackupFlow } = require("../src/backup_flow");
const { SidecarError } = require("../src/sidecar");
const { backupResult } = require("../src/backup_protocol");

function facts(overrides = {}) {
  return backupResult({ event: "backup_create_result", protocol_version: 1, success: true, error_code: "",
    snapshot_namespace: "created", snapshot_relative_path: `.career_hub_staging/backups/${"a".repeat(32)}`,
    staged_files: 1, last_file_publication: "published", last_file_temporary_cleanup_confirmed: true,
    archive_relative_path: "backups/test.zip", archive_publication: "published",
    archive_temporary_cleanup_confirmed: true, archive_files: 1, full_source_validated: true,
    snapshot_cleanup: { status: "complete", removed_files: 1, removed_directories: 1,
      last_removal: "removed", snapshot_removal: "removed" }, ...overrides });
}

test("host backup rejects renderer paths/options before any side effect", () => {
  let calls = 0;
  const flow = new DesktopBackupFlow({ prepare: () => { calls++; }, createBackup: () => { calls++; } });
  for (const request of [null, [], {}, { confirmWritersStopped: false }, { confirmWritersStopped: "true" },
    { confirmWritersStopped: true, archiveRelativePath: "backups/a.zip" },
    { confirmWritersStopped: true, dataRoot: "C:/private" }, { confirmWritersStopped: true, includeRoots: ["jobs"] }]) {
    assert.throws(() => flow.start(request), /DESKTOP_BACKUP_REQUEST_INVALID/);
  }
  assert.equal(calls, 0);
  assert.equal(flow.getState().phase, "idle");
});

test("host backup synchronously claims operation and keeps immutable result across renderer loss", async () => {
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  let calls = 0;
  const flow = new DesktopBackupFlow({ prepare: () => gate,
    createBackup: async (request) => {
      calls++;
      assert.deepEqual(request, { confirmWritersStopped: true });
      return { backup: { ...facts(), secret: "not projected" }, backendRestarted: true, origin: "not projected" };
    }, onState: () => { throw new Error("window gone"); } });
  assert.deepEqual(flow.start({ confirmWritersStopped: true }), { accepted: true });
  assert.equal(flow.busy, true);
  assert.equal(calls, 0);
  assert.throws(() => flow.start({ confirmWritersStopped: true }), /DESKTOP_BACKUP_BUSY/);
  release();
  const state = await flow.settled;
  assert.equal(calls, 1);
  assert.equal(flow.busy, false);
  assert.equal(state.phase, "complete");
  assert.equal(state.revision, 1);
  assert.equal(Object.isFrozen(state), true);
  assert.equal(Object.isFrozen(state.backup), true);
  assert.doesNotMatch(JSON.stringify(state), /not projected|secret|origin/);
  assert.equal(flow.getState(), state);
});

test("failure before loading view cannot start disk operation", async () => {
  let calls = 0;
  const flow = new DesktopBackupFlow({ prepare: () => { throw new Error("C:/private"); },
    createBackup: () => { calls++; } });
  flow.start({ confirmWritersStopped: true });
  const state = await flow.settled;
  assert.equal(calls, 0);
  assert.equal(state.errorCode, "DESKTOP_BACKUP_VIEW_UNAVAILABLE");
  assert.equal(state.backup.archivePublication, "not_published");
  assert.equal(state.backup.snapshotNamespace, "not_created");
});

test("host state retains known publication even when restart or cleanup fails", async () => {
  for (const backendRestarted of [false, true]) {
    const backup = facts({ success: false, error_code: "BACKUP_SOURCE_CLOSE_FAILED" });
    const flow = new DesktopBackupFlow({ prepare: async () => {}, createBackup: async () => {
      throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { backup, backendRestarted,
        restartErrorCode: backendRestarted ? "" : "DESKTOP_SIDECAR_START_TIMEOUT", secret: "password" });
    } });
    flow.start({ confirmWritersStopped: true });
    const state = await flow.settled;
    assert.equal(state.phase, "error");
    assert.equal(state.backup.archivePublication, "published");
    assert.equal(state.backendRestarted, backendRestarted);
    assert.doesNotMatch(JSON.stringify(state), /password|secret/);
  }
});

test("missing and malformed dispatched outcomes stay unknown without retry", async () => {
  for (const scenario of ["throws", "missing", "malformed", "restartMissing"]) {
    let calls = 0;
    const flow = new DesktopBackupFlow({ prepare: async () => {}, createBackup: async () => {
      calls++;
      if (scenario === "throws") throw new Error("private exception text");
      if (scenario === "missing") return {};
      if (scenario === "malformed") throw new SidecarError("C:/private", { backup: { success: true }, restartErrorCode: "private text" });
      return { backup: facts(), backendRestarted: false };
    } });
    flow.start({ confirmWritersStopped: true });
    const state = await flow.settled;
    assert.equal(calls, 1);
    assert.equal(state.phase, "error");
    assert.equal(state.backup.archivePublication, scenario === "restartMissing" ? "published" : "unknown");
    assert.doesNotMatch(JSON.stringify(state), /private/);
  }
});

test("host preserves incomplete cleanup with a valid archive and never retries it", async () => {
  let calls = 0;
  const backup = facts({ success: false, error_code: "BACKUP_CLEANUP_UNCONFIRMED",
    snapshot_cleanup: { status: "incomplete", removed_files: 0, removed_directories: 0,
      last_removal: "unknown", snapshot_removal: "not_removed" } });
  const flow = new DesktopBackupFlow({ prepare: async () => {}, createBackup: async () => {
    calls++;
    throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { backup, backendRestarted: false });
  } });
  flow.start({ confirmWritersStopped: true });
  const state = await flow.settled;
  assert.equal(calls, 1);
  assert.equal(state.phase, "error");
  assert.equal(state.backup.archivePublication, "published");
  assert.equal(state.backup.snapshotCleanup.status, "incomplete");
  assert.equal(state.backup.snapshotCleanup.lastRemoval, "unknown");
});
