package homebackup

import (
	"context"
	"reflect"
	"slices"
	"strings"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type restoreValidationResult struct {
	Preparation  snapshotPreparation
	Validation   *PayloadValidation
	Physical     *Manifest
	PresentRoots []string
	cleanup      *validationCleanupWitness
}

// validateRestoreCopy validates all active managed roots without ever opening
// their SQLite database. The admitted maintenance owner supplies the expected
// marker identity and a fresh snapshot namespace; journal/quiescence admission
// stays with that owner so this also works during explicit restore recovery.
// Raw SQLite/companions are copied under .physical/, normalized only there into
// the ordinary staged payload, and never used as an export manifest themselves.
// Retained stages and partial write facts survive failure. Physical is usable
// only on success, after full semantic validation and active-source rehashing.
func validateRestoreCopy(ctx context.Context, dataRoot, snapshot string, home datahome.Info, backend keys.Backend) (restoreValidationResult, *Error) {
	return validateRestoreCopyWith(ctx, dataRoot, snapshot, home, backend, nil)
}

// afterValidation is a private deterministic drift/cancellation seam.
func validateRestoreCopyWith(ctx context.Context, dataRoot, snapshot string, home datahome.Info, backend keys.Backend, afterValidation func()) (result restoreValidationResult, problem *Error) {
	result.Preparation.Namespace = runtimepaths.DirectoryNotCreated
	result.Preparation.LastFile = runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	defer func() {
		if problem != nil {
			result.Physical, result.PresentRoots = nil, nil
			result.cleanup = nil
		}
	}()
	if ctx == nil || ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	if !validRestoreHome(home) || !strings.HasPrefix(snapshot, snapshotPrefix) || !lowerHex(strings.TrimPrefix(snapshot, snapshotPrefix), 32) {
		return result, failure("BACKUP_SNAPSHOT_INVALID")
	}
	root, q := runtimepaths.Open(dataRoot)
	if q != nil {
		return result, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return result, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	marker, q := root.OpenBackupRead(ctx, datahome.MarkerPath, datahome.MaxMarkerBytes)
	if q != nil {
		return result, failure("BACKUP_HOME_UNAVAILABLE")
	}
	defer func() {
		if marker.Close() != nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	raw, err := readRestoreEvidenceBytes(marker, marker.Size())
	if err != nil {
		return result, failure("BACKUP_HOME_UNAVAILABLE")
	}
	observed, mp := datahome.ParseMarkerForDocumentMigration(raw)
	if mp != nil || observed == nil || *observed != home {
		return result, failure("BACKUP_RESTORE_HOME_CHANGED")
	}
	before, p := collectRestoreInventory(ctx, root, nil)
	if p != nil {
		return result, p
	}
	// Canonical reserved paths must be checked before SQLite or key access.
	paths := make([]File, 0, len(before.Files))
	for _, file := range before.Files {
		paths = append(paths, File{RelativePath: file.RelativePath})
	}
	if p := validatePayloadPaths(paths); p != nil {
		return result, p
	}
	if _, q := root.EnsureDirectory(strings.TrimSuffix(snapshotPrefix, "/")); q != nil {
		return result, failure("BACKUP_SNAPSHOT_UNAVAILABLE")
	}
	reservation, outcome, q := root.ReserveDirectory(snapshot)
	result.Preparation.Namespace = outcome
	if q != nil {
		return result, failure("BACKUP_SNAPSHOT_RESERVATION_FAILED")
	}
	defer func() {
		if reservation.Close() != nil {
			problem = failure("BACKUP_SNAPSHOT_CLOSE_FAILED")
		}
	}()
	physical := Manifest{FormatVersion: FormatVersion, DataHomeFormatVersion: home.FormatVersion, DataHomeID: home.DataHomeID, DataHomeChannel: home.Channel, Files: []File{}}
	normalized := physical
	normalized.Files = []File{}
	for _, file := range before.Files {
		stage := snapshot
		isSQLite := file.RelativePath == databasePath || file.RelativePath == databasePath+"-wal" || file.RelativePath == databasePath+"-shm" || file.RelativePath == databasePath+"-journal"
		if isSQLite {
			stage += "/.physical"
		}
		copied, facts, p := copyStagedFile(ctx, root, stage, file)
		result.Preparation.LastFile = facts
		if p != nil {
			return result, p
		}
		result.Preparation.CopiedFiles++
		physical.Files = append(physical.Files, copied)
		if !isSQLite {
			normalized.Files = append(normalized.Files, copied)
		}
	}
	if reservation.Check() != nil || marker.Check() != nil {
		return result, failure("BACKUP_SOURCE_CHANGED")
	}
	problem = withDatabaseSnapshotFrom(ctx, dataRoot, snapshot, snapshot+"/.physical/"+databasePath, func(_ *runtimepaths.Root, database *databaseSnapshot) *Error {
		if database != nil {
			version := database.SchemaVersion
			physical.SQLiteSchemaVersion, normalized.SQLiteSchemaVersion = &version, &version
			normalized.Files = append(normalized.Files, database.File)
		}
		var p *Error
		result.Validation, p = ValidateSnapshot(ctx, dataRoot, snapshot, normalized, backend)
		return p
	})
	if problem != nil {
		return result, problem
	}
	if afterValidation != nil {
		afterValidation()
	}
	if ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	after, p := collectRestoreInventory(ctx, root, nil)
	if p != nil {
		return result, p
	}
	if !reflect.DeepEqual(before.Files, after.Files) || !reflect.DeepEqual(before.Directories, after.Directories) {
		return result, failure("BACKUP_SOURCE_CHANGED")
	}
	physical, p = validationManifest(physical)
	if p != nil {
		return result, p
	}
	roots, _ := selectedRoots(nil)
	for _, name := range roots {
		if !slices.Contains(before.Directories, name) {
			continue
		}
		if p := verifyRestoreTree(ctx, root, name, name, physical.Files, []string{name}); p != nil {
			return result, p
		}
		result.PresentRoots = append(result.PresentRoots, name)
	}
	if marker.Check() != nil || reservation.Check() != nil {
		return result, failure("BACKUP_SOURCE_CHANGED")
	}
	result.Physical = &physical
	result.cleanup, problem = captureValidationCleanup(ctx, root, snapshot, reservation, normalized.Files, physical.SQLiteSchemaVersion != nil)
	return result, problem
}
