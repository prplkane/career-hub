package homebackup

import (
	"bytes"
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

type restoreRemovalOutcome string

const (
	restoreNotRemoved     restoreRemovalOutcome = "not_removed"
	restoreRemoved        restoreRemovalOutcome = "removed"
	restoreRemovalUnknown restoreRemovalOutcome = "unknown"
)

type restoreFinishResult struct {
	MarkerPublication runtimepaths.FilePublicationOutcome
	SemanticValidated bool
	Receipt           runtimepaths.StreamPublication
	JournalRemoval    restoreRemovalOutcome
	Complete          bool
}

type restoreReceipt struct {
	Version         int    `json:"version"`
	RestoreID       string `json:"restore_id"`
	RootFingerprint string `json:"root_fingerprint"`
	JournalSHA256   string `json:"journal_sha256"`
	MarkerSHA256    string `json:"marker_sha256"`
	State           string `json:"state"`
}

type restoreFinishOperations struct {
	checkpoint func(string) *Error
	replace    func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error)
	publish    func(context.Context, *runtimepaths.Root, string, []byte) (runtimepaths.StreamPublication, *Error)
	remove     func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
}

// finishRestore is the internal transaction terminator, not a public command.
// The admitted owner supplies a mandatory read-only full semantic validator;
// physical hashes alone cannot authorize commit. No default validator or key
// creation/discovery is provided here. Call in a quiescent owned maintenance
// process after root activation or rollback. It never moves/cleans any tree.
//
// The journal's exact-byte removal is the commit boundary. A receipt is audit
// evidence, never replay authority or proof that the journal was removed. Late
// errors retain known/unknown marker, receipt and journal-removal facts. A
// failed boolean native call has an unknown outcome unless success was observed;
// there is no automatic retry/rollback inside this component.
func finishRestore(ctx context.Context, root *runtimepaths.Root, channel string, confirmWritersStopped, rollback bool,
	validate func(context.Context, *runtimepaths.Root, datahome.Info) *Error, ops restoreFinishOperations) (result restoreFinishResult, problem *Error) {
	result.MarkerPublication = runtimepaths.FileNotPublished
	result.Receipt = runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	result.JournalRemoval = restoreNotRemoved
	if ctx == nil || ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	if !confirmWritersStopped {
		return result, failure("BACKUP_WRITER_CONFIRMATION_REQUIRED")
	}
	if validate == nil {
		return result, failure("BACKUP_RESTORE_VALIDATOR_REQUIRED")
	}
	j, evidence, p := readRestoreJournal(ctx, root, channel)
	if p != nil {
		return result, p
	}
	bound, p := encodeRestoreJournal(j, j.RootFingerprint)
	if p != nil {
		return result, p
	}
	target, home, state, receiptName := evidence.afterMarker, j.AfterHome, "restored", "completed.json"
	if rollback {
		target, home, state, receiptName = evidence.beforeMarker, j.BeforeHome, "rolled_back", "recovered.json"
	}
	check := func() *Error {
		current, _, p := readRestoreJournal(ctx, root, channel)
		if p != nil {
			return p
		}
		raw, p := encodeRestoreJournal(current, j.RootFingerprint)
		if p != nil || !bytes.Equal(raw, bound) {
			return failure("BACKUP_RESTORE_EVIDENCE_CHANGED")
		}
		return nil
	}
	checkpoint := func(stage string) *Error {
		if ops.checkpoint != nil {
			if p := ops.checkpoint(stage); p != nil {
				return p
			}
		}
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		return nil
	}
	if p := checkTerminalRestoreTrees(ctx, root, j, evidence, rollback); p != nil {
		return result, p
	}
	if p := checkpoint("before_marker"); p != nil {
		return result, p
	}
	if p := check(); p != nil {
		return result, p
	}
	current, present, q := root.ReadObjectFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
	if q != nil || !present || (!bytes.Equal(current, evidence.beforeMarker) && !bytes.Equal(current, evidence.afterMarker)) {
		return result, failure("BACKUP_RESTORE_HOME_CHANGED")
	}
	if !bytes.Equal(current, target) {
		if ops.replace == nil {
			ops.replace = (*runtimepaths.Root).ReplaceFile
		}
		result.MarkerPublication = runtimepaths.FilePublicationUnknown
		committed, q := ops.replace(root, datahome.MarkerPath, current, target)
		if committed {
			result.MarkerPublication = runtimepaths.FilePublished
		}
		if q != nil || !committed {
			return result, failure("BACKUP_RESTORE_MARKER_UNCONFIRMED")
		}
	}
	if p := checkpoint("after_marker"); p != nil {
		return result, p
	}
	if p := check(); p != nil {
		return result, p
	}
	marker, present, q := root.ReadObjectFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
	if q != nil || !present || !bytes.Equal(marker, target) {
		return result, failure("BACKUP_RESTORE_HOME_CHANGED")
	}
	if p := validate(ctx, root, home); p != nil {
		return result, p
	}
	result.SemanticValidated = true
	if p := checkpoint("after_validation"); p != nil {
		return result, p
	}
	if p := check(); p != nil {
		return result, p
	}
	receipt, err := json.Marshal(restoreReceipt{2, j.RestoreID, j.RootFingerprint, restoreDigest(bound), restoreDigest(target), state})
	if err != nil {
		return result, failure("BACKUP_RESTORE_RECEIPT_INVALID")
	}
	path := j.recoveryPath() + "/" + receiptName
	if existing, present, q := root.ReadObjectFile(path, maxRestoreJournalBytes); q != nil {
		return result, failure("BACKUP_RESTORE_RECEIPT_INVALID")
	} else if present {
		if !bytes.Equal(existing, receipt) {
			return result, failure("BACKUP_RESTORE_RECEIPT_CONFLICT")
		}
		result.Receipt = runtimepaths.StreamPublication{Outcome: runtimepaths.FilePublished, CleanupConfirmed: true}
	} else {
		if ops.publish == nil {
			ops.publish = publishRestoreBytes
		}
		result.Receipt, p = ops.publish(ctx, root, path, receipt)
		if p != nil {
			return result, p
		}
		if result.Receipt.Outcome != runtimepaths.FilePublished {
			return result, failure("BACKUP_RESTORE_RECEIPT_UNCONFIRMED")
		}
	}
	if p := checkpoint("after_receipt"); p != nil {
		return result, p
	}
	// Keep the receipt immutable through commit. Evidence/marker reads must be
	// released before their own replace/remove operations; do not hold a journal
	// read lease while trying to unlink that journal on Windows.
	receiptLease, q := root.OpenBackupRead(ctx, path, maxRestoreJournalBytes)
	if q != nil {
		return result, failure("BACKUP_RESTORE_RECEIPT_INVALID")
	}
	defer func() {
		if receiptLease.Close() != nil {
			problem = failure("BACKUP_RESTORE_EVIDENCE_CLOSE_FAILED")
		}
	}()
	observed, err := readRestoreEvidenceBytes(receiptLease, receiptLease.Size())
	if err != nil || !bytes.Equal(observed, receipt) {
		return result, failure("BACKUP_RESTORE_RECEIPT_INVALID")
	}
	if p := checkpoint("before_commit"); p != nil {
		return result, p
	}
	if p := check(); p != nil {
		return result, p
	}
	if p := checkTerminalRestoreTrees(ctx, root, j, evidence, rollback); p != nil {
		return result, p
	}
	marker, present, q = root.ReadObjectFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
	if q != nil || !present || !bytes.Equal(marker, target) || receiptLease.Check() != nil {
		return result, failure("BACKUP_RESTORE_HOME_CHANGED")
	}
	if ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	if ops.remove == nil {
		ops.remove = (*runtimepaths.Root).RemoveFile
	}
	result.JournalRemoval = restoreRemovalUnknown
	removed, q := ops.remove(root, datahome.RestoreRecoveryPath, bound)
	if removed {
		result.JournalRemoval = restoreRemoved
	}
	if q != nil || !removed {
		return result, failure("BACKUP_RESTORE_COMMIT_UNCONFIRMED")
	}
	if p := checkpoint("after_commit"); p != nil {
		return result, p
	}
	scope, q := root.CredentialScope()
	classification, admissionProblem := datahome.ClassifyOwnedRecovery(root, &channel)
	if q != nil || scope.Fingerprint() != j.RootFingerprint || admissionProblem != nil || classification.Cardinality != datahome.RecoveryNone {
		return result, failure("BACKUP_RESTORE_POST_COMMIT_CHECK_FAILED")
	}
	marker, present, q = root.ReadObjectFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
	if q != nil || !present || !bytes.Equal(marker, target) {
		return result, failure("BACKUP_RESTORE_POST_COMMIT_CHECK_FAILED")
	}
	result.Complete = true
	return result, nil
}

