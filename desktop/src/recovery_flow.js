"use strict";

const PROTOCOL_VERSION = 1;
const { SIDECAR_CAPABILITIES } = require("./app_paths");
const { SidecarError } = require("./sidecar");
const { publicRestore, absentRestoreResult } = require("./restore_protocol");

class RecoveryFlowError extends Error {
  constructor(code) {
    super(code);
    this.name = "RecoveryFlowError";
    this.code = code;
  }
}

function publicPlan(plan) {
  return Object.freeze({
    canAdvance: plan.canAdvance,
    disposition: plan.disposition,
    fileState: plan.fileState,
    initialStatus: plan.initialStatus,
    ledgerState: plan.ledgerState,
    nextStep: plan.nextStep,
    reasonCode: plan.reasonCode,
    receiptState: plan.receiptState,
    recoveryKind: plan.recoveryKind,
  });
}

class DataHomeRecoveryFlow {
  constructor({ channel, resolveRuntime, runMaintenance }) {
    if (
      !["dev", "stable"].includes(channel) ||
      typeof resolveRuntime !== "function" ||
      typeof runMaintenance !== "function"
    ) {
      throw new TypeError("Recovery flow configuration is invalid");
    }
    this._channel = channel;
    this._resolveRuntime = resolveRuntime;
    this._runMaintenance = runMaintenance;
    this._pendingPlan = null;
    this._pendingRuntime = null;
    this._busy = false;
  }

  get busy() {
    return this._busy;
  }

  clear() {
    if (this._busy) {
      throw new RecoveryFlowError("DESKTOP_RECOVERY_BUSY");
    }
    this._pendingPlan = null;
    this._pendingRuntime = null;
  }

  async plan() {
    return this._exclusive(async () => {
      this._pendingPlan = null;
      this._pendingRuntime = null;
      const plan = await this._requestPlan();
      this._pendingPlan = plan;
      return publicPlan(plan);
    });
  }

  async advance(request) {
    return this._exclusive(async () => {
      const reviewed = this._pendingPlan;
      if (reviewed === null || !reviewed.canAdvance) {
        throw new RecoveryFlowError("DESKTOP_RECOVERY_ADVANCE_NOT_ALLOWED");
      }
      if (reviewed.recoveryKind === "restore" && (!request || typeof request !== "object" ||
          Array.isArray(request) || Object.keys(request).length !== 1 || request.confirmWritersStopped !== true)) {
        throw new RecoveryFlowError("DESKTOP_RESTORE_REQUEST_INVALID");
      }
      this._pendingPlan = null;
      const expectedRuntime = this._pendingRuntime;
      this._pendingRuntime = null;
      const { dataRoot, sidecarPaths } = this._resolveRuntime();
      if (!expectedRuntime || dataRoot !== expectedRuntime.dataRoot ||
          sidecarPaths.deploymentRoot !== expectedRuntime.deploymentRoot ||
          sidecarPaths.executablePath !== expectedRuntime.executablePath) {
        throw new RecoveryFlowError("DESKTOP_RECOVERY_RUNTIME_CHANGED");
      }
      if (reviewed.recoveryKind === "restore") {
        if (!sidecarPaths.runtimeCapabilities?.includes(SIDECAR_CAPABILITIES.BACKUP_RESTORE_V1)) {
          throw new RecoveryFlowError("DESKTOP_RECOVERY_RESULT_MISMATCH");
        }
        let facts = null;
        try {
          const result = await this._runMaintenance({ deploymentRoot: sidecarPaths.deploymentRoot, executablePath: sidecarPaths.executablePath,
            expectedEvent: "backup_restore_recovery_result", record: { channel: this._channel, command: "backup_restore_recover",
              confirm_writers_stopped: true, data_root: dataRoot, resource_root: sidecarPaths.deploymentRoot, protocol_version: PROTOCOL_VERSION } });
          facts = publicRestore(result);
          if (!facts?.recovery || !facts.success || !facts.complete || facts.journalRemoval !== "removed") {
            throw new RecoveryFlowError("DESKTOP_RECOVERY_RESULT_MISMATCH");
          }
          return Object.freeze({ settled: true, step: "restore" });
        } catch (error) {
          const code = error instanceof SidecarError ? error.details?.reportedCode || error.code : "DESKTOP_RECOVERY_RESULT_MISMATCH";
          const errorCode = typeof code === "string" && /^[A-Z][A-Z0-9_]{0,127}$/.test(code) ? code : "DESKTOP_RECOVERY_RESULT_MISMATCH";
          facts = facts || (error instanceof SidecarError && publicRestore(error.details?.restore));
          return Object.freeze({ settled: false, recoveryKind: "restore", errorCode,
            facts: facts?.recovery ? facts : absentRestoreResult(true, errorCode, true) });
        }
      }
      if (reviewed.recoveryKind === "email_credential") {
        try {
          const result = await this._runMaintenance({
            deploymentRoot: sidecarPaths.deploymentRoot,
            executablePath: sidecarPaths.executablePath,
            expectedEvent: "email_credential_recovered",
            record: { channel: this._channel, command: "email_credential_recover",
              confirm: true, data_root: dataRoot, resource_root: sidecarPaths.deploymentRoot,
              protocol_version: PROTOCOL_VERSION },
          });
          if (!result.success || !result.cleanupConfirmed || !result.intentRemoved) {
            throw new RecoveryFlowError("DESKTOP_RECOVERY_RESULT_MISMATCH");
          }
          return Object.freeze({ settled: true, step: "email_credential" });
        } catch (error) {
          // Failed confirmation is consumed, never automatically retried.
          // The renderer receives only the supervisor's validated nonsecret facts.
          const facts = error instanceof SidecarError ? error.details?.recovery : undefined;
          return Object.freeze({ settled: false, recoveryKind: "email_credential",
            errorCode: error instanceof SidecarError
              ? (error.details?.reportedCode || error.code) : "DESKTOP_RECOVERY_RESULT_MISMATCH",
            facts: facts || null });
        }
      }
      const advanced = await this._runMaintenance({
        deploymentRoot: sidecarPaths.deploymentRoot,
        executablePath: sidecarPaths.executablePath,
        expectedEvent: "data_home_recovery_advanced",
        record: {
          channel: this._channel,
          command: "data_home_recovery_advance",
          confirm_plan: reviewed.planId,
          data_root: dataRoot,
          protocol_version: PROTOCOL_VERSION,
          resource_root: sidecarPaths.deploymentRoot,
        },
      });
      if (advanced.confirmedPlan !== reviewed.planId) {
        throw new RecoveryFlowError("DESKTOP_RECOVERY_RESULT_MISMATCH");
      }
      if (advanced.settled) {
        return Object.freeze({ settled: true, step: advanced.step });
      }
      const next = await this._requestPlan();
      this._pendingPlan = next;
      return Object.freeze({
        plan: publicPlan(next),
        settled: false,
        step: advanced.step,
      });
    });
  }

