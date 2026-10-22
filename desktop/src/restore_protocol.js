"use strict";

const { archivePath } = require("./backup_protocol");
const RESTORE_RESULT_FIELDS = Object.freeze([
  "event", "protocol_version", "success", "error_code", "restore_id", "staging_relative_path", "recovery_relative_path",
  "initial_validation", "extraction", "staged_validated", "archive_validation", "journal", "roots", "final_validation", "marker_publication",
  "semantic_validated", "receipt_publication", "receipt_temporary_cleanup_confirmed", "journal_removal", "complete",
].sort());
const STAGE_FIELDS = ["namespace", "copied_files", "last_file_publication", "temporary_cleanup_confirmed"];
const VALIDATION_FIELDS = [...STAGE_FIELDS, "snapshot_relative_path", "validated", "cleanup"];
const CLEANUP_FIELDS = ["status", "files", "removed_files", "removed_directories", "last_removal", "snapshot_removal"];
const JOURNAL_FIELDS = ["namespace", "evidence_files", "last_evidence_publication", "evidence_temporary_cleanup_confirmed", "publication", "temporary_cleanup_confirmed"];
const ROOT_FIELDS = ["completed_moves", "last_move", "recovery_directory_may_have_changed", "complete"];
const outcomes = ["not_published", "published", "unknown"];
const namespaces = ["not_created", "created", "unknown"];
const codePattern = /^[A-Z][A-Z0-9_]{0,127}$/;
const snapshotPattern = /^\.career_hub_staging\/backups\/[a-f0-9]{32}$/;
const exact = (value, fields) => value && typeof value === "object" && !Array.isArray(value) &&
  Object.keys(value).length === fields.length && fields.every((field) => Object.hasOwn(value, field));
const bounded = (value, max) => Number.isSafeInteger(value) && value >= 0 && value <= max;
const stream = (state, clean) => outcomes.includes(state) && typeof clean === "boolean" && !(state === "unknown" && clean);
const isRestoreEvent = (value) => value === "backup_restore_result" || value === "backup_restore_recovery_result";

function parseCleanup(c, phase) {
  if (!exact(c, CLEANUP_FIELDS) || !["not_started", "incomplete", "complete", "unknown"].includes(c.status) ||
      !bounded(c.files, 25004) || !bounded(c.removed_files, c.files) || !bounded(c.removed_directories, 50025) ||
      !["not_removed", "removed", "unknown"].includes(c.last_removal) || !["not_removed", "removed", "unknown"].includes(c.snapshot_removal)) return null;
  if (c.status === "not_started" && (c.removed_files || c.removed_directories || c.last_removal !== "not_removed" || c.snapshot_removal !== "not_removed")) return null;
  if (c.status === "unknown" && (c.files || c.removed_files || c.removed_directories || c.last_removal !== "unknown" || c.snapshot_removal !== "unknown")) return null;
  if (["complete", "incomplete"].includes(c.status) && (phase.namespace !== "created" || !phase.validated)) return null;
  if (c.snapshot_removal === "removed" && (c.removed_files !== c.files || !c.removed_directories || c.last_removal !== "removed")) return null;
  if (c.status === "complete" && c.snapshot_removal !== "removed") return null;
  return Object.freeze({ status: c.status, files: c.files, removedFiles: c.removed_files, removedDirectories: c.removed_directories,
    lastRemoval: c.last_removal, snapshotRemoval: c.snapshot_removal });
}

function restoreOptions(value, recovery = false) {
  const fields = recovery ? ["confirmWritersStopped"] : ["confirmWritersStopped", "archiveRelativePath"];
  if (!exact(value, fields) || value.confirmWritersStopped !== true || (!recovery && !archivePath(value.archiveRelativePath))) return null;
  return Object.freeze(recovery ? { confirm_writers_stopped: true } :
    { confirm_writers_stopped: true, archive_relative_path: value.archiveRelativePath });
}

function parseStage(raw, validation) {
  if (!exact(raw, validation ? VALIDATION_FIELDS : STAGE_FIELDS) || !namespaces.includes(raw.namespace) ||
      !bounded(raw.copied_files, 25000) || !stream(raw.last_file_publication, raw.temporary_cleanup_confirmed)) return null;
  const out = { namespace: raw.namespace, copiedFiles: raw.copied_files,
    lastFilePublication: raw.last_file_publication, temporaryCleanupConfirmed: raw.temporary_cleanup_confirmed };
  if (validation) {
    if (typeof raw.validated !== "boolean" || typeof raw.snapshot_relative_path !== "string" ||
        (raw.snapshot_relative_path !== "" && !snapshotPattern.test(raw.snapshot_relative_path)) ||
        (raw.namespace === "created" && raw.snapshot_relative_path === "")) return null;
    out.snapshotRelativePath = raw.snapshot_relative_path;
    out.validated = raw.validated;
    out.cleanup = parseCleanup(raw.cleanup, raw);
    if (!out.cleanup) return null;
  }
  return Object.freeze(out);
}

