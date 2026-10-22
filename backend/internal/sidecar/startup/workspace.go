// Package startup composes explicit Windows Data Home bootstrap with the
// read-only sidecar workspace. It does not discover profiles or select itself.
package startup

import (
	"context"
	"fmt"
	"io"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

// Error keeps only a fixed public code; it does not retain paths, native causes,
// catalog contents or bootstrap internals. Failure never promises rollback.
type Error struct{ Code string }

func (e *Error) Error() string              { return "Startup workspace could not be prepared." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func fail(code string) error                { return &Error{Code: code} }

// Open validates resources/separation before CH36 may write, then admits the
// resulting initialized home and effective registry. Other writers must remain
// quiescent; neither the marker nor this function is a Data Home ownership lock.
// The process owner supplies startup cancellation/deadlines and exact-child
// reaping because synchronous native calls cannot be forcibly canceled here.
func Open(ctx context.Context, start protocol.StartRecord) (*readonly.Workspace, error) {
	return open(ctx, start, datahome.Bootstrap)
}

// OpenForProcess adds ownership before bootstrap's private-file operations and
// prepares the application database only after full workspace admission. The
// claim survives this function and Run; the caller must exit the process.
func OpenForProcess(ctx context.Context, start protocol.StartRecord) (*readonly.Workspace, error) {
	return openPrepared(ctx, start, datahome.BootstrapForProcess, schema.PrepareOwned)
}

type bootstrapFunc func(context.Context, string, string) (*datahome.Info, *datahome.BootstrapError)

type prepareFunc func(context.Context, string) (*schema.Preparation, error)

func openPrepared(ctx context.Context, start protocol.StartRecord, bootstrap bootstrapFunc, prepare prepareFunc) (*readonly.Workspace, error) {
	w, err := open(ctx, start, bootstrap)
	if err != nil {
		return nil, err
	}
	return prepareAdmitted(ctx, start.DataRoot, w, prepare)
}

// This private seam tests admission drift without exposing alternative writers
// to the executable or HTTP. The coordinator returns only after its transaction,
// connections and native guards close; a preparation error never means rollback.
func prepareAdmitted(ctx context.Context, dataRoot string, w *readonly.Workspace, prepare prepareFunc) (*readonly.Workspace, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if err := w.CheckAdmission(); err != nil {
		return nil, workspaceError(err)
	}
	if ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if prepare == nil {
		return nil, fail("SIDECAR_STARTUP_FAILED")
	}
	report, prepErr := prepare(ctx, dataRoot)
	if ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	// Check even after a reported preparation failure. Never admit a changed
	// identity or newly pending restore after successful SQL/backup cleanup.
	if err := w.CheckAdmission(); err != nil {
		return nil, workspaceError(err)
	}
	if ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if prepErr != nil {
		return nil, preparationError(prepErr)
	}
	if report == nil {
		return nil, fail("SIDECAR_STARTUP_FAILED")
	}
	return w, nil
}

func preparationError(err error) error {
	if known, ok := err.(*schema.PreparationError); ok {
		switch known.Code {
		case "APPLICATION_PREPARATION_CANCELED":
			return fail("DATA_HOME_STARTUP_CANCELLED")
		case "APPLICATION_PATH_REJECTED", "DATA_HOME_OWNERSHIP_REQUIRED", "APPLICATION_ORPHANED_SQLITE_FILES",
			"APPLICATION_DATABASE_CREATE_FAILED", "APPLICATION_DATABASE_UNAVAILABLE", "APPLICATION_CLEANUP_FAILED",
			"APPLICATION_UPGRADE_LOCK_FAILED", "APPLICATION_ROLLBACK_FAILED", "APPLICATION_SCHEMA_READ_FAILED",
			"APPLICATION_SCHEMA_TOO_NEW", "APPLICATION_SCHEMA_UPGRADE_FAILED", "APPLICATION_COMMIT_UNCONFIRMED",
			"APPLICATION_BACKUP_PATH_FAILED", "APPLICATION_BACKUP_NAME_FAILED", "APPLICATION_BACKUP_CREATE_FAILED",
			"APPLICATION_BACKUP_CLEANUP_FAILED", "APPLICATION_BACKUP_VERIFY_FAILED", "APPLICATION_BACKUP_METADATA_FAILED",
			"APPLICATION_BACKUP_SOURCE_FAILED", "APPLICATION_BACKUP_COPY_FAILED":
			return fail(known.Code)
		}
	}
	return fail("SIDECAR_STARTUP_FAILED")
}

func open(ctx context.Context, start protocol.StartRecord, bootstrap bootstrapFunc) (*readonly.Workspace, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if err := readonly.Preflight(start); err != nil {
		return nil, workspaceError(err)
	}
	if ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if _, err := bootstrap(ctx, start.DataRoot, start.Channel); err != nil {
		return nil, bootstrapError(err)
	}
	if ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	w, err := readonly.Open(start)
	if err != nil {
		return nil, workspaceError(err)
	}
	if ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	return w, nil
}

func workspaceError(err error) error {
	if known, ok := err.(*readonly.Error); ok {
		switch known.Code {
		case "SIDECAR_START_INVALID_PORT", "SIDECAR_START_INVALID_CHANNEL", "SIDECAR_START_INVALID_PATH", "SIDECAR_PATH_BOUNDARY_INVALID", "SIDECAR_RESOURCE_INVALID", "SIDECAR_SCHEMA_INITIALIZATION_FAILED", "DATA_HOME_UNAVAILABLE", "INVALID_DATA_HOME", "DATA_HOME_NEWER_THAN_APP", "DATA_HOME_MIGRATION_REQUIRED", "DATA_HOME_RECOVERY_REQUIRED", "DATA_HOME_CHANNEL_MISMATCH", "DATA_HOME_INPUT_UNSUPPORTED", "DATA_HOME_IDENTITY_CHANGED":
			return fail(known.Code)
		}
	}
	return fail("SIDECAR_STARTUP_FAILED")
}

func bootstrapError(err *datahome.BootstrapError) error {
	switch err.Code {
	case "INVALID_CHANNEL", "INVALID_DATA_HOME", "DATA_HOME_UNAVAILABLE", "DATA_HOME_UNWRITABLE", "DATA_HOME_UNREADABLE", "DATA_HOME_NEWER_THAN_APP", "DATA_HOME_MIGRATION_REQUIRED", "DATA_HOME_RECOVERY_REQUIRED", "DATA_HOME_CHANNEL_MISMATCH", "DATA_HOME_INPUT_UNSUPPORTED", "DATA_HOME_STARTUP_CANCELLED", "DATA_HOME_BOOTSTRAP_UNSUPPORTED", "PARTIAL_DATA_HOME", "DEFAULT_CONFLICT", "DATA_HOME_ID_UNAVAILABLE", "ACL_CONTEXT_CANCELED", "ACL_SETUP_FAILED", "ACL_TREE_LIMIT", "DATA_HOME_PERMISSIONS_UNSAFE", "DATA_HOME_IN_USE", "DATA_HOME_OWNERSHIP_UNAVAILABLE", "DATA_HOME_OWNERSHIP_UNSUPPORTED":
		return fail(err.Code)
	default:
		return fail("SIDECAR_STARTUP_FAILED")
	}
}
