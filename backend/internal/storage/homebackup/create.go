package homebackup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path"
	"reflect"
	"strings"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type CreateRequest struct {
	DataRoot              string
	Channel               string
	ArchiveRelativePath   string // Optional managed backups/...zip path, not an absolute path.
	IncludeRoots          []string
	ConfirmWritersStopped bool
}

// CreationResult retains facts even when the accompanying error is non-nil.
// A created snapshot is removed only after confirmed publication. CleanupConfirmed on
// each publication describes only that stream's temporary sibling, NOT this
// snapshot, its handles, or power-loss durability. Unknown outcomes prohibit an
// automatic retry. FullSourceValidation counts the entire source, not the subset
// exported to ArchiveRelativePath. Neither the marker nor OS keys is exported.
type CreationResult struct {
	SnapshotRelativePath string
	SnapshotNamespace    runtimepaths.DirectoryCreationOutcome
	StagedFiles          int
	LastStagedFile       runtimepaths.StreamPublication
	ArchiveRelativePath  string
	ArchivePublication   runtimepaths.StreamPublication
	ArchiveFiles         int
	FullSourceValidation *PayloadValidation
	SnapshotCleanup      SnapshotCleanupResult
}

// CreateOwned creates a verified local recovery ZIP without changing active
// content, initializing a Home, migrating a schema, or discovering credentials.
// Call only in a dedicated maintenance process AFTER its supervisor has reaped
// the application child and the user has stopped legacy Python/CLI/file writers.
// It requires an existing process claim and explicit quiescence confirmation.
// These checks do not prove arbitrary external writers are absent; a Go claim
// excludes only cooperating backends. There is no listener or mail work here.
//
// The complete supported Home is staged and validated even for a partial export.
// Thus partial backup is not a way to bypass an invalid unselected section. This
// also means the full staging limits/disk cost apply to partial exports. Staging
// is removed only after verified archive publication and successful closure of
// preparation handles. Failed/unknown operations retain their staging evidence.
func CreateOwned(ctx context.Context, request CreateRequest, backend keys.Backend) (CreationResult, *Error) {
	return createOwned(ctx, request, backend, createOperations{newID: newBackupID, publish: PublishSnapshot})
}

// Private deterministic fault seams; no caller-supplied product callbacks.
type createOperations struct {
	newID           func() (string, error)
	afterValidation func()
	publish         func(context.Context, *runtimepaths.Root, string, string, Manifest) (runtimepaths.StreamPublication, *Error)
	cleanup         snapshotCleanupOperations
}

