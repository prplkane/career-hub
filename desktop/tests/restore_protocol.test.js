"use strict";

const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const { PassThrough } = require("node:stream");
const test = require("node:test");
const { restoreOptions, restoreResult, absentRestoreResult, publicRestore } = require("../src/restore_protocol");
const { SidecarSupervisor, parseMaintenanceLine } = require("../src/sidecar");

function record(recovery = false) {
  const stage = () => ({ namespace: "created", copied_files: 1, last_file_publication: "published", temporary_cleanup_confirmed: true });
  const validation = () => ({ ...stage(), snapshot_relative_path: `.career_hub_staging/backups/${"b".repeat(32)}`, validated: true,
    cleanup: { status: "complete", files: 0, removed_files: 0, removed_directories: 1, last_removal: "removed", snapshot_removal: "removed" } });
  return { event: recovery ? "backup_restore_recovery_result" : "backup_restore_result", protocol_version: 1, success: true, error_code: "",
    restore_id: "a".repeat(32), staging_relative_path: `.career_hub_staging/restores/${"a".repeat(32)}`,
    recovery_relative_path: `backups/restore-recovery/restore_${"a".repeat(32)}`, initial_validation: validation(), extraction: stage(),
    staged_validated: true, archive_validation: validation(), journal: { namespace: "created", evidence_files: 4, last_evidence_publication: "published",
      evidence_temporary_cleanup_confirmed: true, publication: "published", temporary_cleanup_confirmed: true },
    roots: { completed_moves: 2, last_move: "moved", recovery_directory_may_have_changed: false, complete: true }, final_validation: validation(),
    marker_publication: "not_published", semantic_validated: true, receipt_publication: "published", receipt_temporary_cleanup_confirmed: true,
    journal_removal: "removed", complete: true };
}

test("validation cleanup is closed, immutable and separate from restore commit", () => {
  for (const recovery of [false, true]) {
    for (const outcome of ["not_removed", "removed", "unknown"]) {
      const raw = record(recovery);
      raw.success = false; raw.error_code = "BACKUP_CLEANUP_UNCONFIRMED";
      raw.final_validation.cleanup = { status: "incomplete", files: 2, removed_files: outcome === "removed" ? 1 : 0,
        removed_directories: 0, last_removal: outcome, snapshot_removal: "not_removed" };
      const parsed = restoreResult(raw);
      assert.equal(parsed.complete, true);
      assert.equal(parsed.success, false);
      assert.equal(parsed.journalRemoval, "removed");
      assert.equal(parsed.finalValidation.cleanup.lastRemoval, outcome);
      assert.equal(Object.isFrozen(parsed.finalValidation.cleanup), true);
      raw.final_validation.cleanup.removed_files = 2;
      assert.equal(parsed.finalValidation.cleanup.removedFiles, outcome === "removed" ? 1 : 0);
      const exposed = publicRestore({ ...parsed, finalValidation: { ...parsed.finalValidation,
        cleanup: { ...parsed.finalValidation.cleanup, private: "secret" } } });
      assert.deepEqual(exposed, parsed);
      assert.equal(JSON.stringify(exposed).includes("secret"), false);
    }
    const unknown = absentRestoreResult(true, "BACKUP_RESTORE_UNCONFIRMED", recovery);
    assert.equal(unknown.finalValidation.cleanup.status, "unknown");
    const unstarted = absentRestoreResult(false, "BACKUP_RESTORE_UNCONFIRMED", recovery);
    assert.equal(unstarted.finalValidation.cleanup.status, "not_started");
    for (const mutate of [
      r => { delete r.final_validation.cleanup; },
      r => { r.final_validation.cleanup.private = "secret"; },
      r => { r.final_validation.cleanup.files = 25005; },
      r => { r.final_validation.cleanup.removed_files = 1; },
      r => { r.final_validation.cleanup.removed_directories = 50026; },
      r => { r.final_validation.cleanup.snapshot_removal = "unknown"; },
      r => { r.final_validation.cleanup.status = "incomplete"; },
      r => { r.final_validation.cleanup.status = "not_started"; },
      r => { r.complete = false; r.success = false; r.error_code = "BACKUP_RESTORE_UNCONFIRMED"; },
    ]) { const raw = record(recovery); mutate(raw); assert.equal(restoreResult(raw), null); }
    const lateRoot = record(recovery);
    lateRoot.success = false; lateRoot.error_code = "BACKUP_CLEANUP_UNCONFIRMED";
    lateRoot.final_validation.cleanup.status = "incomplete";
    assert.equal(restoreResult(lateRoot).finalValidation.cleanup.snapshotRemoval, "removed");
  }
});

