"use strict";

const BACKUP_RESULT_FIELDS = Object.freeze([
  "archive_files", "archive_publication", "archive_relative_path", "archive_temporary_cleanup_confirmed",
  "error_code", "event", "full_source_validated", "last_file_publication", "last_file_temporary_cleanup_confirmed",
  "protocol_version", "snapshot_cleanup", "snapshot_namespace", "snapshot_relative_path", "staged_files", "success",
]);
const ROOTS = new Set(["applications", "career_vault", "documents", "jobs", "outputs", "resume_versions", "templates", "logs"]);
const OUTCOMES = new Set(["not_published", "published", "unknown"]);
const codePattern = /^[A-Z][A-Z0-9_]{0,127}$/;
const CLEANUP_FIELDS = ["last_removal", "removed_directories", "removed_files", "snapshot_removal", "status"];

function cleanupResult(value, payload) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const fields = Object.keys(value).sort();
  if (fields.length !== CLEANUP_FIELDS.length || fields.some((field, i) => field !== CLEANUP_FIELDS[i]) ||
      !["not_started", "incomplete", "complete", "unknown"].includes(value.status) ||
      !["not_removed", "removed", "unknown"].includes(value.last_removal) ||
      !["not_removed", "removed", "unknown"].includes(value.snapshot_removal) ||
      !Number.isSafeInteger(value.removed_files) || value.removed_files < 0 || value.removed_files > payload.staged_files ||
      !Number.isSafeInteger(value.removed_directories) || value.removed_directories < 0 || value.removed_directories > 50009) return null;
  if (value.status === "not_started" && (value.removed_files !== 0 || value.removed_directories !== 0 ||
      value.last_removal !== "not_removed" || value.snapshot_removal !== "not_removed")) return null;
  if (value.status === "unknown" && (value.removed_files !== 0 || value.removed_directories !== 0 ||
      value.last_removal !== "unknown" || value.snapshot_removal !== "unknown")) return null;
  if (["incomplete", "complete"].includes(value.status) && (payload.snapshot_namespace !== "created" ||
      payload.archive_publication !== "published" || !payload.full_source_validated)) return null;
  if (value.snapshot_removal === "removed" && (value.removed_files !== payload.staged_files ||
      value.removed_directories === 0 || value.last_removal !== "removed")) return null;
  if (value.status === "complete" && value.snapshot_removal !== "removed") return null;
  return Object.freeze({ status: value.status, removedFiles: value.removed_files, removedDirectories: value.removed_directories,
    lastRemoval: value.last_removal, snapshotRemoval: value.snapshot_removal });
}

function archivePath(value) {
  return typeof value === "string" && value.isWellFormed() && Buffer.byteLength(value) <= 4096 &&
    value.startsWith("backups/") && value.endsWith(".zip") && !/[\\:\0]/.test(value) &&
    value.split("/").every((part) => part && part !== "." && part !== "..");
}

function backupOptions(value) {
  if (!value || typeof value !== "object" || Array.isArray(value) ||
      value.confirmWritersStopped !== true || Object.keys(value).some((key) =>
        !["confirmWritersStopped", "archiveRelativePath", "includeRoots"].includes(key))) return null;
  const result = { confirm_writers_stopped: true };
  if (Object.hasOwn(value, "archiveRelativePath")) {
    if (!archivePath(value.archiveRelativePath)) return null;
    result.archive_relative_path = value.archiveRelativePath;
  }
  if (Object.hasOwn(value, "includeRoots")) {
    if (!Array.isArray(value.includeRoots) || value.includeRoots.length === 0 || value.includeRoots.length > 8 ||
        new Set(value.includeRoots).size !== value.includeRoots.length || value.includeRoots.some((root) => !ROOTS.has(root))) return null;
    result.include_roots = Object.freeze([...value.includeRoots]);
  }
  return Object.freeze(result);
}

