"use strict";

const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const { PassThrough } = require("node:stream");
const test = require("node:test");

const {
  MAX_PROTOCOL_RECORD_BYTES,
  SidecarError,
  SidecarSupervisor,
  minimalChildEnvironment,
  parseMaintenanceLine,
  parseReadinessLine,
} = require("../src/sidecar");

function backupRecord(overrides = {}) {
  return { event: "backup_create_result", protocol_version: 1, success: true, error_code: "",
    snapshot_namespace: "created", snapshot_relative_path: `.career_hub_staging/backups/${"a".repeat(32)}`,
    staged_files: 2, last_file_publication: "published", last_file_temporary_cleanup_confirmed: true,
    archive_relative_path: "backups/created.zip", archive_publication: "published",
    archive_temporary_cleanup_confirmed: true, archive_files: 1, full_source_validated: true,
    snapshot_cleanup: { status: "complete", removed_files: 2, removed_directories: 1,
      last_removal: "removed", snapshot_removal: "removed" }, ...overrides };
}

class FakeChild extends EventEmitter {
  constructor(onRecord) {
    super();
    this.exitCode = null;
    this.signalCode = null;
    this.stdin = new PassThrough();
    this.stdout = new PassThrough();
    this.stderr = new PassThrough();
    // A real child process keeps Node alive while supervisor timers are unref'ed.
    this.keepAlive = setInterval(() => {}, 1_000);
    this.stdin.on("data", (chunk) => {
      for (const line of chunk.toString("utf8").trim().split("\n")) {
        if (line) onRecord?.(JSON.parse(line), this);
      }
    });
  }

  finish(code = 0, signal = null) {
    if (this.exitCode !== null || this.signalCode !== null) return;
    clearInterval(this.keepAlive);
    this.exitCode = code;
    this.signalCode = signal;
    this.emit("exit", code, signal);
  }

  kill() {
    this.finish(null, "SIGTERM");
    return true;
  }
}

class StubbornChild extends FakeChild {
  kill() {
    return false;
  }
}

// Short-deadline fixtures answer in the next microtask after listeners attach.
// A setImmediate response can otherwise lose to an already-due timeout under load.
test("backup maintenance preserves known/unknown facts through child exit and reap failures", async () => {
  for (const mode of ["success", "reported", "wrong_exit", "exit_timeout", "stop_timeout", "no_response", "malformed"]) {
    let child;
    const supervisor = new SidecarSupervisor({ backupTimeoutMs: 30, shutdownTimeoutMs: 10, terminationTimeoutMs: 10,
      spawn() {
        child = new (mode === "stop_timeout" ? StubbornChild : FakeChild)((_record, proc) => {
          queueMicrotask(() => {
            if (mode === "no_response") return;
            proc.stdout.write(mode === "malformed" ? "{}\n" : `${JSON.stringify(backupRecord(mode === "reported"
              ? { success: false, error_code: "BACKUP_SOURCE_CLOSE_FAILED" } : {}))}\n`);
            if (!["exit_timeout", "stop_timeout"].includes(mode)) proc.finish(mode === "reported" || mode === "wrong_exit" ? 4 : 0);
          });
        });
        return child;
      } });
    try {
      const pending = supervisor.runMaintenance({ executablePath: "synthetic", deploymentRoot: "synthetic-runtime",
        expectedEvent: "backup_create_result", record: { command: "backup_create" } });
      if (mode === "success") assert.equal((await pending).success, true);
      else await assert.rejects(pending, (error) => {
        assert.ok(error instanceof SidecarError);
        assert.equal(error.details.backup.archivePublication, ["no_response", "malformed"].includes(mode) ? "unknown" : "published");
        if (mode === "reported") assert.equal(error.details.reportedCode, "BACKUP_SOURCE_CLOSE_FAILED");
        if (mode === "stop_timeout") assert.equal(error.code, "DESKTOP_SIDECAR_STOP_TIMEOUT");
        return true;
      });
      assert.equal(supervisor.ownsChild, mode === "stop_timeout");
    } finally { child?.finish(4); await supervisor.stop(); }
  }
});

