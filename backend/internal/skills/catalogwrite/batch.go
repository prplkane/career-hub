package catalogwrite

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/textcompat"
)

type BatchSkillResult struct {
	Records         []SkillRecord `json:"records"`
	CreatedSkillIDs []string      `json:"created_skill_ids"`
	Revision        string        `json:"revision"`
}

func cleanBatch(payload map[string]any) (names []string, groupID string, problem *Error) {
	unknown := []string{}
	for key := range payload {
		if !utf8.ValidString(key) {
			return nil, "", &Error{Stage: "input", Code: "CATALOG_NAME_UNICODE_UNSUPPORTED"}
		}
		if key != "names" && key != "primary_group_id" {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, "", registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", "Unknown skill fields: "+strings.Join(unknown, ", "))
	}
	raw, ok := payload["names"].([]any)
	if !ok {
		if typed, yes := payload["names"].([]string); yes {
			if len(typed) > 100 {
				return nil, "", registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", "No more than 100 skills can be added at once")
			}
			raw = make([]any, len(typed))
			for i, name := range typed {
				raw[i] = name
			}
		}
	}
	if len(raw) == 0 {
		return nil, "", registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", "names must be a non-empty list")
	}
	if len(raw) > 100 {
		return nil, "", registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", "No more than 100 skills can be added at once")
	}
	groupID, problem = editGroupID(payload["primary_group_id"])
	if problem != nil {
		return nil, "", problem
	}
	names, seen := []string{}, map[string]bool{}
	for _, rawName := range raw {
		name, e := editName(rawName, "skill name")
		if e != nil {
			return nil, "", e
		}
		key := textcompat.Normalize(name)
		if !seen[key] {
			names = append(names, name)
			seen[key] = true
		}
	}
	return names, groupID, nil
}
func batchDraft(names []string, groupID string, now time.Time) (SkillReferenceDraft, *Error) {
	utc := now.UTC()
	if utc.Year() < 1 || utc.Year() > 9999 {
		return SkillReferenceDraft{}, &Error{Stage: "input", Code: "CATALOG_CLOCK_INVALID"}
	}
	stamp, source := utc.Format("2006-01-02T15:04:05Z"), "manual_registry"
	reference := SkillReferenceDraft{EntityID: "registry_batch", CreatedVia: &source, CreatedAt: &stamp}
	for _, name := range names {
		reference.PendingSkills = append(reference.PendingSkills, PendingSkill{Name: name, GroupID: &groupID})
	}
	return reference, nil
}
func batchResult(plan *SkillReferencePlan) *BatchSkillResult {
	result := &BatchSkillResult{Records: []SkillRecord{}, CreatedSkillIDs: []string{}, Revision: plan.result.Revision}
	byID := map[string]catalog.Term{}
	for _, term := range plan.prospective.Skills() {
		byID[term.ID] = term
	}
	for _, id := range plan.result.SkillIDsByEntity["registry_batch"] {
		result.Records = append(result.Records, SkillRecord{byID[id], plan.prospective.SkillOrigin(id)})
	}
	for _, term := range plan.result.CreatedSkills {
		result.CreatedSkillIDs = append(result.CreatedSkillIDs, term.ID)
	}
	return result
}
func (s *Service) CreateCustomSkills(ctx context.Context, payload map[string]any, revision string) (*EditOutcome[BatchSkillResult], *Error) {
	names, groupID, e := cleanBatch(payload)
	if e != nil {
		return &EditOutcome[BatchSkillResult]{}, e
	}
	plan, report, e := s.runReferencePlan(ctx, func(state *catalogstore.State) (*SkillReferencePlan, *Error) {
		if s.now == nil {
			return nil, &Error{Stage: "configuration", Code: "CATALOG_SERVICE_UNAVAILABLE"}
		}
		reference, e := batchDraft(names, groupID, s.now())
		if e != nil {
			return nil, e
		}
		return PlanSkillReferences(ctx, state, []SkillReferenceDraft{reference}, revision)
	})
	var result *BatchSkillResult
	if plan != nil {
		result = batchResult(plan)
	}
	return editOutcome(result, report, e), e
}

// CommitRegistrySkillReferences is registry-only, like the source plan commit.
// It replans under native ownership; caller-provided plans/paths are not accepted.
// Do not use it to save vocabulary separately from a canonical Vault edit:
// that use case requires one larger recovery boundary for both sets of files.
func (s *Service) CommitRegistrySkillReferences(ctx context.Context, references []SkillReferenceDraft, revision string) (*EditOutcome[SkillReferenceResult], *Error) {
	plan, report, e := s.runReferencePlan(ctx, func(state *catalogstore.State) (*SkillReferencePlan, *Error) {
		return PlanSkillReferences(ctx, state, references, revision)
	})
	var result *SkillReferenceResult
	if plan != nil {
		value := plan.Result()
		result = &value
	}
	return editOutcome(result, report, e), e
}
func planDefaultGroup(ctx context.Context, state *catalogstore.State) (*SkillReferencePlan, *Error) {
	if ctx == nil || state == nil || state.Snapshot == nil {
		return nil, &Error{Stage: "input", Code: "CATALOG_INPUT_INVALID"}
	}
	if e := cancelled(ctx); e != nil {
		return nil, e
	}
	plan := newReferencePlan(state)
	for _, group := range state.Snapshot.Groups() {
		if group.ID == DefaultCustomGroupID {
			return plan, nil
		}
	}
	groups := append(slices.Clone(state.CustomGroups), catalog.Group{ID: DefaultCustomGroupID, Name: DefaultCustomGroupName, CascadeArchivedSkillIDs: []string{}})
	coreIDs, core := editIDSet(state.Snapshot.CoreGroupIDs()), []catalog.Group{}
	for _, group := range state.Snapshot.Groups() {
		if coreIDs[group.ID] {
			core = append(core, group)
		}
	}
	if e := catalog.Validate(append(core, groups...), state.Snapshot.Skills()); e != nil {
		return nil, editDomainError(e)
	}
	if e := finishReferencePlan(ctx, state, plan, groups, state.CustomSkills, true, false); e != nil {
		return nil, e
	}
	return plan, nil
}
func (s *Service) EnsureDefaultCustomGroup(ctx context.Context) (*EditOutcome[catalog.Snapshot], *Error) {
	plan, report, e := s.runReferencePlan(ctx, func(state *catalogstore.State) (*SkillReferencePlan, *Error) { return planDefaultGroup(ctx, state) })
	var result *catalog.Snapshot
	if plan != nil {
		result = plan.prospective
	}
	return editOutcome(result, report, e), e
}
func (s *Service) runReferencePlan(ctx context.Context, build func(*catalogstore.State) (*SkillReferencePlan, *Error)) (plan *SkillReferencePlan, report *ArchiveOutcome, problem *Error) {
	report, problem = s.runArchive(ctx, false, func(a *archiveSession, out *ArchiveOutcome) *Error {
		core := &archiveReadSet{a.resources, map[string]archiveObservation{}}
		private := &archiveReadSet{a.data, map[string]archiveObservation{}}
		state, e := catalogstore.ReadState(core, private)
		if e != nil {
			return &Error{Stage: "load", Code: "CATALOG_LOAD_FAILED", Load: e}
		}
		var failure *Error
		plan, failure = build(state)
		if failure != nil {
			return failure
		}
		if len(plan.replacements) == 0 {
			if !core.unchanged() || !private.unchanged() || !a.ready() {
				return revisionConflict()
			}
			if e := cancelled(ctx); e != nil {
				return e
			}
			out.Revision = plan.result.Revision
			return nil
		}
		return publishCatalog(ctx, a, core, private, plan.replacements, plan.prospective, out)
	})
	if problem != nil {
		plan = nil
	}
	return plan, report, problem
}