test("restore selection carries only explicit confirmation and one managed archive", () => {
  assert.deepEqual(restoreOptions({ confirmWritersStopped: true, archiveRelativePath: "backups/a.zip" }), { confirm_writers_stopped: true, archive_relative_path: "backups/a.zip" });
  assert.deepEqual(restoreOptions({ confirmWritersStopped: true }, true), { confirm_writers_stopped: true });
  for (const value of [null, {}, { confirmWritersStopped: true }, { confirmWritersStopped: "true", archiveRelativePath: "backups/a.zip" },
    { confirmWritersStopped: true, archiveRelativePath: "C:/private.zip" }, { confirmWritersStopped: true, archiveRelativePath: "backups/../a.zip" },
    { confirmWritersStopped: true, archiveRelativePath: "backups/a.zip", dataRoot: "/other" }]) assert.equal(restoreOptions(value), null);
  assert.equal(restoreOptions({ confirmWritersStopped: true, archiveRelativePath: "backups/a.zip" }, true), null);
});

test("closed nested restore facts preserve late committed errors and reject malformed authority", () => {
  for (const recovery of [false, true]) {
    const raw = record(recovery), parsed = restoreResult(raw);
    assert.equal(parsed.complete, true);
    assert.equal(parsed.recovery, recovery);
    assert.equal(Object.isFrozen(parsed.finalValidation), true);
    raw.final_validation.validated = false;
    assert.equal(parsed.finalValidation.validated, true);
    const late = record(recovery); late.success = false; late.error_code = "BACKUP_SOURCE_CLOSE_FAILED";
    assert.equal(restoreResult(late).complete, true);
    assert.equal(parseMaintenanceLine(Buffer.from(JSON.stringify(late)), late.event).journalRemoval, "removed");
    for (const mutate of [r => { r.private = "secret"; }, r => { r.roots.private = "secret"; }, r => { r.extraction = []; },
      r => { delete r.final_validation.validated; }, r => { r.initial_validation.snapshot_relative_path = "C:/private"; },
      r => { delete r.archive_validation; }, r => { r.archive_validation.snapshot_relative_path = "C:/private"; },
      r => { r.recovery_relative_path += "/.."; }, r => { r.restore_id = "x".repeat(32); }, r => { r.journal.evidence_files = 5; },
      r => { r.roots.completed_moves = 17; }, r => { r.extraction.copied_files = 25001; }, r => { r.extraction.copied_files = 0.5; },
      r => { r.receipt_publication = "unknown"; }, r => { r.final_validation.validated = false; }, r => { r.journal_removal = "not_removed"; },
      r => { r.semantic_validated = "true"; }, r => { r.marker_publication = "unknown"; }, r => { r.success = 1; },
      r => { r.error_code = "private prose"; }, r => { r.receipt_temporary_cleanup_confirmed = false; }]) {
      const invalid = record(recovery); mutate(invalid); assert.equal(restoreResult(invalid), null, JSON.stringify(invalid));
    }
  }
  assert.equal(absentRestoreResult(true, "DESKTOP_SIDECAR_MAINTENANCE_TIMEOUT").journalRemoval, "unknown");
  assert.equal(absentRestoreResult(false, "DESKTOP_RESTORE_NOT_READY").journalRemoval, "not_removed");
  assert.equal(absentRestoreResult(true, "DESKTOP_SIDECAR_STOP_TIMEOUT", true).recovery, true);
});

test("archive validation retains copy facts and requires explicit no-database proof", () => {
  const missing = record(); missing.archive_validation.validated = false;
  assert.equal(restoreResult(missing), null);
  const skipped = record();
  skipped.archive_validation = { namespace: "not_created", copied_files: 0, last_file_publication: "not_published",
    temporary_cleanup_confirmed: true, snapshot_relative_path: "", validated: true,
    cleanup: { status: "not_started", files: 0, removed_files: 0, removed_directories: 0, last_removal: "not_removed", snapshot_removal: "not_removed" } };
  assert.equal(restoreResult(skipped).archiveValidation.validated, true);
  skipped.archive_validation.copied_files = 1;
  assert.equal(restoreResult(skipped), null);
  const partial = record(); partial.success = false; partial.complete = false; partial.error_code = "BACKUP_DATABASE_COPY_FAILED";
  for (const key of ["initial_validation", "archive_validation", "final_validation"]) partial[key].cleanup = { ...skipped.archive_validation.cleanup };
  partial.archive_validation.validated = false;
  const facts = restoreResult(partial).archiveValidation;
  assert.equal(facts.namespace, "created");
  assert.equal(Object.isFrozen(facts), true);
  assert.equal(absentRestoreResult(true, "BACKUP_RESTORE_UNCONFIRMED").archiveValidation.namespace, "unknown");
});

