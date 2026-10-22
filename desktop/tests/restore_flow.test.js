"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");
const { DesktopRestoreFlow, managedArchiveSelection } = require("../src/restore_flow");
const { SidecarError } = require("../src/sidecar");
const { absentRestoreResult, publicRestore } = require("../src/restore_protocol");

function facts() {
  const v = absentRestoreResult(false, "UNSTARTED");
  const stage = { namespace: "created", copiedFiles: 0, lastFilePublication: "not_published", temporaryCleanupConfirmed: true };
  const validation = { ...stage, snapshotRelativePath: `.career_hub_staging/backups/${"b".repeat(32)}`, validated: true,
    cleanup: { status: "complete", files: 0, removedFiles: 0, removedDirectories: 1, lastRemoval: "removed", snapshotRemoval: "removed" } };
  return publicRestore({ ...v, success: true, errorCode: "", restoreId: "a".repeat(32),
    stagingRelativePath: `.career_hub_staging/restores/${"a".repeat(32)}`, recoveryRelativePath: `backups/restore-recovery/restore_${"a".repeat(32)}`,
    initialValidation: validation, finalValidation: validation, extraction: stage, stagedValidated: true, archiveValidation: validation,
    journal: { namespace: "created", evidenceFiles: 4, lastEvidencePublication: "published", evidenceTemporaryCleanupConfirmed: true,
      publication: "published", temporaryCleanupConfirmed: true },
    roots: { completedMoves: 0, lastMove: "not_moved", recoveryDirectoryMayHaveChanged: false, complete: true },
    semanticValidated: true, receiptPublication: "published", receiptTemporaryCleanupConfirmed: true, journalRemoval: "removed", complete: true });
}

const request = flow => ({ confirmWritersStopped: true, selectionRevision: flow.getState().revision });
const make = overrides => new DesktopRestoreFlow({ chooseArchive: async () => "backups/input.zip", runtimeIdentity: () => "private-runtime-identity",
  prepare: async () => {}, restoreBackup: async () => ({ restore: facts(), backendRestarted: true }), ...overrides });

test("native selection stays within the current Home backups namespace", () => {
  assert.equal(managedArchiveSelection("C:\\private\\home", "C:\\private\\home\\backups\\nested\\a.zip", path.win32), "backups/nested/a.zip");
  assert.equal(managedArchiveSelection("/private/home", "/private/home/backups/a.zip", path.posix), "backups/a.zip");
  for (const file of ["C:\\private\\home2\\backups\\a.zip", "C:\\private\\home\\jobs\\a.zip", "D:\\backups\\a.zip",
    "C:\\private\\home\\backups\\..\\a.zip", "C:\\private\\home\\backups\\a.zip:ads", "C:\\private\\home\\backups\\a.txt",
    "backups\\a.zip", "C:\\private\\home\\backups\\a.zip\0", "\\\\?\\C:\\private\\home\\backups\\a.zip"]) {
    assert.equal(managedArchiveSelection("C:\\private\\home", file, path.win32), null, file);
  }
  assert.equal(managedArchiveSelection("relative", "/private/home/backups/a.zip", path.posix), null);
});

test("host consumes one selected archive and rejects renderer paths and stale confirmations", async () => {
  let calls = 0;
  const flow = make({ restoreBackup: async (options) => {
    calls++; assert.deepEqual(options, { confirmWritersStopped: true, archiveRelativePath: "backups/input.zip" });
    return { restore: { ...facts(), secret: "private" }, backendRestarted: true, origin: "private" };
  }, onState: () => { throw new Error("renderer gone"); } });
  assert.throws(() => flow.start(request(flow)), /REQUEST_INVALID/);
  await flow.choose();
  const selected = request(flow);
  for (const invalid of [null, {}, { ...selected, archiveRelativePath: "backups/other.zip" }, { ...selected, confirmWritersStopped: false },
    { ...selected, dataRoot: "C:/private" }, { ...selected, selectionRevision: 0 }]) assert.throws(() => flow.start(invalid), /REQUEST_INVALID/);
  assert.equal(calls, 0);
  assert.deepEqual(flow.start(selected), { accepted: true });
  assert.throws(() => flow.start(selected), /BUSY/);
  await assert.rejects(flow.choose(), /BUSY/);
  const state = await flow.settled;
  assert.equal(calls, 1);
  assert.equal(state.phase, "complete");
  assert.equal(state.restore.journalRemoval, "removed");
  assert.equal(Object.isFrozen(state.restore.finalValidation), true);
  assert.doesNotMatch(JSON.stringify(state), /private|secret|origin/);
  assert.throws(() => flow.start(selected), /REQUEST_INVALID/);
});