test("backup exclusively stops/reaps, creates once, then restarts only the captured Go runtime", async () => {
  for (const mode of ["success", "reported", "missing", "stop-fails", "maintenance-stuck", "restart-fails", "cancel-stop", "cancel-copy", "cancel-restart", "concurrent", "immutable-config"]) {
    const children = [], commands = [], launches = [];
    let stopRequested, copyRequested, restartRequested;
    const stopped = new Promise((resolve) => { stopRequested = resolve; });
    const copying = new Promise((resolve) => { copyRequested = resolve; });
    const restarting = new Promise((resolve) => { restartRequested = resolve; });
    const supervisor = new SidecarSupervisor({ backupTimeoutMs: 30, shutdownTimeoutMs: 10, terminationTimeoutMs: 10,
      fetchImpl: async () => ({ ok: true, status: 200, json: async () => ({ status: "ok", protocol_version: 1, app_version: "0.1.0" }) }),
      spawn(executable, args, options) {
        // At most one live process, including failure/timeout cases.
        assert.equal(children.filter((child) => child.exitCode === null && child.signalCode === null).length, 0);
        launches.push({ executable, args, cwd: options.cwd });
        const number = launches.length;
        const stubborn = (mode === "stop-fails" && number === 1) || (mode === "maintenance-stuck" && number === 2);
        const child = new (stubborn ? StubbornChild : FakeChild)((record, proc) => {
          commands.push(record.command);
          queueMicrotask(() => {
            if (record.command === "start") {
              if (mode === "cancel-restart" && number === 3) { restartRequested(); return; }
              if (mode === "restart-fails" && number === 3) { proc.finish(4); return; }
              proc.stdout.write(`${JSON.stringify(readyRecord())}\n`);
            } else if (record.command === "shutdown") {
              stopRequested();
              if (mode !== "stop-fails" && mode !== "cancel-stop") proc.finish();
            } else if (record.command === "backup_create") {
              copyRequested();
              assert.equal(record.data_root, "/tmp/career-hub-data");
              assert.equal(record.confirm_writers_stopped, true);
              if (["missing", "cancel-copy", "concurrent"].includes(mode)) return;
              proc.stdout.write(`${JSON.stringify(backupRecord(mode === "reported" ? { success: false, error_code: "BACKUP_SOURCE_CLOSE_FAILED" } : {}))}\n`);
              if (mode !== "maintenance-stuck") proc.finish(mode === "reported" ? 4 : 0);
            }
          });
        });
        children.push(child);
        return child;
      } });
    try {
      const config = { ...startConfiguration(), mode: "go-bootstrap", extensionOrigins: [] };
      await supervisor.start(config);
      if (mode === "immutable-config") { config.dataRoot = "/untrusted"; config.extensionOrigins.push("chrome-extension://untrusted"); }
      const pending = supervisor.createBackup({ confirmWritersStopped: true, includeRoots: ["jobs"] });
      // Attach rejection handling before deliberately yielding to cancellation.
      const observed = pending.then((value) => ({ value }), (error) => ({ error }));
      assert.equal(supervisor.backupInProgress, true);
      if (mode === "cancel-stop") { await stopped; await supervisor.stop(); }
      if (mode === "cancel-copy") { await copying; await supervisor.stop(); }
      if (mode === "cancel-restart") { await restarting; await supervisor.stop(); }
      if (mode === "concurrent") {
        await copying;
        await assert.rejects(supervisor.start(config), { code: "DESKTOP_BACKUP_BUSY" });
        await assert.rejects(supervisor.runMaintenance({}), { code: "DESKTOP_BACKUP_BUSY" });
        await assert.rejects(supervisor.createBackup({ confirmWritersStopped: true }), { code: "DESKTOP_BACKUP_BUSY" });
        await assert.rejects(supervisor.restoreBackup({ confirmWritersStopped: true, archiveRelativePath: "backups/a.zip" }), { code: "DESKTOP_BACKUP_BUSY" });
        await assert.rejects(supervisor.recoverRestore({ confirmWritersStopped: true }), { code: "DESKTOP_BACKUP_BUSY" });
        children[1].stdout.write(`${JSON.stringify(backupRecord())}\n`);
        children[1].finish();
      }
      const { value, error } = await observed;
      const success = ["success", "concurrent", "immutable-config"].includes(mode);
      assert.equal(!error, success, mode);
      if (success) { assert.equal(value.backendRestarted, true); assert.equal(value.backup.archivePublication, "published"); }
      else {
        const known = ["reported", "maintenance-stuck", "restart-fails", "cancel-restart"].includes(mode);
        assert.equal(error.details.backup.archivePublication, known ? "published" : ["stop-fails", "cancel-stop"].includes(mode) ? "not_published" : "unknown", mode);
        assert.equal(error.details.backendRestarted, ["reported", "missing"].includes(mode), mode);
        if (mode.startsWith("cancel-")) assert.equal(error.code, "DESKTOP_BACKUP_CANCELLED");
      }
      assert.equal(supervisor.backupInProgress, false);
      const noRestart = ["stop-fails", "maintenance-stuck", "cancel-stop", "cancel-copy"].includes(mode);
      assert.equal(launches.length, mode === "stop-fails" || mode === "cancel-stop" ? 1 : noRestart ? 2 : 3, mode);
      assert.equal(commands.filter((command) => command === "backup_create").length, ["stop-fails", "cancel-stop"].includes(mode) ? 0 : 1, mode);
      if (launches.length === 3) assert.deepEqual(launches[2], launches[0]);
    } finally { for (const child of children) child.finish(4); await supervisor.stop(); }
  }
});