function restoreResult(raw) {
  if (!exact(raw, RESTORE_RESULT_FIELDS) || !isRestoreEvent(raw.event) || raw.protocol_version !== 1 ||
      ["success", "staged_validated", "semantic_validated", "complete"].some((key) => typeof raw[key] !== "boolean") ||
      typeof raw.error_code !== "string" || (raw.success ? raw.error_code !== "" : !codePattern.test(raw.error_code)) ||
      typeof raw.restore_id !== "string" || (raw.restore_id !== "" && !/^[a-f0-9]{32}$/.test(raw.restore_id)) ||
      typeof raw.staging_relative_path !== "string" || typeof raw.recovery_relative_path !== "string" ||
      (raw.staging_relative_path !== "" && (!raw.restore_id || raw.staging_relative_path !== `.career_hub_staging/restores/${raw.restore_id}`)) ||
      (raw.recovery_relative_path !== "" && (!raw.restore_id || raw.recovery_relative_path !== `backups/restore-recovery/restore_${raw.restore_id}`)) ||
      !outcomes.includes(raw.marker_publication) || !stream(raw.receipt_publication, raw.receipt_temporary_cleanup_confirmed) ||
      !["not_removed", "removed", "unknown"].includes(raw.journal_removal)) return null;
  const initial = parseStage(raw.initial_validation, true), extracted = parseStage(raw.extraction, false), final = parseStage(raw.final_validation, true);
  const archive = parseStage(raw.archive_validation, true);
  const j = raw.journal, r = raw.roots;
  if (!initial || !extracted || !final || !archive || !exact(j, JOURNAL_FIELDS) || !exact(r, ROOT_FIELDS) ||
      !namespaces.includes(j.namespace) || !bounded(j.evidence_files, 4) ||
      !stream(j.last_evidence_publication, j.evidence_temporary_cleanup_confirmed) || !stream(j.publication, j.temporary_cleanup_confirmed) ||
      !bounded(r.completed_moves, 16) || !["not_moved", "moved", "unknown"].includes(r.last_move) ||
      typeof r.recovery_directory_may_have_changed !== "boolean" || typeof r.complete !== "boolean") return null;
  const validStage = (v) => v.namespace === "created" && v.lastFilePublication !== "unknown" && v.temporaryCleanupConfirmed &&
    (v.copiedFiles === 0 || v.lastFilePublication === "published");
  const validFinal = validStage(final) && final.snapshotRelativePath && final.validated;
  const validArchive = archive.validated && (validStage(archive) && archive.snapshotRelativePath ||
    archive.namespace === "not_created" && archive.snapshotRelativePath === "" && archive.copiedFiles === 0 &&
    archive.lastFilePublication === "not_published" && archive.temporaryCleanupConfirmed);
  const terminal = raw.restore_id && raw.staging_relative_path && raw.recovery_relative_path && raw.semantic_validated && r.complete &&
    r.last_move !== "unknown" && (r.completed_moves === 0 || r.last_move === "moved") && raw.marker_publication !== "unknown" &&
    raw.journal_removal === "removed" && raw.receipt_publication === "published" && raw.receipt_temporary_cleanup_confirmed && validFinal;
  const forward = raw.event === "backup_restore_recovery_result" ||
    validStage(initial) && initial.snapshotRelativePath && initial.validated && validStage(extracted) && raw.staged_validated && validArchive &&
    j.namespace === "created" && j.evidence_files === 4 && j.last_evidence_publication === "published" && j.evidence_temporary_cleanup_confirmed &&
    j.publication === "published" && j.temporary_cleanup_confirmed;
  const clean = v => v.cleanup.status === "complete";
  const cleaned = clean(final) && (raw.event === "backup_restore_recovery_result" || clean(initial) &&
    (clean(archive) || archive.namespace === "not_created" && archive.cleanup.status === "not_started" && archive.cleanup.files === 0));
  if ((!raw.complete && [initial, archive, final].some(v => ["complete", "incomplete"].includes(v.cleanup.status))) ||
      (raw.complete && !terminal) || (raw.success && (!raw.complete || !terminal || !forward || !cleaned))) return null;
  return Object.freeze({ success: raw.success, errorCode: raw.error_code, recovery: raw.event === "backup_restore_recovery_result",
    restoreId: raw.restore_id, stagingRelativePath: raw.staging_relative_path, recoveryRelativePath: raw.recovery_relative_path,
    initialValidation: initial, extraction: extracted, stagedValidated: raw.staged_validated, archiveValidation: archive,
    journal: Object.freeze({ namespace: j.namespace, evidenceFiles: j.evidence_files, lastEvidencePublication: j.last_evidence_publication,
      evidenceTemporaryCleanupConfirmed: j.evidence_temporary_cleanup_confirmed, publication: j.publication, temporaryCleanupConfirmed: j.temporary_cleanup_confirmed }),
    roots: Object.freeze({ completedMoves: r.completed_moves, lastMove: r.last_move,
      recoveryDirectoryMayHaveChanged: r.recovery_directory_may_have_changed, complete: r.complete }),
    finalValidation: final, markerPublication: raw.marker_publication, semanticValidated: raw.semantic_validated,
    receiptPublication: raw.receipt_publication, receiptTemporaryCleanupConfirmed: raw.receipt_temporary_cleanup_confirmed,
    journalRemoval: raw.journal_removal, complete: raw.complete });
}

