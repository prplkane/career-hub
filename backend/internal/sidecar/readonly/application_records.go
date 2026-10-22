package readonly

import (
	"context"
	"database/sql"
	"path/filepath"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
)

// These are complete persistence services, not partially migrated HTTP routes.
// Existing route-level assessment and digest-status enrichments remain required.
func (w *Workspace) Applications(ctx context.Context, filters applications.Filters) ([]applications.Record, error) {
	if err := applications.ValidateFilters(filters); err != nil {
		return nil, applicationReadError(err)
	}
	return readApplicationModel(w, ctx, func(db *sql.DB) ([]applications.Record, *applications.Error) {
		return applications.ReadApplications(ctx, db, filters)
	})
}

func (w *Workspace) Application(ctx context.Context, jobID string) (*applications.Detail, error) {
	if err := applications.ValidateSummaryRequest([]string{jobID}); err != nil {
		return nil, applicationReadError(err)
	}
	return readApplicationModel(w, ctx, func(db *sql.DB) (*applications.Detail, *applications.Error) {
		return applications.ReadApplication(ctx, db, jobID)
	})
}

func (w *Workspace) ResumePlan(ctx context.Context, jobID string) (*applications.PlanRecord, error) {
	if err := applications.ValidateSummaryRequest([]string{jobID}); err != nil {
		return nil, applicationReadError(err)
	}
	return readApplicationModel(w, ctx, func(db *sql.DB) (*applications.PlanRecord, *applications.Error) {
		return applications.ReadResumePlan(ctx, db, jobID)
	})
}

func (w *Workspace) ResumeLibrary(ctx context.Context, filters applications.ResumeFilters) ([]applications.ResumeEntry, error) {
	if err := applications.ValidateResumeFilters(filters); err != nil {
		return nil, applicationReadError(err)
	}
	return readApplicationModel(w, ctx, func(db *sql.DB) ([]applications.ResumeEntry, *applications.Error) {
		return applications.ReadResumeLibrary(ctx, db, filters)
	})
}

// ResumeDigestIdentities is internal-only. Do not expose content identities as
// browser management data or imply a resume was digested from this projection.
func (w *Workspace) ResumeDigestIdentities(ctx context.Context) (map[string]applications.DigestIdentity, error) {
	return readApplicationModel(w, ctx, func(db *sql.DB) (map[string]applications.DigestIdentity, *applications.Error) {
		return applications.ReadResumeDigestIdentities(ctx, db)
	})
}

func readApplicationModel[T any](w *Workspace, ctx context.Context, read func(*sql.DB) (T, *applications.Error)) (T, error) {
	return admittedApplicationModel(ctx, w.CheckAdmission, func() (T, error) {
		return readOwnedApplicationModel(ctx, w.start.DataRoot, read)
	})
}

// This private seam proves post-cleanup admission without racing a live Data
// Home. A valid missing plan is nil; failures always discard any partial value.
func admittedApplicationModel[T any](ctx context.Context, check func() error, load func() (T, error)) (T, error) {
	var empty T
	if err := applicationCanceled(ctx); err != nil {
		return empty, err
	}
	if err := check(); err != nil {
		return empty, err
	}
	if err := applicationCanceled(ctx); err != nil {
		return empty, err
	}
	payload, loadErr := load() // Returns only after native cleanup has finished.
	if err := applicationCanceled(ctx); err != nil {
		return empty, err
	}
	if err := check(); err != nil {
		return empty, err
	}
	if err := applicationCanceled(ctx); err != nil {
		return empty, err
	}
	if loadErr != nil {
		return empty, loadErr
	}
	return payload, nil
}

func readOwnedApplicationModel[T any](ctx context.Context, dataRoot string, read func(*sql.DB) (T, *applications.Error)) (payload T, failure error) {
	return readOwnedApplicationDB(ctx, dataRoot, func(db *sql.DB) (T, error) {
		value, problem := read(db)
		if problem != nil {
			var empty T
			return empty, applicationReadError(problem)
		}
		return value, nil
	})
}

// The guarded DB lifecycle is shared by application records and minimized
// email-application updates; neither reader may return before cleanup.
func readOwnedApplicationDB[T any](ctx context.Context, dataRoot string, read func(*sql.DB) (T, error)) (payload T, failure error) {
	var empty T
	root, pathErr := runtimepaths.Open(dataRoot)
	if pathErr != nil {
		return empty, fail("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			payload, failure = empty, fail("DATA_HOME_UNAVAILABLE")
		}
	}()
	files, pathErr := root.GuardSQLiteFiles(applicationDatabasePath)
	if pathErr != nil {
		return empty, applicationPathError(pathErr)
	}
	if files == nil {
		// Unlike the historical summary pilot, a full read never pretends an
		// unprepared workspace has a valid empty application database.
		return empty, fail("APPLICATION_SCHEMA_PREPARATION_REQUIRED")
	}
	defer func() {
		if err := files.Close(); err != nil {
			payload, failure = empty, applicationPathError(err)
		}
	}()
	if err := applicationCanceled(ctx); err != nil {
		return empty, err
	}
	db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(dataRoot, filepath.FromSlash(applicationDatabasePath)))
	if err != nil {
		return empty, fail("APPLICATION_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if db.Close() != nil {
			payload, failure = empty, fail("APPLICATION_CLEANUP_FAILED")
		}
	}()
	// LIFO: domain rows/transaction/lease, DB, native SQLite guard, then Root.
	// No timeout goroutine can return while another operation still owns files.
	payload, readErr := read(db)
	if readErr != nil {
		return empty, readErr
	}
	return payload, nil
}
