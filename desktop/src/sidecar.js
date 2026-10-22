"use strict";

const { EventEmitter } = require("node:events");
const childProcess = require("node:child_process");
const crypto = require("node:crypto");
const { sidecarLaunchOptions } = require("./launch_configuration");
const { BACKUP_RESULT_FIELDS, backupOptions, backupResult, absentBackupResult } = require("./backup_protocol");
const { RESTORE_RESULT_FIELDS, isRestoreEvent, restoreOptions, restoreResult, absentRestoreResult } = require("./restore_protocol");

const PROTOCOL_VERSION = 1;
const MAX_PROTOCOL_RECORD_BYTES = 8 * 1024;
const DEFAULT_STARTUP_TIMEOUT_MS = 15_000;
const DEFAULT_HEALTH_TIMEOUT_MS = 3_000;
const DEFAULT_SHUTDOWN_TIMEOUT_MS = 3_000;
const DEFAULT_TERMINATION_TIMEOUT_MS = 1_000;
const DEFAULT_MAINTENANCE_TIMEOUT_MS = 5 * 60_000;
// Give the native five-minute cooperative deadline time to emit its facts.
const DEFAULT_BACKUP_TIMEOUT_MS = 5 * 60_000 + 15_000;
const DEFAULT_RESTORE_TIMEOUT_MS = 10 * 60_000 + 15_000;
const PLAN_ID_PATTERN = /^sha256:[0-9a-f]{64}$/;
const RECOVERY_JOB_ID_PATTERN = /^[\p{L}\p{N}_]+$/u;
const RESERVED_RECOVERY_JOB_IDS = new Set([
  "AUX",
  "CON",
  "NUL",
  "PRN",
  ...Array.from({ length: 9 }, (_, index) => `COM${index + 1}`),
  ...Array.from({ length: 9 }, (_, index) => `LPT${index + 1}`),
]);

const READY_FIELDS = Object.freeze([
  "app_version",
  "channel",
  "event",
  "port",
  "protocol_version",
]);

class SidecarError extends Error {
  constructor(code, details = undefined) {
    super(code);
    this.name = "SidecarError";
    this.code = code;
    this.details = details;
  }
}

function minimalChildEnvironment(environment = process.env, platform = process.platform) {
  const common = ["LANG", "LC_ALL", "LC_CTYPE", "TZ"];
  const platformNames =
    platform === "win32"
      ? [
          "APPDATA",
          "HOMEDRIVE",
          "HOMEPATH",
          "LOCALAPPDATA",
          "SYSTEMROOT",
          "TEMP",
          "TMP",
          "USERPROFILE",
          "WINDIR",
        ]
      : platform === "darwin"
        ? ["HOME", "TMPDIR"]
        : ["HOME", "TMPDIR", "XDG_RUNTIME_DIR"];
  const result = Object.create(null);
  for (const name of [...common, ...platformNames]) {
    const value = environment[name];
    if (
      typeof value === "string" &&
      value.length > 0 &&
      value.length <= 32_768 &&
      !value.includes("\0")
    ) {
      result[name] = value;
    }
  }
  return result;
}

function encodeRecord(payload) {
  const encoded = Buffer.from(`${JSON.stringify(payload)}\n`, "utf8");
  if (encoded.length - 1 > MAX_PROTOCOL_RECORD_BYTES) {
    throw new SidecarError("DESKTOP_SIDECAR_START_RECORD_TOO_LARGE");
  }
  return encoded;
}

function validateReadyRecord(payload, { appVersion, channel }) {
  if (payload === null || typeof payload !== "object" || Array.isArray(payload)) {
    throw new SidecarError("DESKTOP_SIDECAR_READY_INVALID");
  }
  const fields = Object.keys(payload).sort();
  const expected = [...READY_FIELDS].sort();
  if (
    fields.length !== expected.length ||
    fields.some((field, index) => field !== expected[index])
  ) {
    throw new SidecarError("DESKTOP_SIDECAR_READY_INVALID");
  }
  if (payload.protocol_version !== PROTOCOL_VERSION) {
    throw new SidecarError("DESKTOP_SIDECAR_PROTOCOL_MISMATCH");
  }
  if (
    payload.event !== "ready" ||
    payload.app_version !== appVersion ||
    payload.channel !== channel ||
    !Number.isInteger(payload.port) ||
    payload.port < 1 ||
    payload.port > 65_535
  ) {
    throw new SidecarError("DESKTOP_SIDECAR_READY_INVALID");
  }
  return Object.freeze({ port: payload.port });
}

