"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const { backupOptions, backupResult, absentBackupResult } = require("../src/backup_protocol");

function record(overrides = {}) {
  return { event: "backup_create_result", protocol_version: 1, success: true, error_code: "",
    snapshot_namespace: "created", snapshot_relative_path: `.career_hub_staging/backups/${"a".repeat(32)}`,
    staged_files: 2, last_file_publication: "published", last_file_temporary_cleanup_confirmed: true,
    archive_relative_path: "backups/created.zip", archive_publication: "published",
    archive_temporary_cleanup_confirmed: true, archive_files: 1, full_source_validated: true,
    snapshot_cleanup: { status: "complete", removed_files: overrides.staged_files ?? 2, removed_directories: 1,
      last_removal: "removed", snapshot_removal: "removed" }, ...overrides };
}

test("backup protocol has closed bounded facts and preserves failed publication", () => {
  assert.equal(backupResult(record()).success, true);
  const failed = backupResult(record({ success: false, error_code: "BACKUP_SOURCE_CLOSE_FAILED" }));
  assert.equal(failed.archivePublication, "published");
  assert.equal(Object.isFrozen(failed), true);
  assert.equal(backupResult(record({ archive_files: 0, staged_files: 0, last_file_publication: "not_published" })).success, true);
  for (const change of [{ secret: "private" }, { archive_files: -1 }, { staged_files: 25001 }, { archive_files: 0.5 },
    { archive_files: 3 }, { success: 1 }, { success: false }, { full_source_validated: false },
    { error_code: "ERROR" }, { success: false, error_code: "private prose" }, { archive_relative_path: undefined },
    { archive_relative_path: "C:/private.zip" }, { archive_relative_path: "backups/../private.zip" },
    { archive_relative_path: "backups/\ud800.zip" }, { archive_relative_path: `backups/${"x".repeat(4096)}.zip` },
    { snapshot_relative_path: ".career_hub_staging/backups/../" }, { snapshot_namespace: "maybe" },
    { archive_publication: "unknown" }, { last_file_publication: "unknown" }, { archive_temporary_cleanup_confirmed: false }]) {
    assert.equal(backupResult(record(change)), null, JSON.stringify(change));
  }
  assert.equal(absentBackupResult(true, "DESKTOP_SIDECAR_MAINTENANCE_TIMEOUT").archivePublication, "unknown");
  assert.equal(absentBackupResult(false, "DESKTOP_SIDECAR_STOP_TIMEOUT").archivePublication, "not_published");
});

test("backup selection cannot supply runtime authority and is defensively copied", () => {
  const input = { confirmWritersStopped: true, includeRoots: ["jobs", "logs"], archiveRelativePath: "backups/nested/a.zip" };
  const parsed = backupOptions(input);
  input.includeRoots[0] = "secrets";
  assert.deepEqual(parsed.include_roots, ["jobs", "logs"]);
  assert.equal(Object.isFrozen(parsed.include_roots), true);
  for (const value of [null, {}, { confirmWritersStopped: "true" }, { confirmWritersStopped: true, executablePath: "/other" },
    { confirmWritersStopped: true, includeRoots: [] }, { confirmWritersStopped: true, includeRoots: ["jobs", "jobs"] },
    { confirmWritersStopped: true, includeRoots: ["secrets"] }, { confirmWritersStopped: true, archiveRelativePath: "" },
    { confirmWritersStopped: true, archiveRelativePath: "backups/a.zip:ads" }]) assert.equal(backupOptions(value), null);
});

test("cleanup facts are required, closed and independent of archive publication", () => {
  const partial = { status: "incomplete", removed_files: 1, removed_directories: 0,
    last_removal: "unknown", snapshot_removal: "not_removed" };
  const parsed = backupResult(record({ success: false, error_code: "BACKUP_CLEANUP_UNCONFIRMED", snapshot_cleanup: partial }));
  assert.equal(parsed.archivePublication, "published");
  assert.equal(parsed.snapshotCleanup.removedFiles, 1);
  assert.equal(parsed.snapshotCleanup.lastRemoval, "unknown");
  assert.equal(Object.isFrozen(parsed.snapshotCleanup), true);
  partial.removed_files = 0;
  assert.equal(parsed.snapshotCleanup.removedFiles, 1);
  const late = backupResult(record({ success: false, error_code: "BACKUP_CANCELED",
    snapshot_cleanup: { ...record().snapshot_cleanup, status: "incomplete" } }));
  assert.equal(late.snapshotCleanup.snapshotRemoval, "removed");
  assert.equal(late.snapshotCleanup.status, "incomplete");
  for (const snapshot_cleanup of [undefined, null, {}, { ...partial, secret: "private" },
    { ...partial, removed_files: -1 }, { ...partial, removed_directories: 50010 },
    { ...partial, removed_files: 3 }, { ...partial, removed_files: 0.5 },
    { ...partial, snapshot_removal: "removed" }, { ...partial, status: "complete" },
    { ...partial, status: "not_started" }, { ...partial, status: "unknown" }]) {
    assert.equal(backupResult(record({ success: false, error_code: "BACKUP_CLEANUP_UNCONFIRMED", snapshot_cleanup })), null);
  }
  assert.equal(backupResult(record({ snapshot_cleanup: partial })), null, "partial cleanup cannot claim whole-operation success");
  assert.equal(absentBackupResult(true, "ERROR").snapshotCleanup.status, "unknown");
  assert.equal(absentBackupResult(false, "ERROR").snapshotCleanup.status, "not_started");
});