class Child extends EventEmitter {
  constructor(onRecord, stubborn = false) {
    super(); this.exitCode = null; this.signalCode = null; this.stubborn = stubborn;
    this.stdin = new PassThrough(); this.stdout = new PassThrough(); this.stderr = new PassThrough();
    this.keepAlive = setInterval(() => {}, 1000);
    this.stdin.on("data", (chunk) => onRecord(JSON.parse(chunk.toString().trim()), this));
  }
  finish(code = 0) { if (this.exitCode !== null) return; clearInterval(this.keepAlive); this.exitCode = code; this.emit("exit", code, null); }
  kill() { if (this.stubborn) return false; this.finish(1); return true; }
}

const configuration = () => ({ executablePath: "/synthetic/sidecar", deploymentRoot: "/synthetic/resources",
  dataRoot: "/synthetic/home", channel: "dev", appVersion: "1.2.3", mode: "go-bootstrap", extensionOrigins: [] });
const ready = () => ({ event: "ready", protocol_version: 1, port: 41234, channel: "dev", app_version: "1.2.3" });
const health = async () => ({ ok: true, status: 200, json: async () => ({ status: "ok", protocol_version: 1, app_version: "1.2.3" }) });

test("restore lifecycle serializes writers, preserves facts and restarts only after clean confirmed completion", async () => {
  const modes = ["success", "immutable", "concurrent", "reported", "partial", "unknown", "malformed", "wrong_exit",
    "exit_timeout", "stop_timeout", "maintenance_stuck", "spawn_failure", "restart_failure", "cancel_stop", "cancel_restore", "cancel_restart"];
  for (const recovery of [false, true]) for (const mode of modes) {
    if (recovery && ["stop_timeout", "cancel_stop"].includes(mode)) continue;
    const children = [], launches = [], commands = [];
    let onRestore, onStop, onRestart;
    const restoring = new Promise(resolve => { onRestore = resolve; });
    const stopping = new Promise(resolve => { onStop = resolve; });
    const restarting = new Promise(resolve => { onRestart = resolve; });
    const supervisor = new SidecarSupervisor({ fetchImpl: health, restoreTimeoutMs: 40, startupTimeoutMs: 40,
      shutdownTimeoutMs: 10, terminationTimeoutMs: 10,
      spawn(executable, args, options) {
        assert.equal(children.filter(c => c.exitCode === null).length, 0, "writer overlap");
        launches.push({ executable, args, cwd: options.cwd });
        const number = launches.length;
        if (mode === "spawn_failure" && number === 2) throw new Error("private path");
        const stubborn = mode === "stop_timeout" && number === 1 || mode === "maintenance_stuck" && number === 2;
        const child = new Child((request, proc) => setImmediate(() => {
          commands.push(request);
          if (request.command === "start") {
            if (recovery && number === 1) {
              proc.stdout.write(`${JSON.stringify({ event: "error", protocol_version: 1, error_code: "DATA_HOME_RECOVERY_REQUIRED" })}\n`);
              proc.finish(4); return;
            }
            if (number === 3 && mode === "cancel_restart") { onRestart(); return; }
            if (number === 3 && mode === "restart_failure") { proc.finish(4); return; }
            proc.stdout.write(`${JSON.stringify(ready())}\n`);
          } else if (request.command === "shutdown") {
            onStop();
            if (!["stop_timeout", "cancel_stop"].includes(mode)) proc.finish();
          } else {
            assert.equal(request.command, recovery ? "backup_restore_recover" : "backup_restore");
            assert.equal(request.data_root, "/synthetic/home");
            assert.equal(request.resource_root, "/synthetic/resources");
            assert.equal(request.confirm_writers_stopped, true);
            assert.equal(request.archive_relative_path, recovery ? undefined : "backups/input.zip");
            onRestore();
            if (["unknown", "cancel_restore", "concurrent"].includes(mode)) return;
            const output = record(recovery);
            if (["reported", "partial"].includes(mode)) { output.success = false; output.error_code = "BACKUP_SOURCE_CLOSE_FAILED"; }
            if (mode === "partial") { output.complete = false; output.journal_removal = "not_removed";
              for (const key of ["initial_validation", "archive_validation", "final_validation"]) output[key].cleanup =
                { status: "not_started", files: 0, removed_files: 0, removed_directories: 0, last_removal: "not_removed", snapshot_removal: "not_removed" };
            }
            proc.stdout.write(mode === "malformed" ? "{}\n" : `${JSON.stringify(output)}\n`);
            if (!["exit_timeout", "maintenance_stuck"].includes(mode)) proc.finish(["reported", "partial", "wrong_exit"].includes(mode) ? 4 : 0);
          }
        }), stubborn);
        children.push(child); return child;
      } });
    try {
      const config = configuration();
      if (recovery) {
        await assert.rejects(supervisor.start(config), { code: "DESKTOP_SIDECAR_REPORTED_ERROR" });
        assert.equal(supervisor.canAttemptRestoreRecovery, true);
      } else await supervisor.start(config);
      if (mode === "immutable") { config.dataRoot = "/untrusted"; config.extensionOrigins.push("chrome-extension://untrusted"); }
      const options = { confirmWritersStopped: true, ...(!recovery && { archiveRelativePath: "backups/input.zip" }) };
      const pending = (recovery ? supervisor.recoverRestore(options) : supervisor.restoreBackup(options));
      const observed = pending.then(value => ({ value }), error => ({ error }));
      assert.equal(supervisor.restoreInProgress, true);
      assert.equal(supervisor.maintenanceInProgress, true);
      assert.equal(supervisor.canAttemptRestoreRecovery, false);
      if (mode === "cancel_stop") { await stopping; await supervisor.stop(); }
      if (mode === "cancel_restore") { await restoring; await supervisor.stop(); }
      if (mode === "cancel_restart") { await restarting; await supervisor.stop(); }
      if (mode === "concurrent") {
        await restoring;
        for (const action of [() => supervisor.start(config), () => supervisor.runMaintenance({}),
          () => supervisor.createBackup({ confirmWritersStopped: true }), () => supervisor.restoreBackup(options),
          () => supervisor.recoverRestore({ confirmWritersStopped: true })]) {
          await assert.rejects(action(), { code: "DESKTOP_RESTORE_BUSY" });
        }
        children[1].stdout.write(`${JSON.stringify(record(recovery))}\n`); children[1].finish();
      }
      const { value, error } = await observed;
      const success = ["success", "immutable", "concurrent"].includes(mode);
      assert.equal(!error, success, `${recovery}/${mode}`);
      if (success) {
        assert.equal(value.restore.complete, true);
        assert.equal(value.backendRestarted, true);
        assert.equal(supervisor.canAttemptRestoreRecovery, false);
      } else {
        assert.equal(error.details.backendRestarted, false, `${mode} must not restart`);
        const known = ["reported", "partial", "wrong_exit", "exit_timeout", "maintenance_stuck", "restart_failure", "cancel_restart"].includes(mode);
        const notDispatched = ["stop_timeout", "cancel_stop"].includes(mode);
        assert.equal(error.details.restore.journalRemoval, mode === "partial" || notDispatched ? "not_removed" : known ? "removed" : "unknown", mode);
        assert.equal(error.details.childStillOwned, ["stop_timeout", "maintenance_stuck"].includes(mode), mode);
        if (mode.startsWith("cancel_")) assert.equal(error.code, "DESKTOP_RESTORE_CANCELLED");
      }
      assert.equal(supervisor.restoreInProgress, false);
      assert.equal(supervisor.maintenanceInProgress, false);
      const restarted = success || ["restart_failure", "cancel_restart"].includes(mode);
      assert.equal(launches.length, ["stop_timeout", "cancel_stop"].includes(mode) ? 1 : restarted ? 3 : 2, mode);
      if (restarted) assert.deepEqual(launches[2], launches[0]);
      assert.equal(commands.filter(c => c.command === "backup_restore" || c.command === "backup_restore_recover").length,
        ["stop_timeout", "cancel_stop", "spawn_failure"].includes(mode) ? 0 : 1, mode);
    } finally { for (const child of children) child.finish(4); await supervisor.stop(); }
  }
});