test("cancelled/replaced dialog invalidates prior selection and serializes further requests", async () => {
  let release, result = "backups/a.zip";
  const flow = make({ chooseArchive: () => result === "pending" ? new Promise(resolve => { release = resolve; }) : result });
  await flow.choose(); const old = request(flow);
  result = "pending"; const pending = flow.choose();
  assert.equal(flow.busy, true);
  assert.throws(() => flow.start(old), /BUSY/);
  await assert.rejects(flow.choose(), /BUSY/);
  release(null); await pending;
  assert.equal(flow.getState().phase, "idle");
  assert.equal(flow.getState().archiveRelativePath, "");
  assert.throws(() => flow.start(old), /REQUEST_INVALID/);
  result = "backups/b.zip"; await flow.choose();
  assert.throws(() => flow.start(old), /REQUEST_INVALID/);
});

test("runtime drift around dialog and navigation cannot dispatch a restore", async () => {
  for (const when of ["dialog", "before", "prepare"]) {
    let identity = "private-a", calls = 0;
    const flow = make({ runtimeIdentity: () => identity,
      chooseArchive: async () => { if (when === "dialog") identity = "private-b"; return "backups/a.zip"; },
      prepare: async () => { if (when === "prepare") identity = "private-b"; },
      restoreBackup: () => { calls++; } });
    await flow.choose();
    if (when !== "dialog") {
      if (when === "before") identity = "private-b";
      flow.start(request(flow)); await flow.settled;
      assert.equal(flow.getState().restore.journalRemoval, "not_removed");
    }
    assert.equal(flow.getState().errorCode, "DESKTOP_RESTORE_RUNTIME_CHANGED");
    assert.equal(calls, 0);
    assert.doesNotMatch(JSON.stringify(flow.getState()), /private/);
  }
});

test("restore failures preserve known facts but never manufacture completion or expose private errors", async () => {
  for (const mode of ["prepare", "unknown", "malformed", "late", "restart", "wrong-operation", "outside", "dialog-error"]) {
    let calls = 0;
    const flow = make({
      chooseArchive: async () => { if (mode === "dialog-error") throw new Error("private path"); return mode === "outside" ? "../private/a.zip" : "backups/a.zip"; },
      prepare: async () => { if (mode === "prepare") throw new Error("private path"); },
      restoreBackup: async () => {
        calls++;
        if (mode === "unknown") throw new Error("private exception");
        if (mode === "malformed") throw new SidecarError("private path", { restore: { success: true }, restartErrorCode: "private" });
        if (mode === "late") throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { restore: { ...facts(), success: false, errorCode: "BACKUP_SOURCE_CLOSE_FAILED" }, secret: "private" });
        return { restore: mode === "wrong-operation" ? { ...facts(), recovery: true } : facts(), backendRestarted: false };
      } });
    await flow.choose();
    if (!["outside", "dialog-error"].includes(mode)) { flow.start(request(flow)); await flow.settled; }
    const state = flow.getState();
    assert.equal(state.phase, "error", mode);
    assert.equal(state.backendRestarted, false);
    assert.equal(calls, ["prepare", "outside", "dialog-error"].includes(mode) ? 0 : 1);
    if (state.restore) assert.equal(state.restore.journalRemoval,
      ["late", "restart"].includes(mode) ? "removed" : mode === "prepare" ? "not_removed" : "unknown", mode);
    assert.doesNotMatch(JSON.stringify(state), /private|secret/);
  }
});
