package catalogwrite

import (
	"bytes"
	"context"
	"errors"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/catalogtransaction"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// ArchiveOutcome is returned even on failure. Retain Write: a timeout or cleanup
// failure does not undo published files. Result is exposed only after readback.
// No result/error field contains the journal's before/after private bytes.
type ArchiveOutcome struct {
	Result        *ArchiveResult
	Revision      string
	Write         catalogtransaction.Report
	CleanupFailed bool
}
type archiveSession struct {
	data, resources *runtimepaths.Root
	home            datahome.Info
}
type archiveObservation struct {
	raw     []byte
	present bool
}
type archiveReadSet struct {
	root  *runtimepaths.Root
	reads map[string]archiveObservation
}

func (r *archiveReadSet) Check(path string) *runtimepaths.Error { return r.root.Check(path) }
func (r *archiveReadSet) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	raw, present, e := r.root.ReadFile(path, limit)
	if e == nil {
		r.reads[path] = archiveObservation{bytes.Clone(raw), present}
	}
	return raw, present, e
}
func (r *archiveReadSet) unchanged() bool {
	for path, expected := range r.reads {
		raw, present, e := r.root.ReadFile(path, cataloginput.YAMLMaxInputBytes)
		if e != nil || present != expected.present || !bytes.Equal(raw, expected.raw) {
			return false
		}
	}
	return true
}

type archiveOverlay struct {
	root    *runtimepaths.Root
	changes []catalogtransaction.Change
}

func (r archiveOverlay) Check(path string) *runtimepaths.Error { return r.root.Check(path) }
func (r archiveOverlay) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	for _, change := range r.changes {
		if change.Path == path {
			return bytes.Clone(change.After), !change.CheckOnly || change.BeforePresent, nil
		}
	}
	return r.root.ReadFile(path, limit)
}
func (a *archiveSession) sameHome() bool {
	info, e := datahome.InspectOwnedForRegistryRecovery(a.data, &a.home.Channel)
	return e == nil && *info == a.home
}
func (a *archiveSession) ready() bool {
	pending, e := a.data.Exists(datahome.RegistryRecoveryPath)
	return e == nil && !pending && a.sameHome()
}
func (s *Service) runArchive(ctx context.Context, recovery bool,
	action func(*archiveSession, *ArchiveOutcome) *Error) (out *ArchiveOutcome, problem *Error) {
	out = &ArchiveOutcome{}
	if s == nil || s.gate == nil || ctx == nil {
		return out, &Error{Stage: "configuration", Code: "CATALOG_SERVICE_UNAVAILABLE"}
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return out, cancelled(ctx)
	}
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return out, &Error{Stage: "cancelled", Code: "CATALOG_OPERATION_CANCELLED"}
	}
	defer release()
	if e := cancelled(ctx); e != nil {
		return out, e
	}
	data, pathErr := runtimepaths.Open(s.dataRoot)
	if pathErr != nil {
		return out, &Error{Stage: "path", Code: pathErr.Code}
	}
	closeRoot := func(root *runtimepaths.Root) {
		if root.Close() != nil {
			out.CleanupFailed = true
			out.Result = nil
			if problem == nil {
				problem = &Error{Stage: "close", Code: "CATALOG_CLOSE_FAILED"}
			}
		}
		if problem != nil {
			problem.Committed = len(out.Write.AppliedFiles) > 0 || out.Write.FilesComplete
		}
	}
	defer closeRoot(data)
	info, admissionErr := datahome.InspectOwnedForRegistryRecovery(data, nil)
	if admissionErr != nil {
		return out, &Error{Stage: "admission", Code: admissionErr.Code}
	}
	if !recovery {
		pending, e := data.Exists(datahome.RegistryRecoveryPath)
		if e != nil || pending {
			return out, &Error{Stage: "recovery", Code: "CATALOG_RECOVERY_REQUIRED"}
		}
	}
	resources, pathErr := runtimepaths.Open(s.resourceRoot)
	if pathErr != nil {
		return out, &Error{Stage: "path", Code: pathErr.Code}
	}
	defer closeRoot(resources)
	return out, action(&archiveSession{data, resources, *info}, out)
}