test("restore rejects invalid confirmation, runtime authority and legacy runtime without stopping a writer", async () => {
  let dispatched = 0;
  const supervisor = new SidecarSupervisor({ spawn() { dispatched++; throw new Error("must not spawn"); } });
  for (const options of [{}, { confirmWritersStopped: false, archiveRelativePath: "backups/a.zip" },
    { confirmWritersStopped: true, archiveRelativePath: "backups/a.zip", dataRoot: "/other" }]) {
    await assert.rejects(supervisor.restoreBackup(options), { code: "DESKTOP_RESTORE_REQUEST_INVALID" });
  }
  await assert.rejects(supervisor.recoverRestore({ confirmWritersStopped: true, archiveRelativePath: "backups/a.zip" }), { code: "DESKTOP_RESTORE_REQUEST_INVALID" });
  await assert.rejects(supervisor.restoreBackup({ confirmWritersStopped: true, archiveRelativePath: "backups/a.zip" }), { code: "DESKTOP_RESTORE_NOT_READY" });
  await assert.rejects(supervisor.recoverRestore({ confirmWritersStopped: true }), { code: "DESKTOP_RESTORE_RECOVERY_NOT_READY" });
  assert.equal(dispatched, 0);

  const child = new Child((request, proc) => setImmediate(() => {
    if (request.command === "start") proc.stdout.write(`${JSON.stringify(ready())}\n`);
    else if (request.command === "shutdown") proc.finish();
  }));
  const legacy = new SidecarSupervisor({ spawn: () => child, fetchImpl: health });
  try {
    await legacy.start({ ...configuration(), mode: "readonly" });
    await assert.rejects(legacy.restoreBackup({ confirmWritersStopped: true, archiveRelativePath: "backups/a.zip" }), { code: "DESKTOP_RESTORE_NOT_READY" });
    assert.equal(legacy.state, "ready");
  } finally { await legacy.stop(); }
});