  async _requestPlan() {
    const { dataRoot, sidecarPaths } = this._resolveRuntime();
    this._pendingRuntime = Object.freeze({ dataRoot,
      deploymentRoot: sidecarPaths.deploymentRoot, executablePath: sidecarPaths.executablePath });
    if (sidecarPaths.runtimeCapabilities?.some(value => [SIDECAR_CAPABILITIES.EMAIL_CREDENTIAL_RECOVERY_V1, SIDECAR_CAPABILITIES.BACKUP_RESTORE_V1].includes(value))) {
      const inspected = await this._runMaintenance({
        deploymentRoot: sidecarPaths.deploymentRoot, executablePath: sidecarPaths.executablePath,
        expectedEvent: "data_home_recovery_inspected",
        record: { channel: this._channel, command: "data_home_recovery_inspect",
          data_root: dataRoot, resource_root: sidecarPaths.deploymentRoot, protocol_version: PROTOCOL_VERSION },
      });
      if (inspected.recoveryKind === "restore") {
        if (!sidecarPaths.runtimeCapabilities.includes(SIDECAR_CAPABILITIES.BACKUP_RESTORE_V1)) throw new RecoveryFlowError("DESKTOP_RECOVERY_RESULT_MISMATCH");
        return Object.freeze({ canAdvance: true, disposition: "advance", recoveryKind: "restore",
          nextStep: "restore", reasonCode: "BACKUP_RESTORE_RECOVERY_REQUIRED" });
      }
      if (inspected.recoveryKind === "email_credential") {
        return Object.freeze({ canAdvance: true, disposition: "advance", recoveryKind: "email_credential",
          nextStep: "email_credential", reasonCode: "EMAIL_CREDENTIAL_RECOVERY_REQUIRED" });
      }
      if (inspected.recoveryKind !== "new_job") throw new RecoveryFlowError("DESKTOP_RECOVERY_RESULT_MISMATCH");
    }
    return this._runMaintenance({
      deploymentRoot: sidecarPaths.deploymentRoot,
      executablePath: sidecarPaths.executablePath,
      expectedEvent: "data_home_recovery_plan",
      record: {
        channel: this._channel,
        command: "data_home_recovery_plan",
        data_root: dataRoot,
        protocol_version: PROTOCOL_VERSION,
        resource_root: sidecarPaths.deploymentRoot,
      },
    });
  }

  async _exclusive(action) {
    if (this._busy) {
      throw new RecoveryFlowError("DESKTOP_RECOVERY_BUSY");
    }
    this._busy = true;
    try {
      return await action();
    } finally {
      this._busy = false;
    }
  }
}

module.exports = {
  DataHomeRecoveryFlow,
  RecoveryFlowError,
};