test("invalid/unconfirmed backup never stops an app or dispatches a process", async () => {
  const supervisor = new SidecarSupervisor({ spawn: () => { assert.fail("unexpected spawn"); } });
  for (const options of [{}, { confirmWritersStopped: true, dataRoot: "/different" }, { confirmWritersStopped: true, includeRoots: ["secrets"] }]) {
    await assert.rejects(supervisor.createBackup(options), { code: "DESKTOP_BACKUP_REQUEST_INVALID" });
  }
  await assert.rejects(supervisor.createBackup({ confirmWritersStopped: true }), { code: "DESKTOP_BACKUP_NOT_READY" });
  assert.equal(supervisor.backupInProgress, false);
});

test("backup cannot stop or replace the legacy readonly runtime", async () => {
  const commands = [];
  const child = new FakeChild((record, proc) => {
    commands.push(record.command);
    setImmediate(() => {
      if (record.command === "start") proc.stdout.write(`${JSON.stringify(readyRecord())}\n`);
      if (record.command === "shutdown") proc.finish();
    });
  });
  const supervisor = new SidecarSupervisor({ spawn: () => child,
    fetchImpl: async () => ({ ok: true, status: 200, json: async () => ({ status: "ok", protocol_version: 1, app_version: "0.1.0" }) }) });
  try {
    await supervisor.start(startConfiguration());
    await assert.rejects(supervisor.createBackup({ confirmWritersStopped: true }), { code: "DESKTOP_BACKUP_NOT_READY" });
    assert.deepEqual(commands, ["start"]);
    assert.equal(supervisor.state, "ready");
  } finally { await supervisor.stop(); }
});

function emailRecoveryRecord(overrides = {}) {
  return { event: "email_credential_recovered", protocol_version: 1,
    success: true, error_code: "", database_side: "before", cleanup: "new",
    cleanup_attempted: true, cleanup_confirmed: true, intent_removed: true, ...overrides };
}

test("email recovery accepts only closed facts and consistent success", () => {
  const parse = (record) => parseMaintenanceLine(Buffer.from(JSON.stringify(record)), "email_credential_recovered");
  assert.equal(parse(emailRecoveryRecord()).intentRemoved, true);
  for (const changes of [{ password: "private" }, { database_side: "maybe" }, { cleanup: "arbitrary" },
    { cleanup_attempted: 1 }, { success: true, error_code: "FAILED" }, { intent_removed: false },
    { cleanup_confirmed: false }, { success: false }, { success: false, error_code: "private text" },
    { event: "data_home_recovery_inspected" }, { protocol_version: 2 }]) {
    assert.throws(() => parse(emailRecoveryRecord(changes)), { code: "DESKTOP_SIDECAR_MAINTENANCE_INVALID" });
  }
  const failed = parse(emailRecoveryRecord({ success: false, error_code: "EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED" }));
  assert.equal(failed.intentRemoved, true);
  assert.equal(failed.success, false);
  assert.deepEqual(parseMaintenanceLine(Buffer.from(JSON.stringify({ event: "data_home_recovery_inspected",
    protocol_version: 1, recovery_kind: "email_credential" })), "data_home_recovery_inspected"), { recoveryKind: "email_credential" });
});

test("email maintenance preserves partial facts without promising a clean exit", async () => {
  for (const mode of ["success", "reported", "wrong_exit", "exit_timeout", "stop_timeout", "no_response", "malformed"]) {
    let child;
    const supervisor = new SidecarSupervisor({ maintenanceTimeoutMs: 30, shutdownTimeoutMs: 10, terminationTimeoutMs: 10,
      spawn() {
        child = new (mode === "stop_timeout" ? StubbornChild : FakeChild)((_record, proc) => {
          queueMicrotask(() => {
            if (mode === "no_response") return;
            proc.stdout.write(mode === "malformed" ? "{}\n" : `${JSON.stringify(emailRecoveryRecord(mode === "reported"
              ? { success: false, error_code: "EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED" } : {}))}\n`);
            if (!["exit_timeout", "stop_timeout"].includes(mode)) proc.finish(mode === "reported" || mode === "wrong_exit" ? 4 : 0);
          });
        });
        return child;
      } });
    try {
      const pending = supervisor.runMaintenance({ executablePath: "synthetic-sidecar", deploymentRoot: "synthetic-runtime",
        expectedEvent: "email_credential_recovered", record: { command: "email_credential_recover" } });
      if (mode === "success") assert.equal((await pending).success, true);
      else await assert.rejects(pending, (error) => {
        assert.ok(error instanceof SidecarError);
        if (["reported", "wrong_exit", "exit_timeout", "stop_timeout"].includes(mode)) {
          assert.equal(error.details.recovery.cleanupConfirmed, true);
          assert.equal(error.details.recovery.intentRemoved, true);
        } else assert.equal(error.details?.recovery, undefined);
        if (mode === "reported") assert.equal(error.details.reportedCode, "EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED");
        if (mode === "stop_timeout") assert.equal(error.code, "DESKTOP_SIDECAR_STOP_TIMEOUT");
        return true;
      });
      assert.equal(supervisor.ownsChild, mode === "stop_timeout");
    } finally {
      child?.finish(4);
      await supervisor.stop();
    }
  }
});

