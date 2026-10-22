"use strict";

const { SidecarError } = require("./sidecar");
const { backupResult, absentBackupResult } = require("./backup_protocol");

// Project only the supervisor's closed result contract, never exception prose,
// executable paths, or credentials. Revalidation also makes malformed outcomes
// conservative rather than allowing the view to claim a successful backup.
function publicBackup(value) {
  if (!value || typeof value !== "object") return null;
  return backupResult({ event: "backup_create_result", protocol_version: 1,
    success: value.success, error_code: value.errorCode,
    snapshot_namespace: value.snapshotNamespace, snapshot_relative_path: value.snapshotRelativePath,
    staged_files: value.stagedFiles, last_file_publication: value.lastFilePublication,
    last_file_temporary_cleanup_confirmed: value.lastFileTemporaryCleanupConfirmed,
    archive_relative_path: value.archiveRelativePath, archive_publication: value.archivePublication,
    archive_temporary_cleanup_confirmed: value.archiveTemporaryCleanupConfirmed,
    archive_files: value.archiveFiles, full_source_validated: value.fullSourceValidated,
    snapshot_cleanup: value.snapshotCleanup && { status: value.snapshotCleanup.status,
      removed_files: value.snapshotCleanup.removedFiles, removed_directories: value.snapshotCleanup.removedDirectories,
      last_removal: value.snapshotCleanup.lastRemoval, snapshot_removal: value.snapshotCleanup.snapshotRemoval } });
}

function safeCode(value, fallback) {
  return typeof value === "string" && /^[A-Z][A-Z0-9_]{0,127}$/.test(value) ? value : fallback;
}

class DesktopBackupFlow {
  constructor({ prepare, createBackup, onState = () => {} }) {
    if ([prepare, createBackup, onState].some((fn) => typeof fn !== "function")) {
      throw new TypeError("Backup flow callbacks are required");
    }
    this._prepare = prepare;
    this._createBackup = createBackup;
    this._onState = onState;
    this._state = Object.freeze({ phase: "idle", revision: 0, backup: null,
      backendRestarted: false, errorCode: "", restartErrorCode: "" });
    this.settled = Promise.resolve(this._state);
  }

  get busy() { return this._state.phase === "running"; }
  getState() { return this._state; }

  start(request) {
    if (this.busy) throw new Error("DESKTOP_BACKUP_BUSY");
    // The renderer may confirm quiescence; it cannot choose roots or paths.
    if (!request || typeof request !== "object" || Array.isArray(request) ||
        Object.keys(request).length !== 1 || request.confirmWritersStopped !== true) {
      throw new Error("DESKTOP_BACKUP_REQUEST_INVALID");
    }
    this._publish({ phase: "running", revision: this._state.revision + 1,
      backup: null, backendRestarted: false, errorCode: "", restartErrorCode: "" });
    // Detached from the renderer invocation: navigating away does not lose the
    // operation or its terminal facts. No automatic retry, including on error.
    this.settled = this._run();
    return Object.freeze({ accepted: true });
  }

  async _run() {
    let dispatched = false;
    try {
      await this._prepare();
      dispatched = true;
      const result = await this._createBackup({ confirmWritersStopped: true });
      const backup = publicBackup(result?.backup);
      if (!backup?.success || result?.backendRestarted !== true) {
        throw new SidecarError("DESKTOP_BACKUP_RESULT_INVALID", { backup,
          backendRestarted: result?.backendRestarted === true });
      }
      this._publish({ ...this._state, phase: "complete", backup, backendRestarted: true });
    } catch (error) {
      const known = error instanceof SidecarError;
      const errorCode = known ? safeCode(error.code, "DESKTOP_BACKUP_FAILED")
        : dispatched ? "DESKTOP_BACKUP_FAILED" : "DESKTOP_BACKUP_VIEW_UNAVAILABLE";
      this._publish({ ...this._state, phase: "error", errorCode,
        backup: (known && publicBackup(error.details?.backup)) || absentBackupResult(dispatched, errorCode),
        backendRestarted: known && error.details?.backendRestarted === true,
        restartErrorCode: known ? safeCode(error.details?.restartErrorCode, "") : "" });
    }
    return this._state;
  }

  _publish(state) {
    this._state = Object.freeze(state);
    // A closed/reloading renderer cannot change the disk outcome or provoke a
    // second backup. getState is authoritative when the next view connects.
    try { this._onState(this._state); } catch (_error) { /* view unavailable */ }
  }
}

module.exports = { DesktopBackupFlow };
