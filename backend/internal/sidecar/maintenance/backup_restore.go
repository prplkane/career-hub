package maintenance

import (
	"context"
	"errors"
	"time"

	"careerhub.local/backend/internal/homebackup"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sidecar/protocol"
)

type executeBackupRestore func(context.Context, protocol.BackupRestoreRequest) (homebackup.RestoreResult, error)

func executeBackupRestorePayload(ctx context.Context, payload map[string]any, execute executeBackupRestore) (record map[string]any, code string, status int) {
	request, err := protocol.ParseBackupRestoreMaintenance(payload, protocol.PathOptions{})
	if err != nil {
		return nil, requestErrorCode(err), 3
	}
	if ctx == nil || execute == nil {
		return nil, "SIDECAR_MAINTENANCE_FAILED", 4
	}
	defer func() {
		if recover() != nil {
			record, code, status = backupRestoreRecord(request.Command, homebackup.RestoreResult{}, "BACKUP_RESTORE_UNCONFIRMED")
		}
	}()
	// Copy/validation before and after activation is heavier than ZIP creation.
	// The supervisor owns a longer hard deadline, kill/reap and restart decision.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, err := execute(ctx, request)
	if err != nil {
		code = backupRestoreCode(err)
	}
	if code == "" && ctx.Err() != nil {
		code = "BACKUP_CANCELED"
	}
	return backupRestoreRecord(request.Command, out, code)
}

