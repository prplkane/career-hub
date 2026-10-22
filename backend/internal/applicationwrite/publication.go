package applicationwrite

import (
	"context"
	"database/sql"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

// Only the shared owner controls commit and cleanup. Domain journals keep their
// own validators, witnesses and recovery gates; this is not a generic file API.
type publicationFacts struct {
	recorded, removed  bool
	cleanupCode, state string
}
type publication interface {
	facts() publicationFacts
	intentStage() string
	record(context.Context, *runtimepaths.Root, *sql.Tx, datahome.Info) *Error
	checkReady(context.Context, *runtimepaths.Root, *sql.Tx, *runtimepaths.SQLiteFiles, datahome.Info) *Error
	finishCommitted(context.Context, *runtimepaths.Root, *sql.Conn, *runtimepaths.SQLiteFiles, datahome.Info, func(string) *Error, func(*sql.Tx) error) *Error
}

func (p *discoveryPublication) facts() publicationFacts {
	return publicationFacts{p.recorded, p.removed, p.cleanupCode, p.state}
}
func (*discoveryPublication) intentStage() string { return "discovery_intent" }
func (r *sourceReads) pendingPublication() publication {
	if r.publication != nil {
		return r.publication
	}
	if r.resumePublication != nil {
		return r.resumePublication
	}
	return nil
}
func (r *sourceReads) pendingRecovery() publication {
	if r.recovery != nil {
		return r.recovery
	}
	if r.resumeRecovery != nil {
		return r.resumeRecovery
	}
	return nil
}
