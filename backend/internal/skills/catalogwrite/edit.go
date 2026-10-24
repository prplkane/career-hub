package catalogwrite

import (
	"context"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/catalogtransaction"
)

// EditOutcome keeps the source-shaped typed result separate from publication
// facts. A failure clears Result, never the write/cleanup observations.
type EditOutcome[T any] struct {
	Result        *T
	Revision      string
	Write         catalogtransaction.Report
	CleanupFailed bool
}

func editOutcome[T any](value *T, report *ArchiveOutcome, problem *Error) *EditOutcome[T] {
	if problem != nil {
		value = nil
	}
	return &EditOutcome[T]{value, report.Revision, report.Write, report.CleanupFailed}
}
func (s *Service) CreateCustomGroup(ctx context.Context, payload map[string]any, revision string) (*EditOutcome[Result], *Error) {
	plan, report, e := s.runEdit(ctx, editRequest{operation: createGroup, payload: payload, revision: revision})
	var result *Result
	if plan != nil {
		result = plan.group
	}
	return editOutcome(result, report, e), e
}
func (s *Service) CreateCustomSkill(ctx context.Context, payload map[string]any, revision string) (*EditOutcome[SkillResult], *Error) {
	plan, report, e := s.runEdit(ctx, editRequest{operation: createSkill, payload: payload, revision: revision})
	var result *SkillResult
	if plan != nil {
		result = plan.skill
	}
	return editOutcome(result, report, e), e
}
func (s *Service) UpdateCustomSkill(ctx context.Context, id string, payload map[string]any, revision string) (*EditOutcome[SkillResult], *Error) {
	plan, report, e := s.runEdit(ctx, editRequest{operation: updateSkill, id: id, payload: payload, revision: revision})
	var result *SkillResult
	if plan != nil {
		result = plan.skill
	}
	return editOutcome(result, report, e), e
}
func (s *Service) SetCustomSkillArchived(ctx context.Context, id string, archived bool, revision string) (*EditOutcome[SkillResult], *Error) {
	plan, report, e := s.runEdit(ctx, editRequest{operation: archiveSkill, id: id, archived: archived, revision: revision})
	var result *SkillResult
	if plan != nil {
		result = plan.skill
	}
	return editOutcome(result, report, e), e
}
func (s *Service) runEdit(ctx context.Context, request editRequest) (plan *editPlan, report *ArchiveOutcome, problem *Error) {
	// Source rejects unknown fields before reading a possibly malformed home.
	if e := validateEditFields(request.operation, request.payload); e != nil {
		return nil, &ArchiveOutcome{}, e
	}
	report, problem = s.runArchive(ctx, false, func(a *archiveSession, out *ArchiveOutcome) *Error {
		core := &archiveReadSet{a.resources, map[string]archiveObservation{}}
		private := &archiveReadSet{a.data, map[string]archiveObservation{}}
		state, e := catalogstore.ReadState(core, private)
		if e != nil {
			return &Error{Stage: "load", Code: "CATALOG_LOAD_FAILED", Load: e}
		}
		if request.operation == createSkill {
			if s.now == nil {
				return &Error{Stage: "configuration", Code: "CATALOG_SERVICE_UNAVAILABLE"}
			}
			request.now = s.now()
		}
		var failure *Error
		plan, failure = planEdit(ctx, state, request)
		if failure != nil {
			return failure
		}
		return publishCatalog(ctx, a, core, private, plan.replacements, plan.prospective, out)
	})
	if problem != nil {
		plan = nil
	}
	return plan, report, problem
}

// publishCatalog is shared by group archive and single-record edits. Both
// private files are bound even for a one-file mutation; recovery can therefore
// reject changes to an unchanged or previously absent companion input.
func publishCatalog(ctx context.Context, a *archiveSession, core, private *archiveReadSet,
	replacements map[string][]byte, prospective *catalog.Snapshot, out *ArchiveOutcome) *Error {
	return publishCatalogObserved(ctx, a, core, private, replacements, prospective, out, nil)
}

// An additional source witness cannot select publication targets or replace the
// ordinary catalog/home checks. Recovery retains its existing registry policy.
func publishCatalogObserved(ctx context.Context, a *archiveSession, core, private *archiveReadSet,
	replacements map[string][]byte, prospective *catalog.Snapshot, out *ArchiveOutcome, sourceCurrent func() bool) *Error {
	current := func() bool { return sourceCurrent == nil || sourceCurrent() }
	if !core.unchanged() || !private.unchanged() || !a.ready() || !current() {
		return revisionConflict()
	}
	changes := make([]catalogtransaction.Change, 0, 2)
	for _, path := range []string{catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath} {
		before := private.reads[path]
		after, changed := replacements[path]
		if !changed {
			after = before.raw
		}
		changes = append(changes, catalogtransaction.Change{Path: path, BeforePresent: before.present, Before: before.raw, After: after, CheckOnly: !changed})
	}
	validate := func(ctx context.Context, changes []catalogtransaction.Change) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if !core.unchanged() || !a.sameHome() || !current() {
			return catalogtransaction.ErrConflict
		}
		target, e := catalogstore.ReadState(a.resources, archiveOverlay{a.data, changes})
		if e != nil || target.Snapshot.Revision() != prospective.Revision() || target.Snapshot.CatalogRevision() != prospective.CatalogRevision() {
			return catalogtransaction.ErrTarget
		}
		if !core.unchanged() || !current() {
			return catalogtransaction.ErrConflict
		}
		return ctx.Err()
	}
	var transactionErr error
	out.Write, transactionErr = catalogtransaction.BeginOwned(ctx, a.data, a.home.DataHomeID, changes, validate)
	if transactionErr != nil {
		return archiveTransactionError(transactionErr)
	}
	if !out.Write.Completed {
		return &Error{Stage: "save", Code: "CATALOG_WRITE_UNCONFIRMED"}
	}
	if e := archiveReadback(a, core, prospective.Revision(), prospective.CatalogRevision(), out); e != nil {
		return e
	}
	if !current() {
		return &Error{Stage: "readback", Code: "CATALOG_CONTEXT_CHANGED"}
	}
	return nil
}