func backupRestoreRecord(command string, out homebackup.RestoreResult, code string) (map[string]any, string, int) {
	invalid := false
	publication := func(value runtimepaths.FilePublicationOutcome) string {
		switch value {
		case runtimepaths.FilePublished:
			return "published"
		case runtimepaths.FileNotPublished:
			return "not_published"
		default:
			return "unknown"
		}
	}
	stream := func(value runtimepaths.StreamPublication) (string, bool) {
		state := publication(value.Outcome)
		return state, state != "unknown" && value.CleanupConfirmed
	}
	namespace := func(value runtimepaths.DirectoryCreationOutcome) string {
		switch value {
		case runtimepaths.DirectoryCreated:
			return "created"
		case runtimepaths.DirectoryNotCreated:
			return "not_created"
		default:
			return "unknown"
		}
	}
	count := func(value, limit int) int {
		if value < 0 || value > limit {
			invalid = true
			return 0
		}
		return value
	}
	stage := func(created runtimepaths.DirectoryCreationOutcome, files int, last runtimepaths.StreamPublication) map[string]any {
		state, clean := stream(last)
		return map[string]any{"namespace": namespace(created), "copied_files": count(files, homebackup.MaxFiles), "last_file_publication": state, "temporary_cleanup_confirmed": clean}
	}
	validation := func(phase homebackup.RestoreValidationPhase) map[string]any {
		path := phase.SnapshotRelativePath
		if path != "" && !protocol.ValidBackupSnapshotPath(path) {
			path = ""
			invalid = true
		}
		out := stage(phase.Preparation.Namespace, phase.Preparation.CopiedFiles, phase.Preparation.LastFile)
		out["snapshot_relative_path"], out["validated"] = path, phase.Summary != nil
		cleanup, bad := restoreCleanupRecord(phase, out)
		// Cleanup validity affects operation success, not the separate commit fact.
		out["cleanup"] = cleanup
		if bad && code == "" {
			code = "BACKUP_RESTORE_UNCONFIRMED"
		}
		return out
	}
	id, stagePath, recoveryPath := out.RestoreID, out.StagingRelativePath, out.RecoveryRelativePath
	if id != "" && !protocol.ValidRestoreID(id) {
		id = ""
		invalid = true
	}
	if stagePath != "" && !protocol.ValidRestoreStagePath(stagePath, id) {
		stagePath = ""
		invalid = true
	}
	if recoveryPath != "" && !protocol.ValidRestoreRecoveryPath(recoveryPath, id) {
		recoveryPath = ""
		invalid = true
	}
	initial, final := validation(out.InitialValidation), validation(out.FinalValidation)
	archive := validation(out.ArchiveValidation)
	extracted := stage(out.Extracted.Namespace, out.Extracted.CopiedFiles, out.Extracted.LastFile)
	evidence, evidenceClean := stream(out.Journal.LastEvidence)
	journal, journalClean := stream(out.Journal.Journal)
	journalFields := map[string]any{"namespace": namespace(out.Journal.Namespace), "evidence_files": count(out.Journal.EvidenceFiles, 4), "last_evidence_publication": evidence, "evidence_temporary_cleanup_confirmed": evidenceClean, "publication": journal, "temporary_cleanup_confirmed": journalClean}
	lastMove := "unknown"
	switch out.Roots.LastMove {
	case runtimepaths.DirectoryMoveMoved:
		lastMove = "moved"
	case runtimepaths.DirectoryMoveNotMoved:
		lastMove = "not_moved"
	}
	rootFields := map[string]any{"completed_moves": count(out.Roots.CompletedMoves, 16), "last_move": lastMove, "recovery_directory_may_have_changed": out.Roots.RecoveryDirectoryMayHaveChanged || lastMove == "unknown", "complete": out.Roots.RootPhaseComplete}
	marker := publication(out.Finish.MarkerPublication)
	receipt, receiptClean := stream(out.Finish.Receipt)
	removal := "unknown"
	switch string(out.Finish.JournalRemoval) {
	case "not_removed":
		removal = "not_removed"
	case "removed":
		removal = "removed"
	}
	// A domain return without an error is not sufficient: require all facts that
	// prove terminal completion. Late errors still retain valid committed facts.
	validStage := func(v map[string]any) bool {
		return v["namespace"] == "created" && v["last_file_publication"] != "unknown" && v["temporary_cleanup_confirmed"] == true && (v["copied_files"] == 0 || v["last_file_publication"] == "published")
	}
	validFinal := validStage(final) && final["snapshot_relative_path"] != "" && final["validated"] == true
	complete := !invalid && id != "" && stagePath != "" && recoveryPath != "" && out.Finish.Complete && out.Finish.SemanticValidated && out.Roots.RootPhaseComplete && removal == "removed" && receipt == "published" && receiptClean && marker != "unknown" && lastMove != "unknown" && (out.Roots.CompletedMoves == 0 || lastMove == "moved") && validFinal
	if !complete {
		for _, phase := range []map[string]any{initial, archive, final} {
			cleanup := phase["cleanup"].(map[string]any)
			if cleanup["status"] == "complete" || cleanup["status"] == "incomplete" {
				phase["cleanup"], _ = restoreCleanupRecord(homebackup.RestoreValidationPhase{}, phase)
			}
		}
	}
	validArchive := archive["validated"] == true && (validStage(archive) && archive["snapshot_relative_path"] != "" || archive["namespace"] == "not_created" && archive["snapshot_relative_path"] == "" && archive["copied_files"] == 0 && archive["last_file_publication"] == "not_published" && archive["temporary_cleanup_confirmed"] == true)
	validForward := command == protocol.BackupRestoreRecoverCommand || validStage(initial) && initial["snapshot_relative_path"] != "" && initial["validated"] == true && validStage(extracted) && out.StagedValidation != nil && validArchive && journal == "published" && journalClean && journalFields["namespace"] == "created" && journalFields["evidence_files"] == 4 && evidence == "published" && evidenceClean
	cleanPhase := func(v map[string]any) bool { return v["cleanup"].(map[string]any)["status"] == "complete" }
	archiveCleanup := archive["cleanup"].(map[string]any)
	cleaned := cleanPhase(final) && (command == protocol.BackupRestoreRecoverCommand || cleanPhase(initial) && (cleanPhase(archive) || archive["namespace"] == "not_created" && archiveCleanup["status"] == "not_started" && archiveCleanup["files"] == 0))
	if code == "" && (invalid || id == "" || stagePath == "" || recoveryPath == "" || !complete || !validForward || !cleaned) {
		code = "BACKUP_RESTORE_UNCONFIRMED"
	}
	event := "backup_restore_result"
	if command == protocol.BackupRestoreRecoverCommand {
		event = "backup_restore_recovery_result"
	}
	// No absolute roots, keys, raw errors, database values, per-file manifests or
	// original/candidate filenames. Only fixed recovery namespace paths are sent.
	record := map[string]any{"event": event, "protocol_version": protocol.Version, "success": code == "", "error_code": code,
		"restore_id": id, "staging_relative_path": stagePath, "recovery_relative_path": recoveryPath,
		"initial_validation": initial, "extraction": extracted, "staged_validated": out.StagedValidation != nil, "archive_validation": archive, "journal": journalFields, "roots": rootFields, "final_validation": final,
		"marker_publication": marker, "semantic_validated": out.Finish.SemanticValidated, "receipt_publication": receipt, "receipt_temporary_cleanup_confirmed": receiptClean, "journal_removal": removal, "complete": complete}
	if code != "" {
		return record, code, 4
	}
	return record, "", 0
}