function parseReadinessLine(line, expected) {
  let payload;
  try {
    payload = JSON.parse(line.toString("utf8"));
  } catch (_error) {
    throw new SidecarError("DESKTOP_SIDECAR_READY_MALFORMED");
  }
  if (payload?.event === "error") {
    const reportedCode =
      typeof payload.error_code === "string" &&
      /^[A-Z][A-Z0-9_]{0,127}$/.test(payload.error_code)
        ? payload.error_code
        : "SIDECAR_STARTUP_FAILED";
    throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { reportedCode });
  }
  return validateReadyRecord(payload, expected);
}

function isCanonicalRecoveryJobId(value) {
  return (
    typeof value === "string" &&
    value.length > 0 &&
    Buffer.byteLength(value, "utf8") <= 200 &&
    !value.startsWith("_") &&
    !value.endsWith("_") &&
    RECOVERY_JOB_ID_PATTERN.test(value) &&
    !RESERVED_RECOVERY_JOB_IDS.has(value.toUpperCase())
  );
}

function invalidRecoveryPlan(payload) {
  if (
    payload.recovery_kind !== "new_job" ||
    !PLAN_ID_PATTERN.test(payload.plan_id) ||
    !isCanonicalRecoveryJobId(payload.job_id) ||
    !["captured", "applied"].includes(payload.initial_status) ||
    !["none", "raw_only", "complete", "conflict"].includes(payload.file_state) ||
    ![
      "not_checked",
      "database_missing",
      "job_missing",
      "captured",
      "assessed",
      "applied",
      "conflict",
    ].includes(payload.ledger_state) ||
    !["not_checked", "missing", "exact", "conflict"].includes(
      payload.receipt_state,
    ) ||
    !["advance", "schema_required", "manual_review"].includes(
      payload.disposition,
    ) ||
    typeof payload.can_advance !== "boolean"
  ) {
    return true;
  }
  if (payload.disposition === "advance") {
    return (
      !payload.can_advance ||
      !["ledger", "assessment", "applied", "terminal"].includes(
        payload.next_step,
      ) ||
      payload.reason_code !== "JOB_INTAKE_RECOVERY_STEP_AVAILABLE"
    );
  }
  if (payload.disposition === "schema_required") {
    return (
      payload.can_advance ||
      payload.next_step !== "" ||
      payload.reason_code !== "SCHEMA_PREPARATION_REQUIRED"
    );
  }
  return (
    payload.can_advance ||
    payload.next_step !== "" ||
    ![
      "JOB_INTAKE_RECOVERY_FILES_INCOMPLETE",
      "JOB_INTAKE_RECOVERY_STATE_CONFLICT",
    ].includes(payload.reason_code)
  );
}

