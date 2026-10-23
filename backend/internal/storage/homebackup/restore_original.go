package homebackup

import (
	"context"
	"reflect"
	"slices"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// captureRestoreOriginal records exact quiescent physical bytes without opening
// SQLite or normalizing its image. The caller must first validate the full
// logical source and close all snapshot/database connections; schemaVersion is
// that independently verified schema, not a claim inferred from raw file names.
// No secret store, source write, staging creation or new ownership occurs here.
// Full inventory and selected-root presence are retained even for empty roots.
func captureRestoreOriginal(ctx context.Context, root *runtimepaths.Root, home datahome.Info, schemaVersion *int64) (manifest Manifest, presentRoots []string, problem *Error) {
	if ctx == nil || ctx.Err() != nil {
		return manifest, nil, failure("BACKUP_CANCELED")
	}
	if root == nil || root.RequireProcessOwnership() != nil {
		return manifest, nil, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	if !validRestoreHome(home) {
		return manifest, nil, failure("BACKUP_HOME_UNAVAILABLE")
	}
	marker, q := root.OpenBackupRead(ctx, datahome.MarkerPath, datahome.MaxMarkerBytes)
	if q != nil {
		return manifest, nil, failure("BACKUP_HOME_UNAVAILABLE")
	}
	defer func() {
		if marker.Close() != nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
		if problem != nil {
			manifest, presentRoots = Manifest{}, nil
		}
	}()
	raw, err := readRestoreEvidenceBytes(marker, marker.Size())
	if err != nil {
		return manifest, nil, failure("BACKUP_HOME_UNAVAILABLE")
	}
	observed, markerProblem := datahome.ParseMarkerForDocumentMigration(raw)
	if markerProblem != nil || observed == nil || *observed != home {
		return manifest, nil, failure("BACKUP_RESTORE_HOME_CHANGED")
	}
	check := func() *Error {
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		classification, p := datahome.ClassifyOwnedRecovery(root, &home.Channel)
		if p != nil || classification.Cardinality != datahome.RecoveryNone || marker.Check() != nil {
			return failure("BACKUP_RESTORE_RECOVERY_REQUIRED")
		}
		return nil
	}
	if p := check(); p != nil {
		return manifest, nil, p
	}
	before, p := collectRestoreInventory(ctx, root, nil)
	if p != nil {
		return manifest, nil, p
	}
	// A supplied logical schema must describe an actually present database.
	// Raw companion files alone cannot establish a valid logical source.
	database, companions := false, false
	for _, file := range before.Files {
		if file.RelativePath == databasePath {
			database = true
		}
		if file.RelativePath == databasePath+"-wal" || file.RelativePath == databasePath+"-shm" || file.RelativePath == databasePath+"-journal" {
			companions = true
		}
	}
	if database != (schemaVersion != nil) || (companions && !database) {
		return manifest, nil, failure("BACKUP_RESTORE_SCHEMA_MISMATCH")
	}
	manifest = Manifest{FormatVersion: FormatVersion, DataHomeFormatVersion: 2, DataHomeID: home.DataHomeID, DataHomeChannel: home.Channel, Files: []File{}}
	if schemaVersion != nil {
		value := *schemaVersion
		manifest.SQLiteSchemaVersion = &value
	}
	for _, file := range before.Files {
		digest, p := fingerprintRestoreFile(ctx, root, file.RelativePath, file.SizeBytes)
		if p != nil {
			return manifest, nil, p
		}
		manifest.Files = append(manifest.Files, File{file.RelativePath, file.SizeBytes, digest})
	}
	after, p := collectRestoreInventory(ctx, root, nil)
	if p != nil {
		return manifest, nil, p
	}
	if !reflect.DeepEqual(before.Files, after.Files) || !reflect.DeepEqual(before.Directories, after.Directories) {
		return manifest, nil, failure("BACKUP_SOURCE_CHANGED")
	}
	if p := check(); p != nil {
		return manifest, nil, p
	}
	manifest, problem = validationManifest(manifest)
	if problem != nil {
		return manifest, nil, problem
	}
	if problem = validatePayloadPaths(manifest.Files); problem != nil {
		return manifest, nil, problem
	}
	roots, _ := selectedRoots(nil)
	presentRoots = []string{}
	for _, name := range roots {
		if slices.Contains(before.Directories, name) {
			presentRoots = append(presentRoots, name)
		}
	}
	return manifest, presentRoots, nil
}