// Missing child response after dispatch is unknown, never a no-write result.
// Counts and blank paths are unreported defaults, not evidence of absence.
function absentRestoreResult(dispatched, errorCode, recovery = false) {
  const publication = dispatched ? "unknown" : "not_published", namespace = dispatched ? "unknown" : "not_created";
  const stage = () => ({ namespace, copied_files: 0, last_file_publication: publication, temporary_cleanup_confirmed: !dispatched });
  const validation = () => ({ ...stage(), snapshot_relative_path: "", validated: false,
    cleanup: { status: dispatched ? "unknown" : "not_started", files: 0, removed_files: 0, removed_directories: 0,
      last_removal: dispatched ? "unknown" : "not_removed", snapshot_removal: dispatched ? "unknown" : "not_removed" } });
  return restoreResult({ event: recovery ? "backup_restore_recovery_result" : "backup_restore_result", protocol_version: 1,
    success: false, error_code: errorCode, restore_id: "", staging_relative_path: "", recovery_relative_path: "",
    initial_validation: validation(), extraction: stage(), staged_validated: false, archive_validation: validation(),
    journal: { namespace, evidence_files: 0, last_evidence_publication: publication, evidence_temporary_cleanup_confirmed: !dispatched,
      publication, temporary_cleanup_confirmed: !dispatched },
    roots: { completed_moves: 0, last_move: dispatched ? "unknown" : "not_moved", recovery_directory_may_have_changed: dispatched, complete: false },
    final_validation: validation(), marker_publication: publication, semantic_validated: false, receipt_publication: publication,
    receipt_temporary_cleanup_confirmed: !dispatched, journal_removal: dispatched ? "unknown" : "not_removed", complete: false });
}

// Revalidate the host-facing camelCase projection before exposing a result to a
// renderer. Never forward exception properties or runtime configuration.
function publicRestore(value) {
  if (!value || typeof value !== "object" || typeof value.recovery !== "boolean") return null;
  const stage = (v, validation = false) => v && ({ namespace: v.namespace, copied_files: v.copiedFiles,
    last_file_publication: v.lastFilePublication, temporary_cleanup_confirmed: v.temporaryCleanupConfirmed,
    ...(validation && { snapshot_relative_path: v.snapshotRelativePath, validated: v.validated,
      cleanup: v.cleanup && { status: v.cleanup.status, files: v.cleanup.files, removed_files: v.cleanup.removedFiles,
        removed_directories: v.cleanup.removedDirectories, last_removal: v.cleanup.lastRemoval, snapshot_removal: v.cleanup.snapshotRemoval } }) });
  const j = value.journal, r = value.roots;
  return restoreResult({ event: value.recovery ? "backup_restore_recovery_result" : "backup_restore_result", protocol_version: 1,
    success: value.success, error_code: value.errorCode, restore_id: value.restoreId,
    staging_relative_path: value.stagingRelativePath, recovery_relative_path: value.recoveryRelativePath,
    initial_validation: stage(value.initialValidation, true), extraction: stage(value.extraction), staged_validated: value.stagedValidated,
    archive_validation: stage(value.archiveValidation, true),
    journal: j && { namespace: j.namespace, evidence_files: j.evidenceFiles, last_evidence_publication: j.lastEvidencePublication,
      evidence_temporary_cleanup_confirmed: j.evidenceTemporaryCleanupConfirmed, publication: j.publication, temporary_cleanup_confirmed: j.temporaryCleanupConfirmed },
    roots: r && { completed_moves: r.completedMoves, last_move: r.lastMove, recovery_directory_may_have_changed: r.recoveryDirectoryMayHaveChanged, complete: r.complete },
    final_validation: stage(value.finalValidation, true), marker_publication: value.markerPublication, semantic_validated: value.semanticValidated,
    receipt_publication: value.receiptPublication, receipt_temporary_cleanup_confirmed: value.receiptTemporaryCleanupConfirmed,
    journal_removal: value.journalRemoval, complete: value.complete });
}

module.exports = { RESTORE_RESULT_FIELDS, isRestoreEvent, restoreOptions, restoreResult, absentRestoreResult, publicRestore };