function parseMaintenanceLine(line, expectedEvent) {
  let payload;
  try {
    payload = JSON.parse(line.toString("utf8"));
  } catch (_error) {
    throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_MALFORMED");
  }
  if (payload?.event === "error") {
    const reportedCode =
      typeof payload.error_code === "string" &&
      /^[A-Z][A-Z0-9_]{0,127}$/.test(payload.error_code)
        ? payload.error_code
        : "SIDECAR_MAINTENANCE_FAILED";
    throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { reportedCode });
  }
  if (payload === null || typeof payload !== "object" || Array.isArray(payload)) {
    throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
  }
  const exactFields = {
    backup_create_result: BACKUP_RESULT_FIELDS,
    backup_restore_result: RESTORE_RESULT_FIELDS,
    backup_restore_recovery_result: RESTORE_RESULT_FIELDS,
    data_home_recovery_inspected: ["event", "protocol_version", "recovery_kind"],
    email_credential_recovered: [
      "cleanup", "cleanup_attempted", "cleanup_confirmed", "database_side",
      "error_code", "event", "intent_removed", "protocol_version", "success",
    ],
    document_migration_plan: [
      "ciphertext_file_count",
      "document_count",
      "event",
      "plan_id",
      "protocol_version",
      "source_format_version",
      "target_format_version",
      "total_ciphertext_bytes",
    ],
    document_migration_complete: [
      "document_count",
      "event",
      "plan_id",
      "protocol_version",
    ],
    document_migration_recovered: ["event", "protocol_version", "recovered"],
    data_home_recovery_plan: [
      "can_advance",
      "disposition",
      "event",
      "file_state",
      "initial_status",
      "job_id",
      "ledger_state",
      "next_step",
      "plan_id",
      "protocol_version",
      "reason_code",
      "receipt_state",
      "recovery_kind",
    ],
    data_home_recovery_advanced: [
      "confirmed_plan",
      "event",
      "protocol_version",
      "settled",
      "step",
    ],
  }[expectedEvent];
  const fields = Object.keys(payload).sort();
  if (
    !exactFields ||
    fields.length !== exactFields.length ||
    fields.some((field, index) => field !== exactFields[index]) ||
    payload.event !== expectedEvent ||
    payload.protocol_version !== PROTOCOL_VERSION
  ) {
    throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
  }
  if (expectedEvent === "data_home_recovery_inspected") {
    if (!["new_job", "email_credential", "restore"].includes(payload.recovery_kind)) {
      throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
    }
    return Object.freeze({ recoveryKind: payload.recovery_kind });
  }
  if (expectedEvent === "backup_create_result") {
    const result = backupResult(payload);
    if (!result) throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
    return result;
  }
  if (isRestoreEvent(expectedEvent)) {
    const result = restoreResult(payload);
    if (!result) throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
    return result;
  }
  if (expectedEvent === "email_credential_recovered") {
    if (
      !["before", "after", "unknown"].includes(payload.database_side) ||
      !["none", "new", "old", "unknown"].includes(payload.cleanup) ||
      ["success", "cleanup_attempted", "cleanup_confirmed", "intent_removed"].some(
        (field) => typeof payload[field] !== "boolean",
      ) || typeof payload.error_code !== "string" ||
      (payload.success ? payload.error_code !== "" : !/^[A-Z][A-Z0-9_]{0,127}$/.test(payload.error_code)) ||
      (payload.success && (!payload.cleanup_confirmed || !payload.intent_removed ||
        payload.database_side === "unknown" || payload.cleanup === "unknown")) ||
      (payload.intent_removed && !payload.cleanup_confirmed)
    ) {
      throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
    }
    return Object.freeze({
      success: payload.success, errorCode: payload.error_code,
      databaseSide: payload.database_side, cleanup: payload.cleanup,
      cleanupAttempted: payload.cleanup_attempted, cleanupConfirmed: payload.cleanup_confirmed,
      intentRemoved: payload.intent_removed,
    });
  }
  if (expectedEvent === "data_home_recovery_advanced") {
    if (
      !PLAN_ID_PATTERN.test(payload.confirmed_plan) ||
      !["ledger", "assessment", "applied", "terminal"].includes(payload.step) ||
      typeof payload.settled !== "boolean" ||
      (payload.settled && payload.step !== "terminal")
    ) {
      throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
    }
    return Object.freeze({
      confirmedPlan: payload.confirmed_plan,
      settled: payload.settled,
      step: payload.step,
    });
  }
  if (expectedEvent === "data_home_recovery_plan") {
    if (invalidRecoveryPlan(payload)) {
      throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
    }
    return Object.freeze({
      canAdvance: payload.can_advance,
      disposition: payload.disposition,
      fileState: payload.file_state,
      initialStatus: payload.initial_status,
      jobId: payload.job_id,
      ledgerState: payload.ledger_state,
      nextStep: payload.next_step,
      planId: payload.plan_id,
      reasonCode: payload.reason_code,
      receiptState: payload.receipt_state,
      recoveryKind: payload.recovery_kind,
    });
  }
  if (expectedEvent === "document_migration_recovered") {
    if (typeof payload.recovered !== "boolean") {
      throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
    }
    return Object.freeze({ recovered: payload.recovered });
  }
  if (
    !PLAN_ID_PATTERN.test(payload.plan_id) ||
    !Number.isSafeInteger(payload.document_count) ||
    payload.document_count < 0
  ) {
    throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
  }
  if (expectedEvent === "document_migration_complete") {
    return Object.freeze({
      documentCount: payload.document_count,
      planId: payload.plan_id,
    });
  }
  if (
    payload.source_format_version !== 1 ||
    payload.target_format_version !== 2 ||
    !Number.isSafeInteger(payload.ciphertext_file_count) ||
    payload.ciphertext_file_count < 0 ||
    !Number.isSafeInteger(payload.total_ciphertext_bytes) ||
    payload.total_ciphertext_bytes < 0
  ) {
    throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
  }
  return Object.freeze({
    ciphertextFileCount: payload.ciphertext_file_count,
    documentCount: payload.document_count,
    planId: payload.plan_id,
    sourceFormatVersion: payload.source_format_version,
    targetFormatVersion: payload.target_format_version,
    totalCiphertextBytes: payload.total_ciphertext_bytes,
  });
}

function waitForExit(child, timeoutMs) {
  if (child.exitCode != null || child.signalCode != null) {
    return Promise.resolve(true);
  }
  return new Promise((resolve) => {
    let settled = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      child.removeListener("exit", onExit);
      resolve(value);
    };
    const onExit = () => finish(true);
    const timer = setTimeout(() => finish(false), timeoutMs);
    timer.unref?.();
    child.once("exit", onExit);
  });
}