func restoreCleanupRecord(phase homebackup.RestoreValidationPhase, stage map[string]any) (map[string]any, bool) {
	c, files := phase.Cleanup, phase.CleanupFiles
	unknown := func() map[string]any {
		return map[string]any{"status": "unknown", "files": 0, "removed_files": 0, "removed_directories": 0, "last_removal": "unknown", "snapshot_removal": "unknown"}
	}
	if c == (homebackup.SnapshotCleanupResult{}) {
		return unknown(), files != 0
	}
	validOutcome := func(v runtimepaths.BackupRemovalOutcome) bool {
		return v == runtimepaths.BackupRemoved || v == runtimepaths.BackupNotRemoved || v == runtimepaths.BackupRemovalUnknown
	}
	if files < 0 || files > homebackup.MaxValidationCleanupFiles || c.RemovedFiles < 0 || c.RemovedFiles > files || c.RemovedDirectories < 0 || c.RemovedDirectories > homebackup.MaxValidationCleanupDirectories ||
		!validOutcome(c.LastRemoval) || !validOutcome(c.SnapshotRemoval) ||
		(!c.Attempted && (c.Complete || c.RemovedFiles != 0 || c.RemovedDirectories != 0 || c.LastRemoval != runtimepaths.BackupNotRemoved || c.SnapshotRemoval != runtimepaths.BackupNotRemoved)) ||
		(c.Attempted && (stage["namespace"] != "created" || phase.Summary == nil)) ||
		(c.SnapshotRemoval == runtimepaths.BackupRemoved && (c.RemovedFiles != files || c.RemovedDirectories == 0 || c.LastRemoval != runtimepaths.BackupRemoved)) ||
		(c.Complete && c.SnapshotRemoval != runtimepaths.BackupRemoved) {
		return unknown(), true
	}
	status := "not_started"
	if c.Complete {
		status = "complete"
	} else if c.Attempted {
		status = "incomplete"
	}
	return map[string]any{"status": status, "files": files, "removed_files": c.RemovedFiles, "removed_directories": c.RemovedDirectories,
		"last_removal": string(c.LastRemoval), "snapshot_removal": string(c.SnapshotRemoval)}, false
}

func backupRestoreCode(err error) string {
	var p *homebackup.Error
	if errors.As(err, &p) {
		switch p.Code {
		case "BACKUP_RESTORE_LEGACY_RECOVERY_REQUIRED", "BACKUP_RESTORE_COMMIT_UNCONFIRMED", "BACKUP_RESTORE_DESTINATION_UNAVAILABLE", "BACKUP_RESTORE_EVIDENCE_CHANGED", "BACKUP_RESTORE_EVIDENCE_CLOSE_FAILED", "BACKUP_RESTORE_EVIDENCE_INVALID", "BACKUP_RESTORE_EVIDENCE_UNAVAILABLE",
			"BACKUP_RESTORE_EXTRACTION_FAILED", "BACKUP_RESTORE_HOME_CHANGED", "BACKUP_RESTORE_JOURNAL_INVALID", "BACKUP_RESTORE_JOURNAL_UNCONFIRMED", "BACKUP_RESTORE_LAYOUT_CHANGED", "BACKUP_RESTORE_LAYOUT_INVALID", "BACKUP_RESTORE_MARKER_UNCONFIRMED",
			"BACKUP_RESTORE_MOVE_UNCONFIRMED", "BACKUP_RESTORE_PATH_LIMIT", "BACKUP_RESTORE_POST_COMMIT_CHECK_FAILED", "BACKUP_RESTORE_PUBLICATION_FAILED", "BACKUP_RESTORE_RECEIPT_CONFLICT", "BACKUP_RESTORE_RECEIPT_INVALID", "BACKUP_RESTORE_RECEIPT_UNCONFIRMED",
			"BACKUP_RESTORE_RECOVERY_REQUIRED", "BACKUP_RESTORE_RESERVATION_FAILED", "BACKUP_RESTORE_ROLLBACK_REQUIRED", "BACKUP_RESTORE_ROOT_PHASE_INCOMPLETE", "BACKUP_RESTORE_SCHEMA_MISMATCH", "BACKUP_RESTORE_SCOPE_MISMATCH", "BACKUP_RESTORE_SCOPE_UNAVAILABLE",
			"BACKUP_RESTORE_STAGE_CHANGED", "BACKUP_RESTORE_TREE_CHANGED", "BACKUP_RESTORE_TREE_INVALID", "BACKUP_RESTORE_VALIDATOR_REQUIRED":
			return p.Code
		}
	}
	code := backupCreateCode(err)
	if code == "BACKUP_CREATE_UNCONFIRMED" {
		return "BACKUP_RESTORE_UNCONFIRMED"
	}
	return code
}
