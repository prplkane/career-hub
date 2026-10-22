"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const {
  DataHomeRecoveryFlow,
  RecoveryFlowError,
} = require("../src/recovery_flow");
const { SidecarError } = require("../src/sidecar");
const { absentRestoreResult, publicRestore } = require("../src/restore_protocol");

function restoredFacts() {
  return publicRestore({ ...absentRestoreResult(false, "UNSTARTED", true), success: true, errorCode: "", complete: true,
    restoreId: "a".repeat(32), stagingRelativePath: `.career_hub_staging/restores/${"a".repeat(32)}`,
    recoveryRelativePath: `backups/restore-recovery/restore_${"a".repeat(32)}`, semanticValidated: true,
    roots: { completedMoves: 0, lastMove: "not_moved", recoveryDirectoryMayHaveChanged: false, complete: true },
    finalValidation: { namespace: "created", copiedFiles: 0, lastFilePublication: "not_published", temporaryCleanupConfirmed: true,
      snapshotRelativePath: `.career_hub_staging/backups/${"b".repeat(32)}`, validated: true,
      cleanup: { status: "complete", files: 0, removedFiles: 0, removedDirectories: 1, lastRemoval: "removed", snapshotRemoval: "removed" } },
    receiptPublication: "published", receiptTemporaryCleanupConfirmed: true, journalRemoval: "removed" });
}

test("restore recovery requires its capability and fresh quiescence confirmation, preserving failed facts", async () => {
  for (const mode of ["success", "partial", "legacy", "unknown", "malformed", "wrong-kind", "runtime-changed", "capability-lost", "unsupported"]) {
    const calls = [];
    let root = "C:\\synthetic", capabilities = mode === "unsupported" ? ["email_credential_recovery_v1"] : ["backup_restore_v1"];
    const flow = new DataHomeRecoveryFlow({ channel: "dev", resolveRuntime: () => ({ dataRoot: root,
      sidecarPaths: { deploymentRoot: "C:\\runtime", executablePath: "C:\\runtime\\sidecar.exe", runtimeCapabilities: capabilities } }),
      runMaintenance: async request => {
        calls.push(request);
        if (request.record.command === "data_home_recovery_inspect") return { recoveryKind: "restore" };
        assert.equal(request.record.command, "backup_restore_recover");
        assert.equal(request.record.confirm_writers_stopped, true);
        assert.equal(Object.hasOwn(request.record, "archive_relative_path"), false);
        if (mode === "partial") throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { reportedCode: "BACKUP_SOURCE_CLOSE_FAILED",
          restore: { ...restoredFacts(), success: false, errorCode: "BACKUP_SOURCE_CLOSE_FAILED" }, private: "secret" });
        if (mode === "unknown") throw new Error("private exception");
        if (mode === "legacy") throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { reportedCode: "BACKUP_RESTORE_LEGACY_RECOVERY_REQUIRED",
          restore: absentRestoreResult(false, "BACKUP_RESTORE_LEGACY_RECOVERY_REQUIRED", true) });
        if (mode === "malformed") return {};
        if (mode === "wrong-kind") return absentRestoreResult(false, "WRONG_KIND", false);
        return restoredFacts();
      } });
    if (mode === "unsupported") { await assert.rejects(flow.plan(), { code: "DESKTOP_RECOVERY_RESULT_MISMATCH" }); assert.equal(calls.length, 1); continue; }
    assert.equal((await flow.plan()).recoveryKind, "restore");
    for (const input of [undefined, {}, { confirmWritersStopped: false }, { confirmWritersStopped: true, archiveRelativePath: "backups/a.zip" }]) {
      await assert.rejects(flow.advance(input), { code: "DESKTOP_RESTORE_REQUEST_INVALID" });
    }
    assert.equal(calls.length, 1);
    if (mode === "runtime-changed") root += "-new";
    if (mode === "capability-lost") capabilities = [];
    if (["runtime-changed", "capability-lost"].includes(mode)) {
      await assert.rejects(flow.advance({ confirmWritersStopped: true })); assert.equal(calls.length, 1);
    } else {
      const result = await flow.advance({ confirmWritersStopped: true });
      assert.equal(result.settled, mode === "success");
      assert.equal(calls.length, 2);
      if (!result.settled) {
        assert.equal(result.facts.journalRemoval, mode === "partial" ? "removed" : mode === "legacy" ? "not_removed" : "unknown");
        if (mode === "legacy") assert.equal(result.errorCode, "BACKUP_RESTORE_LEGACY_RECOVERY_REQUIRED");
        assert.equal(Object.isFrozen(result.facts), true);
        assert.doesNotMatch(JSON.stringify(result), /private|secret|runtime/);
      }
    }
    await assert.rejects(flow.advance({ confirmWritersStopped: true }), { code: "DESKTOP_RECOVERY_ADVANCE_NOT_ALLOWED" });
  }
});