class SidecarSupervisor extends EventEmitter {
  constructor({
    spawn = childProcess.spawn,
    fetchImpl = globalThis.fetch,
    logger = undefined,
    startupTimeoutMs = DEFAULT_STARTUP_TIMEOUT_MS,
    healthTimeoutMs = DEFAULT_HEALTH_TIMEOUT_MS,
    shutdownTimeoutMs = DEFAULT_SHUTDOWN_TIMEOUT_MS,
    terminationTimeoutMs = DEFAULT_TERMINATION_TIMEOUT_MS,
    maintenanceTimeoutMs = DEFAULT_MAINTENANCE_TIMEOUT_MS,
    backupTimeoutMs = DEFAULT_BACKUP_TIMEOUT_MS,
    restoreTimeoutMs = DEFAULT_RESTORE_TIMEOUT_MS,
    platform = process.platform,
    environment = process.env,
  } = {}) {
    super();
    if (typeof spawn !== "function" || typeof fetchImpl !== "function") {
      throw new TypeError("Sidecar process and health adapters are required");
    }
    this._spawn = spawn;
    this._fetch = fetchImpl;
    this._logger = logger;
    this._startupTimeoutMs = startupTimeoutMs;
    this._healthTimeoutMs = healthTimeoutMs;
    this._shutdownTimeoutMs = shutdownTimeoutMs;
    this._terminationTimeoutMs = terminationTimeoutMs;
    this._maintenanceTimeoutMs = maintenanceTimeoutMs;
    this._backupTimeoutMs = backupTimeoutMs;
    this._restoreTimeoutMs = restoreTimeoutMs;
    this._platform = platform;
    this._environment = environment;
    this._child = null;
    this._state = "idle";
    this._origin = null;
    this._sessionToken = null;
    this._runtimeStdoutBytes = 0;
    this._runtimeProtocolViolation = false;
    this._runningConfiguration = null;
    this._backupOperation = null;
    this._restoreOperation = null;
    this._restoreRecoveryConfiguration = null;
  }

  get state() {
    return this._state;
  }

  get origin() {
    return this._origin;
  }

  // The window's capability token (ADR SEC-250). Generated here, sent to the
  // sidecar only over its stdin, and handed to the owned page through the
  // preload; never read back from sidecar output. Null unless ready.
  get sessionToken() {
    return this._state === "ready" ? this._sessionToken : null;
  }

  get ownsChild() {
    return this._child !== null;
  }

  get backupInProgress() {
    return this._backupOperation !== null;
  }

  get restoreInProgress() { return this._restoreOperation !== null; }
  get maintenanceInProgress() { return this.backupInProgress || this.restoreInProgress; }

  // This is only permission to attempt explicitly confirmed recovery, not proof
  // of a valid restore journal. The native owner re-admits its exact kind/scope.
  get canAttemptRestoreRecovery() {
    return !this.maintenanceInProgress && !this.ownsChild && this._state === "idle" &&
      this._restoreRecoveryConfiguration !== null;
  }

  _assertNoMaintenanceOperation() {
    if (this.restoreInProgress) throw new SidecarError("DESKTOP_RESTORE_BUSY");
    if (this.backupInProgress) throw new SidecarError("DESKTOP_BACKUP_BUSY");
  }

  async start(configuration) {
    this._assertNoMaintenanceOperation();
    return this._start(configuration);
  }