test("a new admitted start supersedes recovery authority and unrelated errors never authorize restore", async () => {
  let mode = "DATA_HOME_RECOVERY_REQUIRED", launches = 0;
  const supervisor = new SidecarSupervisor({ fetchImpl: health, spawn() {
    launches++;
    return new Child((_request, proc) => setImmediate(() => {
      proc.stdout.write(`${JSON.stringify({ event: "error", protocol_version: 1, error_code: mode })}\n`); proc.finish(4);
    }));
  } });
  await assert.rejects(supervisor.start(configuration()));
  assert.equal(supervisor.canAttemptRestoreRecovery, true);
  mode = "DATA_HOME_UNAVAILABLE";
  await assert.rejects(supervisor.start({ ...configuration(), dataRoot: "/synthetic/other-home" }));
  assert.equal(supervisor.canAttemptRestoreRecovery, false);
  await assert.rejects(supervisor.recoverRestore({ confirmWritersStopped: true }), { code: "DESKTOP_RESTORE_RECOVERY_NOT_READY" });
  assert.equal(launches, 2);
});

test("restore maintenance retains validated partial facts through timeout, error and reap failures", async () => {
  for (const recovery of [false, true]) {
    for (const mode of ["success", "reported", "wrong_exit", "exit_timeout", "stop_timeout", "no_response", "malformed"]) {
      let child;
      const supervisor = new SidecarSupervisor({ restoreTimeoutMs: 30, shutdownTimeoutMs: 10, terminationTimeoutMs: 10,
        spawn() { child = new Child((_request, proc) => setImmediate(() => {
          if (mode === "no_response") return;
          const result = record(recovery);
          if (mode === "reported") { result.success = false; result.error_code = "BACKUP_SOURCE_CLOSE_FAILED"; }
          proc.stdout.write(mode === "malformed" ? "{}\n" : `${JSON.stringify(result)}\n`);
          if (!["exit_timeout", "stop_timeout"].includes(mode)) proc.finish(mode === "reported" || mode === "wrong_exit" ? 4 : 0);
        }), mode === "stop_timeout"); return child; } });
      try {
        const pending = supervisor.runMaintenance({ executablePath: "synthetic", deploymentRoot: "synthetic", expectedEvent: record(recovery).event,
          record: { command: recovery ? "backup_restore_recover" : "backup_restore" } });
        if (mode === "success") { assert.equal((await pending).complete, true); continue; }
        await assert.rejects(pending, (error) => {
          assert.equal(error.details.restore.recovery, recovery);
          assert.equal(error.details.restore.journalRemoval, ["no_response", "malformed"].includes(mode) ? "unknown" : "removed");
          assert.equal(supervisor.ownsChild, mode === "stop_timeout");
          return true;
        });
      } finally { child?.finish(1); }
    }
  }
});