function readyRecord(overrides = {}) {
  return {
    app_version: "0.1.0",
    channel: "dev",
    event: "ready",
    port: 41_234,
    protocol_version: 1,
    ...overrides,
  };
}

function startConfiguration() {
  return {
    appVersion: "0.1.0",
    channel: "dev",
    dataRoot: "/tmp/career-hub-data",
    deploymentRoot: "/tmp/career-hub-sidecar",
    executablePath: "/tmp/career-hub-sidecar/career-hub-sidecar",
  };
}

function migrationPlanRecord(overrides = {}) {
  return {
    ciphertext_file_count: 0,
    document_count: 0,
    event: "document_migration_plan",
    plan_id: `sha256:${"a".repeat(64)}`,
    protocol_version: 1,
    source_format_version: 1,
    target_format_version: 2,
    total_ciphertext_bytes: 0,
    ...overrides,
  };
}

function recoveryPlanRecord(overrides = {}) {
  return {
    can_advance: true,
    disposition: "advance",
    event: "data_home_recovery_plan",
    file_state: "complete",
    initial_status: "applied",
    job_id: "fictional_job",
    ledger_state: "assessed",
    next_step: "applied",
    plan_id: `sha256:${"b".repeat(64)}`,
    protocol_version: 1,
    reason_code: "JOB_INTAKE_RECOVERY_STEP_AVAILABLE",
    receipt_state: "missing",
    recovery_kind: "new_job",
    ...overrides,
  };
}

test("minimal child environment drops unrelated secrets and Python injection", () => {
  const environment = minimalChildEnvironment(
    {
      HOME: "/home/test",
      LANG: "en_US.UTF-8",
      NODE_OPTIONS: "--inspect",
      PATH: "/secret/bin",
      PYTHONPATH: "/secret/python",
      SENTINEL_PRIVATE_SECRET: "must-not-cross",
      TMPDIR: "/tmp",
    },
    "darwin",
  );
  assert.deepEqual({ ...environment }, {
    HOME: "/home/test",
    LANG: "en_US.UTF-8",
    TMPDIR: "/tmp",
  });
});

test("readiness validation rejects hostnames, URLs, and extra fields", () => {
  assert.deepEqual(
    parseReadinessLine(Buffer.from(JSON.stringify(readyRecord())), {
      appVersion: "0.1.0",
      channel: "dev",
    }),
    { port: 41_234 },
  );
  assert.throws(
    () =>
      parseReadinessLine(
        Buffer.from(JSON.stringify(readyRecord({ url: "http://evil.invalid" }))),
        { appVersion: "0.1.0", channel: "dev" },
      ),
    (error) =>
      error instanceof SidecarError && error.code === "DESKTOP_SIDECAR_READY_INVALID",
  );
});

test("maintenance response validation is exact and path-free", () => {
  assert.deepEqual(
    parseMaintenanceLine(
      Buffer.from(JSON.stringify(migrationPlanRecord())),
      "document_migration_plan",
    ),
    {
      ciphertextFileCount: 0,
      documentCount: 0,
      planId: `sha256:${"a".repeat(64)}`,
      sourceFormatVersion: 1,
      targetFormatVersion: 2,
      totalCiphertextBytes: 0,
    },
  );
  assert.throws(
    () =>
      parseMaintenanceLine(
        Buffer.from(JSON.stringify(migrationPlanRecord({ data_root: "/private" }))),
        "document_migration_plan",
      ),
    (error) =>
      error instanceof SidecarError &&
      error.code === "DESKTOP_SIDECAR_MAINTENANCE_INVALID",
  );
});