  async _start({ executablePath, deploymentRoot, dataRoot, channel, appVersion,
    mode = "readonly", extensionOrigins = [] }) {
    if (this._child !== null || this._state !== "idle") {
      throw new SidecarError("DESKTOP_SIDECAR_ALREADY_OWNED");
    }
    if (
      ![executablePath, deploymentRoot, dataRoot, channel, appVersion].every(
        (value) => typeof value === "string" && value.length > 0 && !value.includes("\0"),
      )
    ) {
      throw new SidecarError("DESKTOP_SIDECAR_START_INVALID");
    }

    let launch;
    try {
      launch = sidecarLaunchOptions({ mode, extensionOrigins });
    } catch (_error) {
      throw new SidecarError("DESKTOP_SIDECAR_START_INVALID");
    }
    const runningConfiguration = Object.freeze({ executablePath, deploymentRoot, dataRoot, channel, appVersion,
      mode, extensionOrigins: Object.freeze([...extensionOrigins]) });
    // A new admitted start supersedes recovery authority for a previous Home.
    this._restoreRecoveryConfiguration = null;
    this._state = "starting";
    let child;
    try {
      child = this._spawn(executablePath, launch.args, {
        cwd: deploymentRoot,
        detached: false,
        env: minimalChildEnvironment(this._environment, this._platform),
        shell: false,
        stdio: ["pipe", "pipe", "pipe"],
        windowsHide: true,
      });
    } catch (_error) {
      this._state = "idle";
      throw new SidecarError("DESKTOP_SIDECAR_SPAWN_FAILED");
    }
    this._child = child;
    this._attachOwnedChildExit(child);
    this._runtimeStdoutBytes = 0;
    this._runtimeProtocolViolation = false;
    this._attachStderrDrain(child);
    child.stdin?.on("error", () => {});

    try {
      const readinessPromise = this._readReadiness(child, { appVersion, channel });
      // 32 random bytes as unpadded URL-safe base64: the exact format the
      // sidecar's start-record parser accepts.
      const sessionToken = crypto.randomBytes(32).toString("base64url");
      const startRecord = encodeRecord({
        command: "start",
        protocol_version: PROTOCOL_VERSION,
        port: 0,
        resource_root: deploymentRoot,
        data_root: dataRoot,
        channel,
        session_token: sessionToken,
      });
      if (!child.stdin?.writable) {
        throw new SidecarError("DESKTOP_SIDECAR_START_WRITE_FAILED");
      }
      child.stdin.write(startRecord);
      const ready = await readinessPromise;
      if (launch.port !== 0 && ready.port !== launch.port) {
        throw new SidecarError("DESKTOP_SIDECAR_READY_INVALID");
      }
      const origin = `http://127.0.0.1:${ready.port}`;
      this._attachRuntimeStdoutDrain(child);
      await this._verifyHealth(origin, appVersion);
      if (child.exitCode != null || child.signalCode != null || this._child !== child) {
        throw new SidecarError("DESKTOP_SIDECAR_EARLY_EXIT");
      }
      this._state = "ready";
      this._origin = origin;
      this._sessionToken = sessionToken;
      this._runningConfiguration = runningConfiguration;
      return Object.freeze({ origin, port: ready.port });
    } catch (error) {
      if (mode === "go-bootstrap" && error instanceof SidecarError &&
          error.code === "DESKTOP_SIDECAR_REPORTED_ERROR" && error.details?.reportedCode === "DATA_HOME_RECOVERY_REQUIRED") {
        this._restoreRecoveryConfiguration = runningConfiguration;
      }
      await this._stopOwnedChild({ graceful: false });
      if (error instanceof SidecarError) throw error;
      throw new SidecarError("DESKTOP_SIDECAR_START_FAILED");
    }
  }

  async runMaintenance(request) {
    this._assertNoMaintenanceOperation();
    return this._runMaintenance(request);
  }