test("email recovery classifies before confirmation and consumes failed actions", async () => {
  for (const mode of ["success", "partial", "runtime_changed", "unknown_kind", "new_job"]) {
    const calls = [];
    let dataRoot = "C:\\synthetic-data";
    const facts = Object.freeze({ success: false, cleanupConfirmed: true, intentRemoved: true });
    const flow = new DataHomeRecoveryFlow({ channel: "dev", resolveRuntime: () => ({ dataRoot,
      sidecarPaths: { deploymentRoot: "C:\\runtime", executablePath: "C:\\runtime\\sidecar.exe",
        runtimeCapabilities: ["email_credential_recovery_v1"] } }),
      runMaintenance: async (request) => {
        calls.push(request);
        if (request.record.command === "data_home_recovery_inspect") return { recoveryKind: mode === "unknown_kind" ? "unknown" : mode === "new_job" ? "new_job" : "email_credential" };
        if (request.record.command === "data_home_recovery_plan") return plan(`sha256:${"e".repeat(64)}`, "terminal");
        if (mode === "partial") throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { reportedCode: "EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED", recovery: facts });
        return { success: true, cleanupConfirmed: true, intentRemoved: true };
      } });
    if (mode === "unknown_kind") {
      await assert.rejects(flow.plan(), { code: "DESKTOP_RECOVERY_RESULT_MISMATCH" });
      assert.equal(calls.length, 1);
      continue;
    }
    const reviewed = await flow.plan();
    if (mode === "new_job") {
      assert.equal(reviewed.recoveryKind, "new_job");
      assert.equal(calls.length, 2);
      continue;
    }
    assert.equal(calls.length, 1);
    assert.equal(reviewed.recoveryKind, "email_credential");
    if (mode === "runtime_changed") {
      dataRoot += "-replaced";
      await assert.rejects(flow.advance(), { code: "DESKTOP_RECOVERY_RUNTIME_CHANGED" });
      assert.equal(calls.length, 1);
    } else {
      const result = await flow.advance();
      assert.equal(result.settled, mode === "success");
      if (mode === "partial") assert.deepEqual(result.facts, facts);
      assert.equal(calls[1].record.confirm, true);
      assert.equal(calls[1].record.command, "email_credential_recover");
    }
    await assert.rejects(flow.advance(), { code: "DESKTOP_RECOVERY_ADVANCE_NOT_ALLOWED" });
  }
});

function plan(planId, nextStep) {
  return Object.freeze({
    canAdvance: true,
    disposition: "advance",
    fileState: "complete",
    initialStatus: "applied",
    jobId: "private_job_key",
    ledgerState: "assessed",
    nextStep,
    planId,
    reasonCode: "JOB_INTAKE_RECOVERY_STEP_AVAILABLE",
    receiptState: "missing",
    recoveryKind: "new_job",
  });
}

test("recovery flow retains revisions in main memory and advances one step", async () => {
  const firstId = `sha256:${"a".repeat(64)}`;
  const secondId = `sha256:${"b".repeat(64)}`;
  const calls = [];
  const responses = [
    plan(firstId, "assessment"),
    Object.freeze({ confirmedPlan: firstId, settled: false, step: "assessment" }),
    plan(secondId, "terminal"),
    Object.freeze({ confirmedPlan: secondId, settled: true, step: "terminal" }),
  ];
  const flow = new DataHomeRecoveryFlow({
    channel: "dev",
    resolveRuntime: () => ({
      dataRoot: "C:\\fictional-data",
      sidecarPaths: {
        deploymentRoot: "C:\\fictional-runtime",
        executablePath: "C:\\fictional-runtime\\career-hub-sidecar.exe",
      },
    }),
    runMaintenance: async (request) => {
      calls.push(request);
      return responses.shift();
    },
  });

  const visible = await flow.plan();
  assert.equal(visible.nextStep, "assessment");
  assert.equal("planId" in visible, false);
  assert.equal("jobId" in visible, false);

  const first = await flow.advance();
  assert.deepEqual(first, {
    plan: {
      canAdvance: true,
      disposition: "advance",
      fileState: "complete",
      initialStatus: "applied",
      ledgerState: "assessed",
      nextStep: "terminal",
      reasonCode: "JOB_INTAKE_RECOVERY_STEP_AVAILABLE",
      receiptState: "missing",
      recoveryKind: "new_job",
    },
    settled: false,
    step: "assessment",
  });
  assert.equal(calls[1].record.confirm_plan, firstId);
  assert.equal(calls[1].record.data_root, "C:\\fictional-data");
  assert.equal(calls[1].record.resource_root, "C:\\fictional-runtime");
  assert.equal(calls[2].record.command, "data_home_recovery_plan");

  const terminal = await flow.advance();
  assert.deepEqual(terminal, { settled: true, step: "terminal" });
  assert.equal(calls[3].record.confirm_plan, secondId);
  assert.equal(calls.length, 4);
});

test("recovery flow refuses unreviewed and overlapping advances", async () => {
  let releasePlan;
  const flow = new DataHomeRecoveryFlow({
    channel: "stable",
    resolveRuntime: () => ({
      dataRoot: "C:\\data",
      sidecarPaths: { deploymentRoot: "C:\\runtime", executablePath: "C:\\runtime\\sidecar.exe" },
    }),
    runMaintenance: () => new Promise((resolve) => {
      releasePlan = resolve;
    }),
  });

  await assert.rejects(
    flow.advance(),
    (error) => error instanceof RecoveryFlowError && error.code === "DESKTOP_RECOVERY_ADVANCE_NOT_ALLOWED",
  );
  const pending = flow.plan();
  await assert.rejects(
    flow.plan(),
    (error) => error instanceof RecoveryFlowError && error.code === "DESKTOP_RECOVERY_BUSY",
  );
  releasePlan(plan(`sha256:${"c".repeat(64)}`, "ledger"));
  await pending;
});