test("recovery maintenance response validation is exact and content-minimized", () => {
  const planId = `sha256:${"b".repeat(64)}`;
  assert.deepEqual(
    parseMaintenanceLine(
      Buffer.from(JSON.stringify(recoveryPlanRecord())),
      "data_home_recovery_plan",
    ),
    {
      canAdvance: true,
      disposition: "advance",
      fileState: "complete",
      initialStatus: "applied",
      jobId: "fictional_job",
      ledgerState: "assessed",
      nextStep: "applied",
      planId,
      reasonCode: "JOB_INTAKE_RECOVERY_STEP_AVAILABLE",
      receiptState: "missing",
      recoveryKind: "new_job",
    },
  );

  const invalidPlans = [
    recoveryPlanRecord({ data_root: "/private" }),
    recoveryPlanRecord({ plan_id: `sha256:${"B".repeat(64)}` }),
    recoveryPlanRecord({ job_id: "CON" }),
    recoveryPlanRecord({ job_id: "bad/job" }),
    recoveryPlanRecord({ disposition: "manual_review" }),
    recoveryPlanRecord({ file_state: "unknown" }),
    recoveryPlanRecord({ can_advance: false }),
  ];
  for (const record of invalidPlans) {
    assert.throws(
      () =>
        parseMaintenanceLine(
          Buffer.from(JSON.stringify(record)),
          "data_home_recovery_plan",
        ),
      (error) =>
        error instanceof SidecarError &&
        error.code === "DESKTOP_SIDECAR_MAINTENANCE_INVALID",
    );
  }

  assert.deepEqual(
    parseMaintenanceLine(
      Buffer.from(
        JSON.stringify({
          confirmed_plan: planId,
          event: "data_home_recovery_advanced",
          protocol_version: 1,
          settled: true,
          step: "terminal",
        }),
      ),
      "data_home_recovery_advanced",
    ),
    { confirmedPlan: planId, settled: true, step: "terminal" },
  );
  for (const record of [
    {
      confirmed_plan: planId,
      event: "data_home_recovery_advanced",
      protocol_version: 1,
      settled: true,
      step: "assessment",
    },
    {
      confirmed_plan: "wrong",
      event: "data_home_recovery_advanced",
      protocol_version: 1,
      settled: false,
      step: "assessment",
    },
    {
      confirmed_plan: planId,
      event: "data_home_recovery_advanced",
      private_path: "/private",
      protocol_version: 1,
      settled: false,
      step: "assessment",
    },
  ]) {
    assert.throws(
      () =>
        parseMaintenanceLine(
          Buffer.from(JSON.stringify(record)),
          "data_home_recovery_advanced",
        ),
      (error) =>
        error instanceof SidecarError &&
        error.code === "DESKTOP_SIDECAR_MAINTENANCE_INVALID",
    );
  }
});

test("one-shot maintenance owns, validates, and reaps its exact child", async () => {
  let spawnCall;
  let request;
  let child;
  const supervisor = new SidecarSupervisor({
    maintenanceTimeoutMs: 100,
    shutdownTimeoutMs: 50,
    spawn(executable, args, options) {
      spawnCall = { args, executable, options };
      child = new FakeChild((record, processHandle) => {
        request = record;
        setImmediate(() => {
          processHandle.stdout.write(`${JSON.stringify(migrationPlanRecord())}\n`);
          processHandle.finish(0);
        });
      });
      return child;
    },
  });
  const record = {
    command: "document_migration_plan",
    data_root: "/tmp/career-hub-data",
    protocol_version: 1,
  };

  const result = await supervisor.runMaintenance({
    deploymentRoot: "/tmp/career-hub-sidecar",
    executablePath: "/tmp/career-hub-sidecar/career-hub-sidecar",
    expectedEvent: "document_migration_plan",
    record,
  });

  assert.deepEqual(result, {
    ciphertextFileCount: 0,
    documentCount: 0,
    planId: `sha256:${"a".repeat(64)}`,
    sourceFormatVersion: 1,
    targetFormatVersion: 2,
    totalCiphertextBytes: 0,
  });
  assert.deepEqual(request, record);
  assert.deepEqual(spawnCall.args, ["--desktop-maintenance"]);
  assert.equal(spawnCall.options.shell, false);
  assert.equal(child.exitCode, 0);
  assert.equal(supervisor.ownsChild, false);
  assert.equal(supervisor.state, "idle");
});

test("maintenance preserves the reported path-free migration error", async () => {
  const supervisor = new SidecarSupervisor({
    maintenanceTimeoutMs: 100,
    shutdownTimeoutMs: 50,
    spawn() {
      return new FakeChild((_record, processHandle) => {
        setImmediate(() => {
          processHandle.stdout.write(
            `${JSON.stringify({ event: "error", error_code: "DOCUMENT_MIGRATION_PLAN_MISMATCH", protocol_version: 1 })}\n`,
          );
          processHandle.finish(4);
        });
      });
    },
  });

  await assert.rejects(
    supervisor.runMaintenance({
      deploymentRoot: "/tmp/career-hub-sidecar",
      executablePath: "/tmp/career-hub-sidecar/career-hub-sidecar",
      expectedEvent: "document_migration_complete",
      record: {
        command: "document_migration_apply",
        confirm_plan: `sha256:${"a".repeat(64)}`,
        data_root: "/tmp/career-hub-data",
        protocol_version: 1,
      },
    }),
    (error) =>
      error instanceof SidecarError &&
      error.code === "DESKTOP_SIDECAR_REPORTED_ERROR" &&
      error.details.reportedCode === "DOCUMENT_MIGRATION_PLAN_MISMATCH",
  );
  assert.equal(supervisor.ownsChild, false);
});