func newBackupID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func createOwned(ctx context.Context, request CreateRequest, backend keys.Backend, ops createOperations) (result CreationResult, problem *Error) {
	result.SnapshotNamespace = runtimepaths.DirectoryNotCreated
	result.LastStagedFile = runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	result.ArchivePublication = result.LastStagedFile
	result.SnapshotCleanup = unstartedSnapshotCleanup()
	if ctx == nil || ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	if !request.ConfirmWritersStopped {
		return result, failure("BACKUP_WRITER_CONFIRMATION_REQUIRED")
	}
	if request.Channel != "dev" && request.Channel != "stable" {
		return result, failure("BACKUP_CHANNEL_INVALID")
	}
	selected, p := selectedRoots(request.IncludeRoots)
	if p != nil {
		return result, p
	}
	destination := request.ArchiveRelativePath
	if destination != "" && !validArchiveDestination(destination) {
		return result, failure("BACKUP_DESTINATION_INVALID")
	}
	root, pathProblem := runtimepaths.Open(request.DataRoot)
	if pathProblem != nil {
		return result, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	defer func() {
		if err := root.Close(); err != nil && problem == nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return result, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	// Retain the admitted identity through validation, key reads and publication.
	// Read-only sharing prevents ordinary marker writes/replacement, not edits of
	// other Home files. Recovery sentinel absence is checked again below.
	marker, pathProblem := root.OpenBackupRead(ctx, datahome.MarkerPath, datahome.MaxMarkerBytes)
	if pathProblem != nil {
		return result, failure("BACKUP_HOME_UNAVAILABLE")
	}
	defer func() {
		if err := marker.Close(); err != nil && problem == nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	home, admissionProblem := datahome.Inspect(request.DataRoot, &request.Channel)
	if admissionProblem != nil {
		return result, failure(admissionProblem.Code)
	}
	checkHome := func() *Error {
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		if marker.Check() != nil {
			return failure("BACKUP_SOURCE_CHANGED")
		}
		current, p := datahome.Inspect(request.DataRoot, &request.Channel)
		if p != nil {
			return failure(p.Code)
		}
		if *current != *home {
			return failure("BACKUP_SOURCE_CHANGED")
		}
		return nil
	}
	id, err := ops.newID()
	if err != nil || !lowerHex(id, 32) {
		return result, failure("BACKUP_ID_UNAVAILABLE")
	}
	if destination == "" {
		destination = "backups/backup_" + id + ".zip"
	}
	if root.Check(destination) != nil {
		return result, failure("BACKUP_DESTINATION_INVALID")
	}
	if present, p := root.Exists(destination); p != nil {
		return result, failure("BACKUP_DESTINATION_INVALID")
	} else if present {
		return result, failure("BACKUP_DESTINATION_EXISTS")
	}
	if p := checkHome(); p != nil {
		return result, p
	}
	result.SnapshotRelativePath = snapshotPrefix + id
	result.ArchiveRelativePath = destination
	var cleanupPermit *snapshotCleanupPermit
	var exported Manifest
	prepared, p := withPreparedSnapshot(ctx, request.DataRoot, result.SnapshotRelativePath, nil, func(source *runtimepaths.Root, files []File, schema *int64) *Error {
		if p := checkHome(); p != nil {
			return p
		}
		full := Manifest{FormatVersion, home.FormatVersion, home.DataHomeID, home.Channel, schema, files}
		validation, p := ValidateSnapshot(ctx, request.DataRoot, result.SnapshotRelativePath, full, backend)
		if p != nil {
			return p
		}
		result.FullSourceValidation = validation
		if ops.afterValidation != nil {
			ops.afterValidation()
		}
		if p := checkHome(); p != nil {
			return p
		}
		manifest := selectExport(full, selected)
		exported = manifest
		cleanupPermit, p = prepareSnapshotCleanup(ctx, source, request.Channel, result.SnapshotRelativePath, files)
		if p != nil {
			return p
		}
		result.ArchiveFiles = len(manifest.Files)
		if _, p := source.EnsureDirectory(path.Dir(destination)); p != nil {
			return failure("BACKUP_DESTINATION_UNAVAILABLE")
		}
		// Assign before testing the error. Later cancellation, close/rollback or
		// recovery detection must never reset a known/unknown publication outcome.
		result.ArchivePublication, p = ops.publish(ctx, source, result.SnapshotRelativePath, destination, manifest)
		if p != nil {
			return p
		}
		return checkHome()
	})
	result.SnapshotNamespace = prepared.Namespace
	result.StagedFiles = prepared.CopiedFiles
	result.LastStagedFile = prepared.LastFile
	if p != nil {
		return result, p
	}
	if p := checkHome(); p != nil {
		return result, p
	}
	if result.ArchivePublication.Outcome != runtimepaths.FilePublished || !result.ArchivePublication.CleanupConfirmed {
		return result, failure("BACKUP_PUBLICATION_FAILED")
	}
	// All preparation reservations/SQLite handles are now closed. Pin and verify
	// the actual published ZIP before disposing of this operation's working copy.
	// This also refuses a valid but different ZIP substituted after publication.
	archive, q := root.OpenBackupRead(ctx, destination, MaxArchiveBytes)
	if q != nil {
		if ctx.Err() != nil {
			return result, failure("BACKUP_CANCELED")
		}
		return result, failure("BACKUP_CLEANUP_ARCHIVE_UNAVAILABLE")
	}
	defer func() {
		if archive.Close() != nil && problem == nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	verified, p := Inspect(ctx, archive, archive.Size())
	if p != nil {
		if ctx.Err() != nil {
			return result, failure("BACKUP_CANCELED")
		}
		return result, failure("BACKUP_CLEANUP_ARCHIVE_CHANGED")
	}
	if p := verified.Verify(ctx); p != nil || !reflect.DeepEqual(verified.Manifest(), exported) || archive.Check() != nil {
		if ctx.Err() != nil {
			return result, failure("BACKUP_CANCELED")
		}
		return result, failure("BACKUP_CLEANUP_ARCHIVE_CHANGED")
	}
	result.SnapshotCleanup, p = consumeSnapshotCleanup(ctx, root, cleanupPermit, ops.cleanup)
	if p != nil {
		return result, p
	}
	if archive.Check() != nil {
		return result, failure("BACKUP_CLEANUP_ARCHIVE_CHANGED")
	}
	return result, checkHome()
}

func validArchiveDestination(value string) bool {
	return len(value) <= 4096 && strings.HasPrefix(value, "backups/") && strings.HasSuffix(value, ".zip") && safeName(value)
}

// selectedRoots already copied/validated this selection. Preserve user root
// order and each root's inventory order, with a fresh output slice.
func selectExport(full Manifest, selected []string) Manifest {
	out := full
	out.Files = []File{}
	out.SQLiteSchemaVersion = nil
	for _, name := range selected {
		if name == "applications" && full.SQLiteSchemaVersion != nil {
			value := *full.SQLiteSchemaVersion
			out.SQLiteSchemaVersion = &value
		}
		for _, file := range full.Files {
			if strings.HasPrefix(file.RelativePath, name+"/") {
				out.Files = append(out.Files, file)
			}
		}
	}
	return out
}
