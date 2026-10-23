package readonly

import (
	"context"
	"database/sql"
	"path/filepath"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

// Complete persistence operations only. HTTP detail still requires the source
// review-assessment/instruction/contact enrichments; no partial route is enabled.
func (w *Workspace) Discoveries(ctx context.Context, options discovery.PageOptions) ([]discovery.Record, error) {
	if e := discovery.ValidatePage(options); e != nil {
		return nil, discoveryReadError(e)
	}
	return readDiscovery(w, ctx, func(db *sql.DB, _ *runtimepaths.Root) ([]discovery.Record, *discovery.Error) {
		return discovery.ReadList(ctx, db, options)
	})
}
func (w *Workspace) DiscoveryCount(ctx context.Context, filters discovery.Filters) (int64, error) {
	if e := discovery.ValidateFilters(filters); e != nil {
		return 0, discoveryReadError(e)
	}
	return readDiscovery(w, ctx, func(db *sql.DB, _ *runtimepaths.Root) (int64, *discovery.Error) {
		return discovery.ReadCount(ctx, db, filters)
	})
}

func (w *Workspace) DiscoveryFeedInputs(ctx context.Context, options discovery.PageOptions) ([]discovery.Record, error) {
	if e := discovery.ValidatePage(options); e != nil {
		return nil, discoveryReadError(e)
	}
	return readDiscovery(w, ctx, func(db *sql.DB, _ *runtimepaths.Root) ([]discovery.Record, *discovery.Error) {
		return discovery.ReadFeedInputs(ctx, db, options)
	})
}
func (w *Workspace) Discovery(ctx context.Context, id string) (*discovery.Detail, error) {
	key, e := discovery.NormalizeID(id)
	if e != nil {
		return nil, discoveryReadError(e)
	}
	return readDiscovery(w, ctx, func(db *sql.DB, files *runtimepaths.Root) (*discovery.Detail, *discovery.Error) {
		return discovery.ReadDetail(ctx, db, files, key)
	})
}

func (w *Workspace) DiscoveryFeedDetail(ctx context.Context, id string) (*discovery.Detail, error) {
	key, e := discovery.NormalizeID(id)
	if e != nil {
		return nil, discoveryReadError(e)
	}
	return readDiscovery(w, ctx, func(db *sql.DB, files *runtimepaths.Root) (*discovery.Detail, *discovery.Error) {
		return discovery.ReadFeedDetail(ctx, db, files, key)
	})
}

// DiscoveryAssessment is an internal stored artifact, not a current fit verdict.
func (w *Workspace) DiscoveryAssessment(ctx context.Context, id string) (*discovery.Assessment, error) {
	key, e := discovery.NormalizeID(id)
	if e != nil {
		return nil, discoveryReadError(e)
	}
	return readDiscovery(w, ctx, func(db *sql.DB, _ *runtimepaths.Root) (*discovery.Assessment, *discovery.Error) {
		return discovery.ReadAssessment(ctx, db, key)
	})
}

func discoveryCanceled(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return fail("DISCOVERY_READ_CANCELED")
	}
	return nil
}
func readDiscovery[T any](w *Workspace, ctx context.Context, load func(*sql.DB, *runtimepaths.Root) (T, *discovery.Error)) (T, error) {
	return admittedDiscovery(ctx, w.CheckAdmission, func() (T, error) { return ownedDiscovery(ctx, w.start.DataRoot, load) })
}
func admittedDiscovery[T any](ctx context.Context, check func() error, load func() (T, error)) (T, error) {
	var empty T
	if err := discoveryCanceled(ctx); err != nil {
		return empty, err
	}
	if err := check(); err != nil {
		return empty, err
	}
	if err := discoveryCanceled(ctx); err != nil {
		return empty, err
	}
	payload, loadErr := load() // Native cleanup finishes before admission is rechecked.
	if err := discoveryCanceled(ctx); err != nil {
		return empty, err
	}
	if err := check(); err != nil {
		return empty, err
	}
	if err := discoveryCanceled(ctx); err != nil {
		return empty, err
	}
	if loadErr != nil {
		return empty, loadErr
	}
	return payload, nil
}
func ownedDiscovery[T any](ctx context.Context, dataRoot string, load func(*sql.DB, *runtimepaths.Root) (T, *discovery.Error)) (payload T, problem error) {
	var empty T
	root, e := runtimepaths.Open(dataRoot)
	if e != nil {
		return empty, fail("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			payload, problem = empty, fail("DATA_HOME_UNAVAILABLE")
		}
	}()
	files, e := root.GuardSQLiteFiles(applicationDatabasePath)
	if e != nil {
		return empty, discoveryPathError(e)
	}
	if files == nil {
		return empty, fail("DISCOVERY_SCHEMA_PREPARATION_REQUIRED")
	}
	defer func() {
		if e := files.Close(); e != nil {
			payload, problem = empty, discoveryPathError(e)
		}
	}()
	if err := discoveryCanceled(ctx); err != nil {
		return empty, err
	}
	db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(dataRoot, filepath.FromSlash(applicationDatabasePath)))
	if err != nil {
		return empty, fail("DISCOVERY_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if db.Close() != nil {
			payload, problem = empty, fail("DISCOVERY_CLEANUP_FAILED")
		}
	}()
	// LIFO: domain rows/transaction/lease, DB, SQLite guards, Root. No abandoned
	// goroutine can outlive the timeout response and write/open files afterwards.
	payload, readErr := load(db, root)
	if readErr != nil {
		return empty, discoveryReadError(readErr)
	}
	return payload, nil
}
func discoveryReadError(err *discovery.Error) error {
	switch err.Code {
	case "INPUT_LIMIT", "INPUT_UNSUPPORTED", "INVALID_ID", "INVALID_TEXT", "INVALID_TIMESTAMP", "INVALID_STATES", "INVALID_SOURCES", "INVALID_READINESS", "INVALID_QUEUES", "INVALID_SORT", "INVALID_LIMIT", "INVALID_OFFSET", "SCHEMA_TOO_NEW", "SCHEMA_PREPARATION_REQUIRED", "READ_CANCELED", "NOT_FOUND", "RESULT_LIMIT", "SOURCE_PATH_REJECTED", "SOURCE_UNREADABLE", "SOURCE_LENGTH_MISMATCH", "SOURCE_HASH_MISMATCH", "METADATA_INVALID", "ASSESSMENT_INVALID":
		return fail("DISCOVERY_" + err.Code)
	case "DATABASE_VALUE_UNSUPPORTED":
		return fail("DISCOVERY_VALUE_UNSUPPORTED")
	case "DATABASE_CLEANUP_FAILED":
		return fail("DISCOVERY_CLEANUP_FAILED")
	default:
		return fail("DISCOVERY_READ_FAILED")
	}
}
func discoveryPathError(err *runtimepaths.Error) error {
	switch err.Code {
	case "OWNERSHIP_REQUIRED":
		return fail("DATA_HOME_OWNERSHIP_REQUIRED")
	case "SQLITE_FILES_UNSUPPORTED", "SQLITE_VOLUME_REJECTED":
		return fail("DISCOVERY_STORAGE_UNSUPPORTED")
	default:
		return fail("DISCOVERY_PATH_REJECTED")
	}
}