test("supervisor owns one exact child, verifies health, and shuts it down", async () => {
  let spawnCall;
  let startRecord;
  let child;
  const supervisor = new SidecarSupervisor({
    environment: {
      HOME: "/home/test",
      SENTINEL_PRIVATE_SECRET: "must-not-cross",
      TMPDIR: "/tmp",
    },
    fetchImpl: async (url, options) => {
      assert.equal(url, "http://127.0.0.1:41234/api/health");
      assert.equal(options.redirect, "error");
      return {
        ok: true,
        status: 200,
        async json() {
          return { app_version: "0.1.0", protocol_version: 1, status: "ok" };
        },
      };
    },
    platform: "linux",
    shutdownTimeoutMs: 50,
    spawn(executable, args, options) {
      spawnCall = { args, executable, options };
      child = new FakeChild((record, processHandle) => {
        if (record.command === "start") {
          startRecord = record;
          setImmediate(() => {
            processHandle.stdout.write(`${JSON.stringify(readyRecord())}\n`);
          });
        } else if (record.command === "shutdown") {
          setImmediate(() => processHandle.finish(0));
        }
      });
      return child;
    },
    startupTimeoutMs: 100,
  });

  const result = await supervisor.start(startConfiguration());
  assert.deepEqual(result, { origin: "http://127.0.0.1:41234", port: 41_234 });
  assert.equal(supervisor.ownsChild, true);
  assert.deepEqual(spawnCall.args, ["--sidecar"]);
  assert.equal(spawnCall.options.shell, false);
  assert.equal(spawnCall.options.detached, false);
  assert.equal(spawnCall.options.windowsHide, true);
  assert.equal(spawnCall.options.env.SENTINEL_PRIVATE_SECRET, undefined);
  const { session_token: sessionToken, ...startFields } = startRecord;
  assert.deepEqual(startFields, {
    channel: "dev",
    command: "start",
    data_root: "/tmp/career-hub-data",
    port: 0,
    protocol_version: 1,
    resource_root: "/tmp/career-hub-sidecar",
  });
  // ADR SEC-250: 32 random bytes, canonical unpadded URL-safe base64, handed
  // to the owned page while ready and forgotten after stop.
  assert.match(sessionToken, /^[A-Za-z0-9_-]{43}$/);
  assert.equal(Buffer.from(sessionToken, "base64url").length, 32);
  assert.equal(Buffer.from(sessionToken, "base64url").toString("base64url"), sessionToken);
  assert.equal(supervisor.sessionToken, sessionToken);
  await assert.rejects(
    supervisor.start(startConfiguration()),
    (error) =>
      error instanceof SidecarError && error.code === "DESKTOP_SIDECAR_ALREADY_OWNED",
  );
  await supervisor.stop();
  assert.equal(child.exitCode, 0);
  assert.equal(supervisor.ownsChild, false);
  assert.equal(supervisor.sessionToken, null);
});

test("native pairing uses explicit argv and fixed port; wrong readiness never probes another service", async () => {
  for (const actualPort of [8766, 41234]) {
    let child;
    let healthCalls = 0;
    const supervisor = new SidecarSupervisor({
      startupTimeoutMs: 100, shutdownTimeoutMs: 50,
      fetchImpl: async (url) => {
        healthCalls += 1;
        assert.equal(url, "http://127.0.0.1:8766/api/health");
        return { ok: true, status: 200, json: async () => ({ app_version: "0.1.0", protocol_version: 1, status: "ok" }) };
      },
      spawn(executable, args) {
        assert.deepEqual(args, ["--sidecar-bootstrap", "--extension-origin", "chrome-extension://test", "--extension-port", "8766"]);
        child = new FakeChild((record, processHandle) => {
          if (record.command === "start") {
            assert.equal(record.port, 0);
            assert.equal(record.protocol_version, 1);
            setImmediate(() => processHandle.stdout.write(`${JSON.stringify(readyRecord({ port: actualPort }))}\n`));
          } else if (record.command === "shutdown") setImmediate(() => processHandle.finish(0));
        });
        return child;
      },
    });
    const config = { ...startConfiguration(), mode: "go-bootstrap", extensionOrigins: ["chrome-extension://test"] };
    if (actualPort === 8766) {
      assert.equal((await supervisor.start(config)).port, 8766);
      await supervisor.stop();
      assert.equal(healthCalls, 1);
    } else {
      await assert.rejects(supervisor.start(config), { code: "DESKTOP_SIDECAR_READY_INVALID" });
      assert.equal(healthCalls, 0);
    }
    assert.equal(supervisor.ownsChild, false);
    assert.ok(child.exitCode !== null || child.signalCode !== null);
  }
});

