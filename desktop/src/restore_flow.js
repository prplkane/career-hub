"use strict";

const path = require("node:path");
const { SidecarError } = require("./sidecar");
const { archivePath } = require("./backup_protocol");
const { publicRestore, absentRestoreResult } = require("./restore_protocol");

// A native dialog may navigate outside its default directory. Constrain its
// result without opening it here; the Go owner rejects links/reparse points and
// verifies archive bytes under owned handles immediately before restoration.
function managedArchiveSelection(dataRoot, selectedPath, paths = path) {
  if (typeof dataRoot !== "string" || typeof selectedPath !== "string" ||
      !paths.isAbsolute(dataRoot) || !paths.isAbsolute(selectedPath) ||
      dataRoot.includes("\0") || selectedPath.includes("\0")) return null;
  const relative = paths.relative(dataRoot, selectedPath).split(paths.sep).join("/");
  return archivePath(relative) ? relative : null;
}

const safeCode = (value, fallback) => typeof value === "string" && /^[A-Z][A-Z0-9_]{0,127}$/.test(value) ? value : fallback;

class DesktopRestoreFlow {
  constructor({ chooseArchive, runtimeIdentity, prepare, restoreBackup, onState = () => {} }) {
    if ([chooseArchive, runtimeIdentity, prepare, restoreBackup, onState].some(fn => typeof fn !== "function")) {
      throw new TypeError("Restore flow callbacks are required");
    }
    this._chooseArchive = chooseArchive;
    this._runtimeIdentity = runtimeIdentity;
    this._prepare = prepare;
    this._restoreBackup = restoreBackup;
    this._onState = onState;
    this._selection = null;
    this._state = Object.freeze({ phase: "idle", revision: 0, archiveRelativePath: "", restore: null,
      backendRestarted: false, errorCode: "", restartErrorCode: "" });
    this.settled = Promise.resolve(this._state);
  }

  get busy() { return ["selecting", "running"].includes(this._state.phase); }
  getState() { return this._state; }

  async choose() {
    if (this.busy) throw new Error("DESKTOP_RESTORE_BUSY");
    this._selection = null;
    this._publish({ phase: "selecting", revision: this._state.revision + 1, archiveRelativePath: "", restore: null,
      backendRestarted: false, errorCode: "", restartErrorCode: "" });
    try {
      const identity = this._runtimeIdentity();
      if (typeof identity !== "string" || !identity) throw new SidecarError("DESKTOP_RESTORE_NOT_READY");
      const archive = await this._chooseArchive();
      if (archive === null) { this._publish({ ...this._state, phase: "idle" }); return this._state; }
      if (!archivePath(archive)) throw new SidecarError("DESKTOP_RESTORE_ARCHIVE_OUTSIDE_HOME");
      if (identity !== this._runtimeIdentity()) throw new SidecarError("DESKTOP_RESTORE_RUNTIME_CHANGED");
      this._selection = Object.freeze({ identity, archive });
      this._publish({ ...this._state, phase: "ready", archiveRelativePath: archive });
    } catch (error) {
      this._publish({ ...this._state, phase: "error",
        errorCode: error instanceof SidecarError ? safeCode(error.code, "DESKTOP_RESTORE_SELECTION_FAILED") : "DESKTOP_RESTORE_SELECTION_FAILED" });
    }
    return this._state;
  }

  start(request) {
    if (this.busy) throw new Error("DESKTOP_RESTORE_BUSY");
    if (!request || typeof request !== "object" || Array.isArray(request) || Object.keys(request).length !== 2 ||
        request.confirmWritersStopped !== true || !Number.isSafeInteger(request.selectionRevision) ||
        request.selectionRevision !== this._state.revision || !this._selection || this._state.phase !== "ready") {
      throw new Error("DESKTOP_RESTORE_REQUEST_INVALID");
    }
    const selection = this._selection;
    this._selection = null; // Consume confirmation before navigation or awaits.
    this._publish({ ...this._state, phase: "running" });
    this.settled = this._run(selection);
    return Object.freeze({ accepted: true });
  }

  async _run(selection) {
    let dispatched = false;
    try {
      if (selection.identity !== this._runtimeIdentity()) throw new SidecarError("DESKTOP_RESTORE_RUNTIME_CHANGED");
      await this._prepare();
      if (selection.identity !== this._runtimeIdentity()) throw new SidecarError("DESKTOP_RESTORE_RUNTIME_CHANGED");
      dispatched = true;
      const result = await this._restoreBackup({ confirmWritersStopped: true, archiveRelativePath: selection.archive });
      const restore = publicRestore(result?.restore);
      if (!restore?.success || !restore.complete || restore.recovery || result?.backendRestarted !== true) {
        throw new SidecarError("DESKTOP_RESTORE_RESULT_INVALID", { restore: restore?.recovery ? null : restore });
      }
      this._publish({ ...this._state, phase: "complete", restore, backendRestarted: true });
    } catch (error) {
      const known = error instanceof SidecarError;
      const errorCode = known ? safeCode(error.code, "DESKTOP_RESTORE_FAILED") : dispatched ? "DESKTOP_RESTORE_FAILED" : "DESKTOP_RESTORE_VIEW_UNAVAILABLE";
      const facts = known ? publicRestore(error.details?.restore) : null;
      this._publish({ ...this._state, phase: "error", errorCode,
        restore: (facts && !facts.recovery ? facts : null) || absentRestoreResult(dispatched, errorCode),
        backendRestarted: known && error.details?.backendRestarted === true,
        restartErrorCode: known ? safeCode(error.details?.restartErrorCode, "") : "" });
    }
    return this._state;
  }

  _publish(state) {
    this._state = Object.freeze(state);
    try { this._onState(this._state); } catch (_error) { /* closed/reloading renderer */ }
  }
}

module.exports = { DesktopRestoreFlow, managedArchiveSelection };