// SetCustomGroupArchived publishes the complete archive/restore operation under
// the existing native process owner and shared operation gate. It never claims a
// home, invokes Python, deletes evidence, or writes a core catalog. An incomplete
// transaction requires explicit RecoverRegistry, not automatic request retry.
func (s *Service) SetCustomGroupArchived(ctx context.Context, id string, archived bool, revision string) (*ArchiveOutcome, *Error) {
	return s.runArchive(ctx, false, func(a *archiveSession, out *ArchiveOutcome) *Error {
		core := &archiveReadSet{a.resources, map[string]archiveObservation{}}
		private := &archiveReadSet{a.data, map[string]archiveObservation{}}
		state, e := catalogstore.ReadState(core, private)
		if e != nil {
			return &Error{Stage: "load", Code: "CATALOG_LOAD_FAILED", Load: e}
		}
		plan, problem := planArchive(ctx, state, id, archived, revision)
		if problem != nil {
			return problem
		}
		if len(plan.replacements) == 0 {
			if !core.unchanged() || !private.unchanged() || !a.ready() {
				return revisionConflict()
			}
			if e := cancelled(ctx); e != nil {
				return e
			}
			out.Result = &plan.result
			out.Revision = plan.result.Revision
			return nil
		}
		if problem := publishCatalog(ctx, a, core, private, plan.replacements, plan.prospective, out); problem != nil {
			return problem
		}
		out.Result = &plan.result
		return nil
	})
}

// RecoverRegistry is an explicit roll-forward operation. Every journal member
// must equal its before or after state. The complete target is revalidated on
// current shipped resources; invalid/newer/edited inputs are never overwritten.
// Recovery returning an empty Write means no journal was present, not that an
// earlier caller's transaction was rolled back.
func (s *Service) RecoverRegistry(ctx context.Context) (*ArchiveOutcome, *Error) {
	return s.runArchive(ctx, true, func(a *archiveSession, out *ArchiveOutcome) *Error {
		core := &archiveReadSet{a.resources, map[string]archiveObservation{}}
		var prospective *catalogstore.State
		validate := func(ctx context.Context, changes []catalogtransaction.Change) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			if !a.sameHome() {
				return catalogtransaction.ErrConflict
			}
			var e *catalogstore.Error
			prospective, e = catalogstore.ReadState(core, archiveOverlay{a.data, changes})
			if e != nil || !core.unchanged() {
				return catalogtransaction.ErrTarget
			}
			return ctx.Err()
		}
		var transactionErr error
		out.Write, transactionErr = catalogtransaction.RecoverOwned(ctx, a.data, a.home.DataHomeID, validate)
		if transactionErr != nil {
			return archiveTransactionError(transactionErr)
		}
		if prospective == nil {
			var e *catalogstore.Error
			prospective, e = catalogstore.ReadState(core, a.data)
			if e != nil {
				return &Error{Stage: "readback", Code: "CATALOG_READBACK_FAILED", Load: e}
			}
		}
		return archiveReadback(a, core, prospective.Snapshot.Revision(), prospective.Snapshot.CatalogRevision(), out)
	})
}
func archiveReadback(a *archiveSession, core *archiveReadSet, revision, catalogRevision string, out *ArchiveOutcome) *Error {
	if !a.ready() || !core.unchanged() {
		return &Error{Stage: "readback", Code: "CATALOG_CONTEXT_CHANGED"}
	}
	observed, e := catalogstore.ReadState(a.resources, a.data)
	if e != nil {
		return &Error{Stage: "readback", Code: "CATALOG_READBACK_FAILED", Load: e}
	}
	if observed.Snapshot.Revision() != revision || observed.Snapshot.CatalogRevision() != catalogRevision || !core.unchanged() || !a.ready() {
		return &Error{Stage: "readback", Code: "CATALOG_CHANGED_AFTER_SAVE"}
	}
	out.Revision = observed.Snapshot.Revision()
	return nil
}
func archiveTransactionError(e error) *Error {
	code := "CATALOG_TRANSACTION_FAILED"
	switch {
	case errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		code = "CATALOG_OPERATION_CANCELLED"
	case errors.Is(e, catalogtransaction.ErrPending):
		code = "CATALOG_RECOVERY_REQUIRED"
	case errors.Is(e, catalogtransaction.ErrConflict):
		code = "CATALOG_TRANSACTION_CONFLICT"
	case errors.Is(e, catalogtransaction.ErrJournal):
		code = "CATALOG_JOURNAL_INVALID"
	case errors.Is(e, catalogtransaction.ErrTarget):
		code = "CATALOG_TARGET_INVALID"
	case errors.Is(e, catalogtransaction.ErrInput):
		code = "CATALOG_TRANSACTION_INPUT_INVALID"
	}
	return &Error{Stage: "transaction", Code: code}
}
