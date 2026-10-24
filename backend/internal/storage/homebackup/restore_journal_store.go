package homebackup

import (
	"bytes"
	"context"
	"io"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type restoreJournalPublication struct {
	Namespace     runtimepaths.DirectoryCreationOutcome
	EvidenceFiles int
	LastEvidence  runtimepaths.StreamPublication
	Journal       runtimepaths.StreamPublication
}

type restoreJournalOperations struct {
	afterEvidence func() *Error // Internal fault seam, never a product callback.
	publish       func(context.Context, *runtimepaths.Root, string, []byte) (runtimepaths.StreamPublication, *Error)
}

// publishRestoreJournal owns evidence publication ONLY. The eventual restore
// owner must first validate the complete destination and selected staged data,
// stop writers, and release the staging reservation before moving roots. This
// helper never activates, rolls back, removes files, or creates credentials.
// A published/unknown journal means restart must stop for explicit recovery;
// failure does not revoke that fact. Unpublished preparation is retained too.
func publishRestoreJournal(ctx context.Context, root *runtimepaths.Root, j restoreJournal, evidence restoreEvidence, ops restoreJournalOperations) (result restoreJournalPublication, problem *Error) {
	result.Namespace = runtimepaths.DirectoryNotCreated
	result.LastEvidence = runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	result.Journal = result.LastEvidence
	if ctx == nil || ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	if root == nil || root.RequireProcessOwnership() != nil {
		return result, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	scope, pathProblem := root.CredentialScope()
	if pathProblem != nil {
		return result, failure("BACKUP_RESTORE_SCOPE_UNAVAILABLE")
	}
	raw, p := encodeRestoreJournal(j, scope.Fingerprint())
	if p != nil {
		return result, p
	}
	if p = validateRestoreEvidence(j, evidence); p != nil {
		return result, p
	}
	marker, pathProblem := root.OpenBackupRead(ctx, datahome.MarkerPath, datahome.MaxMarkerBytes)
	if pathProblem != nil {
		return result, failure("BACKUP_HOME_UNAVAILABLE")
	}
	defer func() {
		if marker.Close() != nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	markerBytes, err := readRestoreEvidenceBytes(marker, marker.Size())
	if err != nil || !bytes.Equal(markerBytes, evidence.beforeMarker) {
		return result, failure("BACKUP_RESTORE_HOME_CHANGED")
	}
	check := func() *Error {
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		observed, q := root.CredentialScope()
		if q != nil || !observed.Equal(scope) || marker.Check() != nil {
			return failure("BACKUP_RESTORE_HOME_CHANGED")
		}
		classification, admissionProblem := datahome.ClassifyOwnedRecovery(root, &j.BeforeHome.Channel)
		if admissionProblem != nil || classification.Cardinality != datahome.RecoveryNone || classification.Marker != datahome.RecoveryMarkerInitialized {
			return failure("BACKUP_RESTORE_RECOVERY_REQUIRED")
		}
		for _, item := range j.Roots {
			_, present, q := root.ListDirectoryEntries(item.Name)
			if q != nil || present != item.HadActive {
				return failure("BACKUP_RESTORE_HOME_CHANGED")
			}
			_, present, q = root.ListDirectoryEntries(j.stagePath() + "/" + item.Name)
			if q != nil || !present {
				return failure("BACKUP_RESTORE_STAGE_CHANGED")
			}
		}
		return nil
	}
	if p = check(); p != nil {
		return result, p
	}
	if _, pathProblem = root.EnsureDirectory("backups/restore-recovery"); pathProblem != nil {
		return result, failure("BACKUP_RESTORE_EVIDENCE_UNAVAILABLE")
	}
	reservation, outcome, pathProblem := root.ReserveDirectory(j.recoveryPath())
	result.Namespace = outcome
	if pathProblem != nil {
		return result, failure("BACKUP_RESTORE_RESERVATION_FAILED")
	}
	defer func() {
		if reservation.Close() != nil {
			problem = failure("BACKUP_RESTORE_EVIDENCE_CLOSE_FAILED")
		}
	}()
	for _, item := range restoreEvidenceFiles(evidence) {
		result.LastEvidence, p = publishRestoreBytes(ctx, root, j.recoveryPath()+"/"+item.name, item.raw)
		if result.LastEvidence.Outcome == runtimepaths.FilePublished {
			result.EvidenceFiles++
		}
		if p != nil {
			return result, p
		}
	}
	if ops.afterEvidence != nil {
		if p = ops.afterEvidence(); p != nil {
			return result, p
		}
	}
	var evidenceLeases []*runtimepaths.BackupRead
	defer func() {
		for _, lease := range evidenceLeases {
			if lease.Close() != nil {
				problem = failure("BACKUP_RESTORE_EVIDENCE_CLOSE_FAILED")
			}
		}
	}()
	// Pin the verified evidence until the journal has been published. Namespace
	// reservation alone does not prevent writes to files inside that directory.
	for _, item := range restoreEvidenceFiles(evidence) {
		lease, q := root.OpenBackupRead(ctx, j.recoveryPath()+"/"+item.name, int64(len(item.raw)))
		if q != nil {
			return result, failure("BACKUP_RESTORE_EVIDENCE_CHANGED")
		}
		evidenceLeases = append(evidenceLeases, lease)
		observed, err := readRestoreEvidenceBytes(lease, lease.Size())
		if err != nil || !bytes.Equal(observed, item.raw) {
			return result, failure("BACKUP_RESTORE_EVIDENCE_CHANGED")
		}
	}
	if p = check(); p != nil {
		return result, p
	}
	if reservation.Check() != nil {
		return result, failure("BACKUP_RESTORE_EVIDENCE_CHANGED")
	}
	if ops.publish == nil {
		ops.publish = publishRestoreBytes
	}
	result.Journal, p = ops.publish(ctx, root, datahome.RestoreRecoveryPath, raw)
	if p != nil {
		return result, p
	}
	if result.Journal.Outcome != runtimepaths.FilePublished {
		return result, failure("BACKUP_RESTORE_JOURNAL_UNCONFIRMED")
	}
	if ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	observed, pathProblem := root.CredentialScope()
	if pathProblem != nil || !observed.Equal(scope) || marker.Check() != nil || reservation.Check() != nil {
		return result, failure("BACKUP_RESTORE_HOME_CHANGED")
	}
	classification, admissionProblem := datahome.ClassifyOwnedRecovery(root, &j.BeforeHome.Channel)
	if admissionProblem != nil || classification.Cardinality != datahome.RecoverySingle || classification.Kind != datahome.RecoveryRestore {
		return result, failure("BACKUP_RESTORE_RECOVERY_REQUIRED")
	}
	return result, nil
}

type restoreEvidenceFile struct {
	name string
	raw  []byte
}

func restoreEvidenceFiles(e restoreEvidence) []restoreEvidenceFile {
	return []restoreEvidenceFile{{"original_marker.json", e.beforeMarker}, {"target_marker.json", e.afterMarker},
		{"original_manifest.json", e.beforeManifest}, {"target_manifest.json", e.afterManifest}}
}

func publishRestoreBytes(ctx context.Context, root *runtimepaths.Root, relative string, raw []byte) (runtimepaths.StreamPublication, *Error) {
	result, p := root.CreateBackupStream(ctx, relative, int64(len(raw)), func(out io.Writer) error {
		_, err := io.Copy(out, bytes.NewReader(raw))
		return err
	}, func(reader io.ReaderAt, size int64) error {
		if size != int64(len(raw)) {
			return failure("BACKUP_RESTORE_EVIDENCE_CHANGED")
		}
		read, err := readRestoreEvidenceBytes(reader, size)
		if err != nil || !bytes.Equal(read, raw) {
			return failure("BACKUP_RESTORE_EVIDENCE_CHANGED")
		}
		return nil
	})
	if p != nil {
		return result, failure("BACKUP_RESTORE_PUBLICATION_FAILED")
	}
	return result, nil
}

func readRestoreEvidenceBytes(reader io.ReaderAt, size int64) ([]byte, error) {
	if size < 0 || size > MaxManifestBytes {
		return nil, failure("BACKUP_RESTORE_EVIDENCE_INVALID")
	}
	raw := make([]byte, int(size))
	for offset := 0; offset < len(raw); {
		end := min(offset+(64<<10), len(raw))
		n, err := reader.ReadAt(raw[offset:end], int64(offset))
		if n != end-offset || err != nil && err != io.EOF {
			clear(raw)
			return nil, failure("BACKUP_RESTORE_EVIDENCE_UNAVAILABLE")
		}
		offset = end
	}
	return raw, nil
}

// readRestoreJournal admits only one scoped native restore sentinel and its
// exact bound evidence. It grants no replay authority: the caller must inspect
// current root placement/contents and retain/recheck evidence at every effect.
func readRestoreJournal(ctx context.Context, root *runtimepaths.Root, channel string) (journal restoreJournal, evidence restoreEvidence, problem *Error) {
	if ctx == nil || ctx.Err() != nil {
		return journal, evidence, failure("BACKUP_CANCELED")
	}
	if root == nil || root.RequireProcessOwnership() != nil {
		return journal, evidence, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	classification, p := datahome.ClassifyOwnedRecovery(root, &channel)
	if p != nil || classification.Cardinality != datahome.RecoverySingle || classification.Kind != datahome.RecoveryRestore || classification.Marker != datahome.RecoveryMarkerInitialized {
		return journal, evidence, failure("BACKUP_RESTORE_RECOVERY_REQUIRED")
	}
	scope, q := root.CredentialScope()
	if q != nil {
		return journal, evidence, failure("BACKUP_RESTORE_SCOPE_UNAVAILABLE")
	}
	var leases []*runtimepaths.BackupRead
	defer func() {
		for _, lease := range leases {
			if lease.Close() != nil {
				problem = failure("BACKUP_RESTORE_EVIDENCE_CLOSE_FAILED")
			}
		}
		if problem != nil {
			journal, evidence = restoreJournal{}, restoreEvidence{}
		}
	}()
	read := func(path string, limit int64) ([]byte, *Error) {
		lease, p := root.OpenBackupRead(ctx, path, limit)
		if p != nil {
			return nil, failure("BACKUP_RESTORE_EVIDENCE_UNAVAILABLE")
		}
		leases = append(leases, lease)
		raw, err := readRestoreEvidenceBytes(lease, lease.Size())
		if err != nil {
			return nil, failure("BACKUP_RESTORE_EVIDENCE_UNAVAILABLE")
		}
		return raw, nil
	}
	raw, problem := read(datahome.RestoreRecoveryPath, maxRestoreJournalBytes)
	if problem != nil {
		return journal, evidence, problem
	}
	journal, problem = decodeRestoreJournal(raw, scope.Fingerprint())
	if problem != nil {
		return journal, evidence, problem
	}
	if channel != journal.BeforeHome.Channel {
		return journal, evidence, failure("BACKUP_RESTORE_HOME_CHANGED")
	}
	for _, item := range []struct {
		name   string
		limit  int64
		target *[]byte
	}{
		{"original_marker.json", datahome.MaxMarkerBytes, &evidence.beforeMarker},
		{"target_marker.json", datahome.MaxMarkerBytes, &evidence.afterMarker},
		{"original_manifest.json", MaxManifestBytes, &evidence.beforeManifest},
		{"target_manifest.json", MaxManifestBytes, &evidence.afterManifest},
	} {
		*item.target, problem = read(journal.recoveryPath()+"/"+item.name, item.limit)
		if problem != nil {
			return journal, evidence, problem
		}
	}
	if problem = validateRestoreEvidence(journal, evidence); problem != nil {
		return journal, evidence, problem
	}
	current, problem := read(datahome.MarkerPath, datahome.MaxMarkerBytes)
	if problem != nil {
		return journal, evidence, problem
	}
	if !bytes.Equal(current, evidence.beforeMarker) && !bytes.Equal(current, evidence.afterMarker) {
		return journal, evidence, failure("BACKUP_RESTORE_HOME_CHANGED")
	}
	observed, q := root.CredentialScope()
	if q != nil || !scope.Equal(observed) {
		return journal, evidence, failure("BACKUP_RESTORE_SCOPE_MISMATCH")
	}
	for _, lease := range leases {
		if lease.Check() != nil {
			return journal, evidence, failure("BACKUP_RESTORE_EVIDENCE_CHANGED")
		}
	}
	if ctx.Err() != nil {
		return journal, evidence, failure("BACKUP_CANCELED")
	}
	classification, p = datahome.ClassifyOwnedRecovery(root, &channel)
	if p != nil || classification.Cardinality != datahome.RecoverySingle || classification.Kind != datahome.RecoveryRestore {
		return journal, evidence, failure("BACKUP_RESTORE_RECOVERY_REQUIRED")
	}
	return journal, evidence, nil
}