test("invalid native pairing fails before a child is spawned", async () => {
  let calls = 0;
  const supervisor = new SidecarSupervisor({ spawn() { calls += 1; } });
  for (const extra of [{ mode: "unknown" }, { extensionOrigins: ["chrome-extension://test"] },
    { mode: "go-bootstrap", extensionOrigins: ["https://example.test"] }]) {
    await assert.rejects(supervisor.start({ ...startConfiguration(), ...extra }), { code: "DESKTOP_SIDECAR_START_INVALID" });
  }
  assert.equal(calls, 0);
  assert.equal(supervisor.state, "idle");
});

test("oversized readiness is bounded and the exact child is reaped", async () => {
  let child;
  const supervisor = new SidecarSupervisor({
    fetchImpl: async () => assert.fail("health must not run"),
    shutdownTimeoutMs: 5,
    spawn() {
      child = new FakeChild((_record, processHandle) => {
        setImmediate(() => {
          processHandle.stdout.write(Buffer.alloc(MAX_PROTOCOL_RECORD_BYTES + 2, 0x61));
        });
      });
      return child;
    },
    startupTimeoutMs: 100,
  });
  await assert.rejects(
    supervisor.start(startConfiguration()),
    (error) =>
      error instanceof SidecarError &&
      error.code === "DESKTOP_SIDECAR_STDOUT_TOO_LARGE",
  );
  assert.equal(child.signalCode, "SIGTERM");
  assert.equal(supervisor.ownsChild, false);
});

test("startup timeout is distinct and leaves no owned child", async () => {
  const supervisor = new SidecarSupervisor({
    fetchImpl: async () => assert.fail("health must not run"),
    shutdownTimeoutMs: 5,
    spawn() {
      return new FakeChild();
    },
    startupTimeoutMs: 10,
  });
  await assert.rejects(
    supervisor.start(startConfiguration()),
    (error) =>
      error instanceof SidecarError &&
      error.code === "DESKTOP_SIDECAR_START_TIMEOUT",
  );
  assert.equal(supervisor.ownsChild, false);
});

test("a confirmed runtime crash releases ownership before one fresh retry", async () => {
  const children = [];
  const exits = [];
  const supervisor = new SidecarSupervisor({
    fetchImpl: async () => ({
      ok: true,
      status: 200,
      async json() {
        return { app_version: "0.1.0", protocol_version: 1, status: "ok" };
      },
    }),
    shutdownTimeoutMs: 50,
    spawn() {
      const child = new FakeChild((record, processHandle) => {
        if (record.command === "start") {
          setImmediate(() => {
            processHandle.stdout.write(`${JSON.stringify(readyRecord())}\n`);
          });
        } else if (record.command === "shutdown") {
          setImmediate(() => processHandle.finish(0));
        }
      });
      children.push(child);
      return child;
    },
    startupTimeoutMs: 100,
  });
  supervisor.on("exit", (details) => exits.push(details));

  await supervisor.start(startConfiguration());
  const firstChild = children[0];
  firstChild.finish(9);
  assert.deepEqual(exits, [{ code: 9, signal: null }]);
  assert.equal(supervisor.ownsChild, false);
  assert.equal(supervisor.state, "idle");
  assert.equal(supervisor.origin, null);

  await supervisor.start(startConfiguration());
  assert.equal(children.length, 2);
  assert.notEqual(children[1], firstChild);
  assert.equal(supervisor.ownsChild, true);

  await supervisor.stop();
  assert.equal(supervisor.ownsChild, false);
  assert.equal(children[1].exitCode, 0);
});

