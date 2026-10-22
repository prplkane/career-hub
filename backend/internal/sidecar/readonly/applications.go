package readonly

import (
	"context"
	"path/filepath"
	"slices"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
)

const applicationDatabasePath = "applications/career_hub.sqlite3"

// ApplicationSummaries reads only the requested saved-job summaries. It never
// creates/migrates a database and is not Python's general list/detail operation.
// Empty requests return before context/workspace/filesystem checks, like the
// source. Other requests require this process's already claimed Data Home.
// Callers bound context/lifecycle; native cleanup is synchronous even on timeout.
func (w *Workspace) ApplicationSummaries(ctx context.Context, jobIDs []string) (map[string]applications.Summary, error) {
	if err := applications.ValidateSummaryRequest(jobIDs); err != nil {
		return nil, applicationReadError(err)
	}
	if len(jobIDs) == 0 {
		return map[string]applications.Summary{}, nil
	}
	if err := applicationCanceled(ctx); err != nil {
		return nil, err
	}
	if w == nil || w.dataHomeID == "" {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	ids := slices.Clone(jobIDs)
	if _, err := validateRoots(w.start); err != nil {
		return nil, err
	}
	return admittedApplicationSummaries(ctx, w.dataHomeID,
		func() (*datahome.Info, *datahome.Error) {
			return datahome.Inspect(w.start.DataRoot, &w.start.Channel)
		},
		func() (map[string]applications.Summary, error) {
			return readOwnedApplicationSummaries(ctx, w.start.DataRoot, ids)
		})
}

func readOwnedApplicationSummaries(ctx context.Context, dataRoot string, ids []string) (payload map[string]applications.Summary, failure error) {
	root, pathErr := runtimepaths.Open(dataRoot)
	if pathErr != nil {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			payload = nil
			if failure == nil {
				failure = fail("DATA_HOME_UNAVAILABLE")
			}
		}
	}()
	files, pathErr := root.GuardSQLiteFiles(applicationDatabasePath)
	if pathErr != nil {
		return nil, applicationPathError(pathErr)
	}
	if files == nil {
		return map[string]applications.Summary{}, nil
	}
	defer func() {
		if err := files.Close(); err != nil {
			payload, failure = nil, applicationPathError(err)
		}
	}()
	if err := applicationCanceled(ctx); err != nil {
		return nil, err
	}
	db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(dataRoot, filepath.FromSlash(applicationDatabasePath)))
	if err != nil {
		return nil, fail("APPLICATION_DATABASE_UNAVAILABLE")
	}
	// LIFO: the query's rows/connection finish first, then DB.Close, the guard's
	// final main/auxiliary checks and pins, and finally the originating root.
	// Never return early by abandoning a driver goroutine on context timeout.
	defer func() {
		if db.Close() != nil {
			payload, failure = nil, fail("APPLICATION_CLEANUP_FAILED")
		}
	}()
	payload, readErr := applications.ReadSummaries(ctx, db, ids)
	if readErr != nil {
		return nil, applicationReadError(readErr)
	}
	return payload, nil
}

func applicationCanceled(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return fail("APPLICATION_READ_CANCELED")
	}
	return nil
}

// No native cause, SQLite text, path, caller ID or unrecognized diagnostic
// survives this service boundary, including through errors.Unwrap or formatting.
func applicationReadError(err *applications.Error) error {
	switch err.Code {
	case "INPUT_LIMIT":
		return fail("APPLICATION_INPUT_LIMIT")
	case "INPUT_UNSUPPORTED":
		return fail("APPLICATION_INPUT_UNSUPPORTED")
	case "SCHEMA_TOO_NEW":
		return fail("APPLICATION_SCHEMA_TOO_NEW")
	case "SCHEMA_PREPARATION_REQUIRED":
		return fail("APPLICATION_SCHEMA_PREPARATION_REQUIRED")
	case "NOT_FOUND":
		return fail("APPLICATION_NOT_FOUND")
	case "INVALID_STATUS":
		return fail("APPLICATION_INVALID_STATUS")
	case "INVALID_DECISION":
		return fail("APPLICATION_INVALID_DECISION")
	case "READ_CANCELED":
		return fail("APPLICATION_READ_CANCELED")
	case "DATABASE_CLEANUP_FAILED":
		return fail("APPLICATION_CLEANUP_FAILED")
	case "RESULT_LIMIT":
		return fail("APPLICATION_RESULT_LIMIT")
	case "DATABASE_VALUE_UNSUPPORTED":
		return fail("APPLICATION_VALUE_UNSUPPORTED")
	default:
		return fail("APPLICATION_READ_FAILED")
	}
}

func applicationPathError(err *runtimepaths.Error) error {
	switch err.Code {
	case "OWNERSHIP_REQUIRED":
		return fail("DATA_HOME_OWNERSHIP_REQUIRED")
	case "SQLITE_FILES_UNSUPPORTED", "SQLITE_VOLUME_REJECTED":
		return fail("APPLICATION_STORAGE_UNSUPPORTED")
	default:
		return fail("APPLICATION_PATH_REJECTED")
	}
}

// Like the Vault admission seam, this private composition tests drift without
// timing races or publicly injectable product dependencies. Admission is checked
// after actual DB/guard cleanup, including when the load failed or was empty.
func admittedApplicationSummaries(ctx context.Context, expectedID string,
	inspect func() (*datahome.Info, *datahome.Error),
	load func() (map[string]applications.Summary, error),
) (map[string]applications.Summary, error) {
	if err := applicationCanceled(ctx); err != nil {
		return nil, err
	}
	if expectedID == "" {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	before, admissionErr := inspect()
	if err := applicationCanceled(ctx); err != nil {
		return nil, err
	}
	if admissionErr != nil {
		return nil, admissionError(admissionErr)
	}
	if before == nil || !before.Initialized {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if before.DataHomeID != expectedID {
		return nil, fail("DATA_HOME_IDENTITY_CHANGED")
	}
	identity, channel, format := before.DataHomeID, before.Channel, before.FormatVersion
	payload, loadErr := load()
	if err := applicationCanceled(ctx); err != nil {
		return nil, err
	}
	after, admissionErr := inspect()
	if err := applicationCanceled(ctx); err != nil {
		return nil, err
	}
	if admissionErr != nil {
		return nil, admissionError(admissionErr)
	}
	if after == nil || !after.Initialized {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if identity != after.DataHomeID || channel != after.Channel || format != after.FormatVersion {
		return nil, fail("DATA_HOME_IDENTITY_CHANGED")
	}
	if loadErr != nil {
		return nil, loadErr
	}
	if payload == nil {
		return nil, fail("APPLICATION_READ_FAILED")
	}
	return payload, nil
}