function backupResult(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return null;
  const fields = Object.keys(payload).sort();
  if (fields.length !== BACKUP_RESULT_FIELDS.length || fields.some((field, index) => field !== BACKUP_RESULT_FIELDS[index]) ||
      payload.event !== "backup_create_result" || payload.protocol_version !== 1 ||
      ["success", "full_source_validated", "archive_temporary_cleanup_confirmed", "last_file_temporary_cleanup_confirmed"].some((key) => typeof payload[key] !== "boolean") ||
      ["archive_files", "staged_files"].some((key) => !Number.isSafeInteger(payload[key]) || payload[key] < 0 || payload[key] > 25000) ||
      !["not_created", "created", "unknown"].includes(payload.snapshot_namespace) ||
      !OUTCOMES.has(payload.archive_publication) || !OUTCOMES.has(payload.last_file_publication) ||
      typeof payload.error_code !== "string" || (payload.success ? payload.error_code !== "" : !codePattern.test(payload.error_code)) ||
      typeof payload.snapshot_relative_path !== "string" ||
      (payload.snapshot_relative_path !== "" && !/^\.career_hub_staging\/backups\/[a-f0-9]{32}$/.test(payload.snapshot_relative_path)) ||
      (payload.archive_relative_path !== "" && !archivePath(payload.archive_relative_path)) ||
      (payload.snapshot_namespace === "created" && !payload.snapshot_relative_path) ||
      (payload.archive_publication === "unknown" && payload.archive_temporary_cleanup_confirmed) ||
      (payload.last_file_publication === "unknown" && payload.last_file_temporary_cleanup_confirmed) ||
      (payload.archive_publication === "published" && (!payload.archive_relative_path || !payload.full_source_validated))) return null;
  const cleanup = cleanupResult(payload.snapshot_cleanup, payload);
  if (!cleanup) return null;
  if (payload.success && (cleanup.status !== "complete" || payload.snapshot_namespace !== "created" || payload.archive_publication !== "published" ||
      !payload.archive_temporary_cleanup_confirmed || !payload.last_file_temporary_cleanup_confirmed ||
      payload.last_file_publication === "unknown" || payload.archive_files > payload.staged_files)) return null;
  return Object.freeze({ success: payload.success, errorCode: payload.error_code,
    snapshotNamespace: payload.snapshot_namespace, snapshotRelativePath: payload.snapshot_relative_path,
    stagedFiles: payload.staged_files, lastFilePublication: payload.last_file_publication,
    lastFileTemporaryCleanupConfirmed: payload.last_file_temporary_cleanup_confirmed,
    archiveRelativePath: payload.archive_relative_path, archivePublication: payload.archive_publication,
    archiveTemporaryCleanupConfirmed: payload.archive_temporary_cleanup_confirmed,
    archiveFiles: payload.archive_files, fullSourceValidated: payload.full_source_validated, snapshotCleanup: cleanup });
}

// A missing result after dispatch cannot establish no-write facts. Counts/path
// are unreported defaults; UI must use the unknown outcome, not interpret 0 as
// proof that no files exist. Not-dispatched facts are only for host preflight.
function absentBackupResult(dispatched, errorCode) {
  return Object.freeze({ success: false, errorCode,
    snapshotNamespace: dispatched ? "unknown" : "not_created", snapshotRelativePath: "", stagedFiles: 0,
    lastFilePublication: dispatched ? "unknown" : "not_published", lastFileTemporaryCleanupConfirmed: !dispatched,
    archiveRelativePath: "", archivePublication: dispatched ? "unknown" : "not_published",
    archiveTemporaryCleanupConfirmed: !dispatched, archiveFiles: 0, fullSourceValidated: false,
    snapshotCleanup: Object.freeze({ status: dispatched ? "unknown" : "not_started", removedFiles: 0, removedDirectories: 0,
      lastRemoval: dispatched ? "unknown" : "not_removed", snapshotRemoval: dispatched ? "unknown" : "not_removed" }) });
}

module.exports = { BACKUP_RESULT_FIELDS, archivePath, backupOptions, backupResult, absentBackupResult };