test("stop timeout retains exact ownership until a late confirmed exit", async () => {
  const children = [];
  const supervisor = new SidecarSupervisor({
    fetchImpl: async () => ({
      ok: true,
      status: 200,
      async json() {
        return { app_version: "0.1.0", protocol_version: 1, status: "ok" };
      },
    }),
    shutdownTimeoutMs: 5,
    spawn() {
      const ChildType = children.length === 0 ? StubbornChild : FakeChild;
      const child = new ChildType((record, processHandle) => {
        if (record.command === "start") {
          setImmediate(() => {
            processHandle.stdout.write(`${JSON.stringify(readyRecord())}\n`);
          });
        } else if (record.command === "shutdown" && ChildType === FakeChild) {
          setImmediate(() => processHandle.finish(0));
        }
      });
      children.push(child);
      return child;
    },
    startupTimeoutMs: 100,
    terminationTimeoutMs: 5,
  });

  await supervisor.start(startConfiguration());
  await assert.rejects(
    supervisor.stop(),
    (error) =>
      error instanceof SidecarError &&
      error.code === "DESKTOP_SIDECAR_STOP_TIMEOUT",
  );
  assert.equal(supervisor.ownsChild, true);
  assert.equal(supervisor.state, "stopping");
  await assert.rejects(
    supervisor.start(startConfiguration()),
    (error) =>
      error instanceof SidecarError && error.code === "DESKTOP_SIDECAR_ALREADY_OWNED",
  );
  assert.equal(children.length, 1);

  children[0].finish(0);
  assert.equal(supervisor.ownsChild, false);
  assert.equal(supervisor.state, "idle");

  await supervisor.start(startConfiguration());
  assert.equal(children.length, 2);
  await supervisor.stop();
});

test("startup cleanup timeout retains ownership until pre-ready child exits", async () => {
  let child;
  const supervisor = new SidecarSupervisor({
    fetchImpl: async () => assert.fail("health must not run"),
    shutdownTimeoutMs: 5,
    spawn() {
      child = new StubbornChild();
      return child;
    },
    startupTimeoutMs: 5,
    terminationTimeoutMs: 5,
  });

  await assert.rejects(
    supervisor.start(startConfiguration()),
    (error) =>
      error instanceof SidecarError &&
      error.code === "DESKTOP_SIDECAR_STOP_TIMEOUT",
  );
  assert.equal(supervisor.ownsChild, true);
  assert.equal(supervisor.state, "stopping");

  child.finish(0);
  assert.equal(supervisor.ownsChild, false);
  assert.equal(supervisor.state, "idle");
});

test("runtime protocol violations emit once per owned child", async () => {
  const children = [];
  const protocolErrors = [];
  const supervisor = new SidecarSupervisor({
    fetchImpl: async () => ({
      ok: true,
      status: 200,
      async json() {
        return { app_version: "0.1.0", protocol_version: 1, status: "ok" };
      },
    }),
    shutdownTimeoutMs: 50,
    spawn() {
      const child = new FakeChild((record, processHandle) => {
        if (record.command === "start") {
          setImmediate(() => {
            processHandle.stdout.write(`${JSON.stringify(readyRecord())}\n`);
          });
        } else if (record.command === "shutdown") {
          setImmediate(() => processHandle.finish(0));
        }
      });
      children.push(child);
      return child;
    },
    startupTimeoutMs: 100,
  });
  supervisor.on("protocol-error", (code) => protocolErrors.push(code));

  await supervisor.start(startConfiguration());
  children[0].stdout.write(Buffer.alloc(MAX_PROTOCOL_RECORD_BYTES + 1, 0x61));
  children[0].stdout.write(Buffer.alloc(MAX_PROTOCOL_RECORD_BYTES + 1, 0x62));
  assert.deepEqual(protocolErrors, ["DESKTOP_SIDECAR_RUNTIME_OUTPUT_INVALID"]);
  await supervisor.stop();

  await supervisor.start(startConfiguration());
  children[1].stdout.write(Buffer.alloc(MAX_PROTOCOL_RECORD_BYTES + 1, 0x63));
  assert.deepEqual(protocolErrors, [
    "DESKTOP_SIDECAR_RUNTIME_OUTPUT_INVALID",
    "DESKTOP_SIDECAR_RUNTIME_OUTPUT_INVALID",
  ]);
  await supervisor.stop();
});

test("supervisor remains reusable across repeated start and stop cycles", async () => {
  const children = [];
  const supervisor = new SidecarSupervisor({
    fetchImpl: async () => ({
      ok: true,
      status: 200,
      async json() {
        return { app_version: "0.1.0", protocol_version: 1, status: "ok" };
      },
    }),
    shutdownTimeoutMs: 50,
    spawn() {
      const child = new FakeChild((record, processHandle) => {
        if (record.command === "start") {
          setImmediate(() => {
            processHandle.stdout.write(`${JSON.stringify(readyRecord())}\n`);
          });
        } else if (record.command === "shutdown") {
          setImmediate(() => processHandle.finish(0));
        }
      });
      children.push(child);
      return child;
    },
    startupTimeoutMs: 100,
  });

  for (let cycle = 0; cycle < 50; cycle += 1) {
    await supervisor.start(startConfiguration());
    assert.equal(supervisor.state, "ready");
    assert.equal(supervisor.ownsChild, true);
    await supervisor.stop();
    assert.equal(supervisor.state, "idle");
    assert.equal(supervisor.origin, null);
    assert.equal(supervisor.ownsChild, false);
  }

  assert.equal(children.length, 50);
  assert.equal(children.every((child) => child.exitCode === 0), true);
});