  async _runMaintenance({ executablePath, deploymentRoot, record, expectedEvent }) {
    if (this._child !== null || this._state !== "idle") {
      throw new SidecarError("DESKTOP_SIDECAR_ALREADY_OWNED");
    }
    if (
      ![executablePath, deploymentRoot, expectedEvent].every(
        (value) => typeof value === "string" && value.length > 0 && !value.includes("\0"),
      ) ||
      record === null ||
      typeof record !== "object" ||
      Array.isArray(record)
    ) {
      throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_INVALID");
    }

    this._state = "maintenance";
    let child;
    try {
      child = this._spawn(executablePath, ["--desktop-maintenance"], {
        cwd: deploymentRoot,
        detached: false,
        env: minimalChildEnvironment(this._environment, this._platform),
        shell: false,
        stdio: ["pipe", "pipe", "pipe"],
        windowsHide: true,
      });
    } catch (_error) {
      this._state = "idle";
      throw new SidecarError("DESKTOP_SIDECAR_SPAWN_FAILED");
    }
    this._child = child;
    this._attachOwnedChildExit(child);
    this._attachStderrDrain(child);
    child.stdin?.on("error", () => {});

    let recoveryResponse = null;
    let backupResponse = null;
    let restoreResponse = null;
    try {
      const responsePromise = this._readProtocolLine(
        child,
        (line) => parseMaintenanceLine(line, expectedEvent),
        isRestoreEvent(expectedEvent) ? this._restoreTimeoutMs : expectedEvent === "backup_create_result" ? this._backupTimeoutMs : this._maintenanceTimeoutMs,
        "DESKTOP_SIDECAR_MAINTENANCE_TIMEOUT",
      );
      if (!child.stdin?.writable) {
        throw new SidecarError("DESKTOP_SIDECAR_START_WRITE_FAILED");
      }
      child.stdin.end(encodeRecord(record));
      const response = await responsePromise;
      if (expectedEvent === "email_credential_recovered") recoveryResponse = response;
      if (expectedEvent === "backup_create_result") backupResponse = response;
      if (isRestoreEvent(expectedEvent)) restoreResponse = response;
      const exited = await waitForExit(child, this._shutdownTimeoutMs);
      if (!exited) {
        throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_EXIT_TIMEOUT");
      }
      if (recoveryResponse?.success === false) {
        throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { reportedCode: recoveryResponse.errorCode });
      }
      if (backupResponse?.success === false) {
        throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { reportedCode: backupResponse.errorCode });
      }
      if (restoreResponse?.success === false) {
        throw new SidecarError("DESKTOP_SIDECAR_REPORTED_ERROR", { reportedCode: restoreResponse.errorCode });
      }
      if (child.exitCode !== 0) {
        throw new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_EXIT_FAILED");
      }
      if (this._child === child) {
        this._child = null;
        this._state = "idle";
      }
      return response;
    } catch (error) {
      let failure = error instanceof SidecarError ? error : new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_FAILED");
      try {
        await this._stopOwnedChild({ graceful: false });
      } catch (stopError) {
        failure = stopError instanceof SidecarError ? stopError : new SidecarError("DESKTOP_SIDECAR_STOP_TIMEOUT");
      }
      // Only closed, validated protocol facts survive. They never prove a clean
      // exit: the caller must still treat this as failed/uncertain completion.
      if (recoveryResponse) {
        failure = new SidecarError(failure.code, Object.freeze({
          ...failure.details, recovery: recoveryResponse,
        }));
      }
      if (expectedEvent === "backup_create_result") {
        failure = new SidecarError(failure.code, Object.freeze({
          ...failure.details, backup: backupResponse || absentBackupResult(true, failure.code),
        }));
      }
      if (isRestoreEvent(expectedEvent)) {
        failure = new SidecarError(failure.code, Object.freeze({ ...failure.details,
          restore: restoreResponse || absentRestoreResult(true, failure.code, expectedEvent === "backup_restore_recovery_result"),
        }));
      }
      throw failure;
    }
  }

  // Host-only operation: paths/runtime come from the last successful Go start,
  // never the renderer or a caller-supplied executable. Legacy Python/CLI/file
  // writers must be stopped by the user; the managed child's reap is enforced.
  // stop() is always permitted and consumes restart authority (quit/cancel).
  async createBackup(options) {
    this._assertNoMaintenanceOperation();
    const selection = backupOptions(options);
    if (!selection) throw new SidecarError("DESKTOP_BACKUP_REQUEST_INVALID");
    const configuration = this._runningConfiguration;
    if (!this.ownsChild || this._state !== "ready" || configuration?.mode !== "go-bootstrap") {
      throw new SidecarError("DESKTOP_BACKUP_NOT_READY");
    }
    const operation = { cancelled: false };
    this._backupOperation = operation;
    let dispatched = false, backup = null, failure = null, backendRestarted = false, restartErrorCode = "";
    const cancelled = () => {
      if (operation.cancelled) throw new SidecarError("DESKTOP_BACKUP_CANCELLED");
    };
    try {
      await this._stopOwnedChild({ graceful: true });
      cancelled();
      // The supervisor retains an unreaped child on timeout; no successor can
      // claim the Home until the old handle is proven exited.
      if (this.ownsChild || this._state !== "idle") throw new SidecarError("DESKTOP_SIDECAR_ALREADY_OWNED");
      dispatched = true;
      try {
        backup = await this._runMaintenance({ executablePath: configuration.executablePath,
          deploymentRoot: configuration.deploymentRoot, expectedEvent: "backup_create_result",
          record: { ...selection, command: "backup_create", protocol_version: PROTOCOL_VERSION,
            data_root: configuration.dataRoot, resource_root: configuration.deploymentRoot, channel: configuration.channel } });
      } catch (error) {
        failure = error instanceof SidecarError ? error : new SidecarError("DESKTOP_SIDECAR_MAINTENANCE_FAILED");
        backup = failure.details?.backup || absentBackupResult(true, failure.code);
      }
      cancelled();
      if (this.ownsChild || this._state !== "idle") throw failure || new SidecarError("DESKTOP_SIDECAR_STOP_TIMEOUT");
      // Backup never activates/replaces source data. Restart may be attempted
      // after a reaped failure too; ordinary startup still refuses recovery.
      let ready;
      try {
        ready = await this._start(configuration);
        cancelled();
        backendRestarted = true;
      } catch (error) {
        restartErrorCode = error instanceof SidecarError ? error.code : "DESKTOP_SIDECAR_START_FAILED";
        throw failure || error;
      }
      if (failure) throw failure;
      return Object.freeze({ backup, backendRestarted, origin: ready.origin });
    } catch (error) {
      const problem = operation.cancelled && !this.ownsChild
        ? new SidecarError("DESKTOP_BACKUP_CANCELLED")
        : error instanceof SidecarError ? error : new SidecarError("DESKTOP_BACKUP_FAILED");
      throw new SidecarError(problem.code, Object.freeze({ ...problem.details,
        backup: backup || absentBackupResult(dispatched, problem.code), backendRestarted, restartErrorCode,
        childStillOwned: this.ownsChild }));
    } finally {
      if (this._backupOperation === operation) this._backupOperation = null;
    }
  }

  async restoreBackup(options) {
    this._assertNoMaintenanceOperation();
    const selection = restoreOptions(options);
    if (!selection) throw new SidecarError("DESKTOP_RESTORE_REQUEST_INVALID");
    const configuration = this._runningConfiguration;
    if (!this.ownsChild || this._state !== "ready" || configuration?.mode !== "go-bootstrap") {
      throw new SidecarError("DESKTOP_RESTORE_NOT_READY");
    }
    return this._runRestore(configuration, selection, false);
  }

  async recoverRestore(options) {
    this._assertNoMaintenanceOperation();
    const selection = restoreOptions(options, true);
    if (!selection) throw new SidecarError("DESKTOP_RESTORE_REQUEST_INVALID");
    if (!this.canAttemptRestoreRecovery) throw new SidecarError("DESKTOP_RESTORE_RECOVERY_NOT_READY");
    return this._runRestore(this._restoreRecoveryConfiguration, selection, true);
  }

  // Host-only, with no caller-supplied runtime authority. Recovery is bound to
  // this host's failed Go start/restore, never an arbitrary configuration.
  async _runRestore(configuration, selection, recovery) {
    const operation = { cancelled: false };
    this._restoreOperation = operation;
    let dispatched = false, restore = null, backendRestarted = false, restartErrorCode = "";
    const cancelled = () => {
      if (operation.cancelled) throw new SidecarError("DESKTOP_RESTORE_CANCELLED");
    };
    try {
      await this._stopOwnedChild({ graceful: true });
      cancelled();
      if (this.ownsChild || this._state !== "idle") throw new SidecarError("DESKTOP_SIDECAR_ALREADY_OWNED");
      dispatched = true;
      this._restoreRecoveryConfiguration = configuration;
      restore = await this._runMaintenance({ executablePath: configuration.executablePath,
        deploymentRoot: configuration.deploymentRoot,
        expectedEvent: recovery ? "backup_restore_recovery_result" : "backup_restore_result",
        record: { ...selection, command: recovery ? "backup_restore_recover" : "backup_restore", protocol_version: PROTOCOL_VERSION,
          data_root: configuration.dataRoot, resource_root: configuration.deploymentRoot, channel: configuration.channel } });
      cancelled();
      // The transport already validates all terminal proof and a clean exit.
      // Unlike backup, ANY failure or ambiguity must stop here: no implicit
      // restart, second restore or rollback, even if a late result says complete.
      if (!restore.success || !restore.complete || restore.journalRemoval !== "removed") {
        throw new SidecarError("DESKTOP_RESTORE_RESULT_INVALID");
      }
      if (this.ownsChild || this._state !== "idle") throw new SidecarError("DESKTOP_SIDECAR_STOP_TIMEOUT");
      this._restoreRecoveryConfiguration = null;
      let ready;
      try {
        ready = await this._start(configuration);
        cancelled();
        backendRestarted = true;
      } catch (error) {
        restartErrorCode = error instanceof SidecarError ? error.code : "DESKTOP_SIDECAR_START_FAILED";
        throw error;
      }
      return Object.freeze({ restore, backendRestarted, origin: ready.origin });
    } catch (error) {
      const failure = error instanceof SidecarError ? error : new SidecarError("DESKTOP_RESTORE_FAILED");
      restore = restore || failure.details?.restore || absentRestoreResult(dispatched, failure.code, recovery);
      const code = operation.cancelled && !this.ownsChild ? "DESKTOP_RESTORE_CANCELLED" : failure.code;
      throw new SidecarError(code, Object.freeze({ ...failure.details, restore, backendRestarted,
        restartErrorCode, childStillOwned: this.ownsChild }));
    } finally {
      if (this._restoreOperation === operation) this._restoreOperation = null;
    }
  }

  _readReadiness(child, expected) {
    return this._readProtocolLine(
      child,
      (line) => parseReadinessLine(line, expected),
      this._startupTimeoutMs,
      "DESKTOP_SIDECAR_START_TIMEOUT",
    );
  }

  _readProtocolLine(child, parser, timeoutMs, timeoutCode) {
    return new Promise((resolve, reject) => {
      let buffer = Buffer.alloc(0);
      let settled = false;
      const finish = (error, value) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        child.stdout?.removeListener("data", onData);
        child.removeListener("error", onError);
        child.removeListener("exit", onExit);
        if (error) reject(error);
        else resolve(value);
      };
      const onData = (chunk) => {
        buffer = Buffer.concat([buffer, Buffer.from(chunk)]);
        const newlineIndex = buffer.indexOf(0x0a);
        if (newlineIndex < 0) {
          if (buffer.length > MAX_PROTOCOL_RECORD_BYTES + 1) {
            finish(new SidecarError("DESKTOP_SIDECAR_STDOUT_TOO_LARGE"));
          }
          return;
        }
        if (newlineIndex > MAX_PROTOCOL_RECORD_BYTES) {
          finish(new SidecarError("DESKTOP_SIDECAR_STDOUT_TOO_LARGE"));
          return;
        }
        const remainder = buffer.subarray(newlineIndex + 1);
        if (remainder.length > 0) {
          finish(new SidecarError("DESKTOP_SIDECAR_READY_DUPLICATE"));
          return;
        }
        try {
          const line = buffer.subarray(0, newlineIndex);
          finish(undefined, parser(line));
        } catch (error) {
          finish(error);
        }
      };
      const onError = () => finish(new SidecarError("DESKTOP_SIDECAR_SPAWN_FAILED"));
      const onExit = () => finish(new SidecarError("DESKTOP_SIDECAR_EARLY_EXIT"));
      const timer = setTimeout(
        () => finish(new SidecarError(timeoutCode)),
        timeoutMs,
      );
      timer.unref?.();
      child.stdout?.on("data", onData);
      child.once("error", onError);
      child.once("exit", onExit);
    });
  }

  async _verifyHealth(origin, appVersion) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this._healthTimeoutMs);
    timer.unref?.();
    try {
      const response = await this._fetch(`${origin}/api/health`, {
        cache: "no-store",
        headers: { Accept: "application/json" },
        method: "GET",
        redirect: "error",
        signal: controller.signal,
      });
      if (!response.ok || response.status !== 200) {
        throw new SidecarError("DESKTOP_SIDECAR_HEALTH_FAILED");
      }
      const payload = await response.json();
      const fields = Object.keys(payload).sort();
      if (
        fields.join(",") !== "app_version,protocol_version,status" ||
        payload.status !== "ok" ||
        payload.protocol_version !== PROTOCOL_VERSION ||
        payload.app_version !== appVersion
      ) {
        throw new SidecarError("DESKTOP_SIDECAR_HEALTH_INVALID");
      }
    } catch (error) {
      if (error instanceof SidecarError) throw error;
      throw new SidecarError("DESKTOP_SIDECAR_HEALTH_FAILED");
    } finally {
      clearTimeout(timer);
    }
  }

  _attachStderrDrain(child) {
    child.stderr?.on("data", (chunk) => {
      const bytes = Math.min(Buffer.byteLength(chunk), 1_048_576);
      this._logger?.warn?.("sidecar stderr suppressed", { bytes });
    });
  }

  _attachRuntimeStdoutDrain(child) {
    child.stdout?.on("data", (chunk) => {
      if (this._runtimeProtocolViolation) return;
      this._runtimeStdoutBytes += Buffer.byteLength(chunk);
      if (this._runtimeStdoutBytes > MAX_PROTOCOL_RECORD_BYTES) {
        this._runtimeProtocolViolation = true;
        this.emit("protocol-error", "DESKTOP_SIDECAR_RUNTIME_OUTPUT_INVALID");
      }
    });
  }

  _attachOwnedChildExit(child) {
    child.once("exit", (code, signal) => {
      if (this._child !== child) return;
      const reportRuntimeExit = this._state === "ready";
      this._child = null;
      this._origin = null;
      this._sessionToken = null;
      this._state = "idle";
      if (reportRuntimeExit) {
        this.emit("exit", {
          code: Number.isInteger(code) ? code : null,
          signal: typeof signal === "string" ? signal : null,
        });
      }
    });
  }

  async stop() {
    if (this._backupOperation) this._backupOperation.cancelled = true;
    if (this._restoreOperation) this._restoreOperation.cancelled = true;
    await this._stopOwnedChild({ graceful: true });
  }

  async _stopOwnedChild({ graceful }) {
    const child = this._child;
    if (child === null) {
      this._state = "idle";
      this._origin = null;
      this._sessionToken = null;
      return;
    }
    this._state = "stopping";
    if (graceful && child.stdin?.writable) {
      try {
        child.stdin.write(
          encodeRecord({ command: "shutdown", protocol_version: PROTOCOL_VERSION }),
        );
      } catch (_error) {
        // The exact owned child is still terminated below if graceful write fails.
      }
    } else {
      child.stdin?.destroy();
    }

    let exited = await waitForExit(child, this._shutdownTimeoutMs);
    if (!exited) {
      child.stdin?.destroy();
      try {
        child.kill();
      } catch (_error) {
        // Re-check the retained handle; never fall back to a process-name kill.
      }
      exited = await waitForExit(child, this._terminationTimeoutMs);
    }
    if (!exited) {
      throw new SidecarError("DESKTOP_SIDECAR_STOP_TIMEOUT");
    }
    if (this._child === child) {
      this._child = null;
      this._origin = null;
      this._sessionToken = null;
      this._state = "idle";
    }
  }
}

module.exports = {
  MAX_PROTOCOL_RECORD_BYTES,
  PROTOCOL_VERSION,
  SidecarError,
  SidecarSupervisor,
  minimalChildEnvironment,
  parseMaintenanceLine,
  parseReadinessLine,
  validateReadyRecord,
};