func checkTerminalRestoreTrees(ctx context.Context, root *runtimepaths.Root, j restoreJournal, evidence restoreEvidence, rollback bool) *Error {
	layout, p := observeRestoreLayout(root, j)
	if p != nil {
		return p
	}
	var before, after Manifest
	if !restoreCanonicalJSON(evidence.beforeManifest, &before) || !restoreCanonicalJSON(evidence.afterManifest, &after) {
		return failure("BACKUP_RESTORE_EVIDENCE_INVALID")
	}
	for _, item := range j.Roots {
		state := layout[item.Name]
		if !validRestorePlacement(item.HadActive, state) {
			return failure("BACKUP_RESTORE_LAYOUT_INVALID")
		}
		if rollback {
			if state[restoreActive] != item.HadActive || state[restoreOriginal] || state[restoreStaged] == state[restoreCandidate] {
				return failure("BACKUP_RESTORE_ROOT_PHASE_INCOMPLETE")
			}
		} else if state != (restorePlacement{true, false, item.HadActive, false}) {
			return failure("BACKUP_RESTORE_ROOT_PHASE_INCOMPLETE")
		}
	}
	for _, item := range j.Roots {
		state := layout[item.Name]
		for position := restoreActive; position <= restoreCandidate; position++ {
			if !state[position] {
				continue
			}
			files := after.Files
			if restorePositionIsBefore(item, state, position) {
				files = before.Files
			}
			path := restoreRootPath(j, item.Name, position)
			if p := verifyRestoreTree(ctx, root, path, item.Name, files, []string{path}); p != nil {
				return p
			}
		}
	}
	return nil
}
