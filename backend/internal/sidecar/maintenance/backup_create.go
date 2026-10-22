package maintenance

import (
	"context"
	"errors"
	"time"

	"careerhub.local/backend/internal/homebackup"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/startup"
)

type executeBackupCreate func(context.Context, protocol.BackupCreateRequest) (homebackup.CreationResult, error)

func executeBackupCreatePayload(ctx context.Context, payload map[string]any, execute executeBackupCreate) (record map[string]any, code string, status int) {
	request, err := protocol.ParseBackupCreateMaintenance(payload, protocol.PathOptions{})
	if err != nil {
		return nil, requestErrorCode(err), 3
	}
	if ctx == nil || execute == nil {
		return nil, "SIDECAR_MAINTENANCE_FAILED", 4
	}
	// A panic after owner dispatch is not evidence that publication did not occur.
	// Emit unknown facts, no native panic prose, and never retry within this child.
	defer func() {
		if recover() != nil {
			record, code, status = backupCreateRecord(homebackup.CreationResult{}, "BACKUP_CREATE_UNCONFIRMED")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := execute(ctx, request)
	if err != nil {
		code = backupCreateCode(err)
	}
	if code == "" && ctx.Err() != nil {
		code = "BACKUP_CANCELED"
	}
	return backupCreateRecord(out, code)
}

func backupCreateRecord(out homebackup.CreationResult, code string) (map[string]any, string, int) {
	namespace := "unknown"
	switch out.SnapshotNamespace {
	case runtimepaths.DirectoryCreated:
		namespace = "created"
	case runtimepaths.DirectoryNotCreated:
		namespace = "not_created"
	}
	publication := func(value runtimepaths.StreamPublication) (string, bool) {
		switch value.Outcome {
		case runtimepaths.FilePublished:
			return "published", value.CleanupConfirmed
		case runtimepaths.FileNotPublished:
			return "not_published", value.CleanupConfirmed
		default:
			return "unknown", false
		}
	}
	archive, archiveCleanup := publication(out.ArchivePublication)
	lastFile, lastCleanup := publication(out.LastStagedFile)
	snapshotPath, archivePath := out.SnapshotRelativePath, out.ArchiveRelativePath
	invalid := false
	if snapshotPath != "" && !protocol.ValidBackupSnapshotPath(snapshotPath) {
		snapshotPath, invalid = "", true
	}
	if archivePath != "" && !protocol.ValidBackupArchivePath(archivePath) {
		archivePath, invalid = "", true
	}
	if out.StagedFiles < 0 || out.StagedFiles > homebackup.MaxFiles {
		out.StagedFiles, invalid = 0, true
	}
	if out.ArchiveFiles < 0 || out.ArchiveFiles > homebackup.MaxFiles {
		out.ArchiveFiles, invalid = 0, true
	}
	cleanup, cleanupInvalid := backupSnapshotCleanupRecord(out)
	if code == "" && (invalid || cleanupInvalid || cleanup["status"] != "complete" || namespace != "created" || archive != "published" || !archiveCleanup || !lastCleanup || lastFile == "unknown" || snapshotPath == "" || archivePath == "" || out.FullSourceValidation == nil || out.ArchiveFiles > out.StagedFiles) {
		code = "BACKUP_CREATE_UNCONFIRMED"
	}
	// No full source root, document text, credential target or raw exception text.
	// Relative archive/snapshot paths are necessary for explicit local recovery.
	record := map[string]any{"event": "backup_create_result", "protocol_version": protocol.Version,
		"success": code == "", "error_code": code, "snapshot_namespace": namespace, "snapshot_relative_path": snapshotPath,
		"staged_files": out.StagedFiles, "last_file_publication": lastFile, "last_file_temporary_cleanup_confirmed": lastCleanup,
		"archive_relative_path": archivePath, "archive_publication": archive, "archive_temporary_cleanup_confirmed": archiveCleanup,
		"archive_files": out.ArchiveFiles, "full_source_validated": out.FullSourceValidation != nil, "snapshot_cleanup": cleanup}
	if code != "" {
		return record, code, 4
	}
	return record, "", 0
}

func backupSnapshotCleanupRecord(out homebackup.CreationResult) (map[string]any, bool) {
	c := out.SnapshotCleanup
	unknown := func() map[string]any {
		return map[string]any{"status": "unknown", "removed_files": 0, "removed_directories": 0, "last_removal": "unknown", "snapshot_removal": "unknown"}
	}
	if c == (homebackup.SnapshotCleanupResult{}) {
		return unknown(), false
	}
	validOutcome := func(v runtimepaths.BackupRemovalOutcome) bool {
		return v == runtimepaths.BackupRemoved || v == runtimepaths.BackupNotRemoved || v == runtimepaths.BackupRemovalUnknown
	}
	if !validOutcome(c.LastRemoval) || !validOutcome(c.SnapshotRemoval) || c.RemovedFiles < 0 || c.RemovedFiles > out.StagedFiles ||
		c.RemovedDirectories < 0 || c.RemovedDirectories > 2*homebackup.MaxFiles+9 ||
		(!c.Attempted && (c.Complete || c.RemovedFiles != 0 || c.RemovedDirectories != 0 || c.LastRemoval != runtimepaths.BackupNotRemoved || c.SnapshotRemoval != runtimepaths.BackupNotRemoved)) ||
		(c.Attempted && (out.SnapshotNamespace != runtimepaths.DirectoryCreated || out.ArchivePublication.Outcome != runtimepaths.FilePublished || out.FullSourceValidation == nil)) ||
		(c.SnapshotRemoval == runtimepaths.BackupRemoved && (c.RemovedFiles != out.StagedFiles || c.RemovedDirectories == 0 || c.LastRemoval != runtimepaths.BackupRemoved)) ||
		(c.Complete && c.SnapshotRemoval != runtimepaths.BackupRemoved) {
		return unknown(), true
	}
	status := "not_started"
	if c.Complete {
		status = "complete"
	} else if c.Attempted {
		status = "incomplete"
	}
	return map[string]any{"status": status, "removed_files": c.RemovedFiles, "removed_directories": c.RemovedDirectories,
		"last_removal": string(c.LastRemoval), "snapshot_removal": string(c.SnapshotRemoval)}, false
}

func backupCreateCode(err error) string {
	var admission *startup.Error
	if errors.As(err, &admission) && (allowedStartupCode(admission.Code) || admission.Code == "SIDECAR_SCHEMA_INITIALIZATION_FAILED" || admission.Code == "SIDECAR_STARTUP_FAILED") {
		return admission.Code
	}
	var problem *homebackup.Error
	if !errors.As(err, &problem) {
		return "BACKUP_CREATE_UNCONFIRMED"
	}
	if allowedStartupCode(problem.Code) {
		return problem.Code
	}
	switch problem.Code {
	case "BACKUP_ARCHIVE_INVALID", "BACKUP_ARCHIVE_WRITE_FAILED", "BACKUP_CANCELED", "BACKUP_CHANNEL_INVALID",
		"BACKUP_CLEANUP_ARCHIVE_UNAVAILABLE", "BACKUP_CLEANUP_ARCHIVE_CHANGED", "BACKUP_CLEANUP_RECOVERY_REQUIRED",
		"BACKUP_CLEANUP_SCOPE_UNAVAILABLE", "BACKUP_CLEANUP_SCOPE_CHANGED", "BACKUP_CLEANUP_TARGET_INVALID",
		"BACKUP_CLEANUP_WITNESS_INVALID", "BACKUP_CLEANUP_TREE_CHANGED", "BACKUP_CLEANUP_PERMIT_INVALID", "BACKUP_CLEANUP_UNCONFIRMED",
		"BACKUP_DATABASE_BUSY", "BACKUP_DATABASE_CLEANUP_FAILED", "BACKUP_DATABASE_COPY_FAILED", "BACKUP_DATABASE_NOT_STANDALONE",
		"BACKUP_DATABASE_OPEN_FAILED", "BACKUP_DATABASE_ORPHANED_FILES", "BACKUP_DATABASE_PATH_INVALID", "BACKUP_DATABASE_ROLLBACK_FAILED",
		"BACKUP_DATABASE_SCHEMA_INVALID", "BACKUP_DATABASE_STAGE_FAILED", "BACKUP_DATABASE_VERIFY_FAILED",
		"BACKUP_DESTINATION_EXISTS", "BACKUP_DESTINATION_INVALID", "BACKUP_DESTINATION_UNAVAILABLE",
		"BACKUP_DOCUMENT_AUTHENTICATION_FAILED", "BACKUP_DOCUMENT_CONTENT_INVALID", "BACKUP_DOCUMENT_IDENTITY_INVALID",
		"BACKUP_DOCUMENT_KEY_STATE_MISSING", "BACKUP_DOCUMENT_KEY_UNAVAILABLE", "BACKUP_DOCUMENT_MIGRATION_REQUIRED", "BACKUP_DOCUMENT_STORAGE_INVALID",
		"BACKUP_ENTRY_INVALID", "BACKUP_FILE_LIMIT", "BACKUP_FORMAT_UNSUPPORTED", "BACKUP_HOME_FORMAT_UNSUPPORTED", "BACKUP_HOME_UNAVAILABLE",
		"BACKUP_ID_UNAVAILABLE", "BACKUP_INVENTORY_LIMIT", "BACKUP_JOB_DUPLICATE", "BACKUP_JOB_INVALID", "BACKUP_MANIFEST_INVALID",
		"BACKUP_MEMBERSHIP_INVALID", "BACKUP_METADATA_LIMIT", "BACKUP_OWNERSHIP_REQUIRED", "BACKUP_PATH_COLLISION", "BACKUP_PATH_INVALID",
		"BACKUP_PAYLOAD_CHANGED", "BACKUP_PAYLOAD_INVALID", "BACKUP_PAYLOAD_LIMIT", "BACKUP_PAYLOAD_PATH_INVALID", "BACKUP_PUBLICATION_FAILED",
		"BACKUP_RESUME_DATABASE_INVALID", "BACKUP_RESUME_REFERENCE_INVALID", "BACKUP_SCHEMA_UNSUPPORTED", "BACKUP_SELECTION_INVALID", "BACKUP_SIZE_LIMIT",
		"BACKUP_SNAPSHOT_CHANGED", "BACKUP_SNAPSHOT_CLOSE_FAILED", "BACKUP_SNAPSHOT_INVALID", "BACKUP_SNAPSHOT_RESERVATION_FAILED", "BACKUP_SNAPSHOT_UNAVAILABLE",
		"BACKUP_SOURCE_CHANGED", "BACKUP_SOURCE_CLOSE_FAILED", "BACKUP_SOURCE_COPY_FAILED", "BACKUP_SOURCE_UNAVAILABLE", "BACKUP_TOTAL_LIMIT",
		"BACKUP_VAULT_INCOMPLETE", "BACKUP_VAULT_INVALID", "BACKUP_WRITER_CONFIRMATION_REQUIRED":
		return problem.Code
	}
	return "BACKUP_CREATE_UNCONFIRMED"
}
