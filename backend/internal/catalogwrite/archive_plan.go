package catalogwrite

import (
	"context"
	"slices"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/skills/catalog"
)

type ArchiveResult struct {
	Record             GroupRecord `json:"record"`
	AffectedSkillIDs   []string    `json:"affected_skill_ids"`
	AffectedSkillCount int         `json:"affected_skill_count"`
	Revision           string      `json:"revision"`
}
type archivePlan struct {
	result       ArchiveResult
	replacements map[string][]byte
	prospective  *catalog.Snapshot
}

// planArchive retains source ordering, no-op behavior, cascade membership and
// core read-only precedence. Only currently active members enter an archive
// cascade; restoration cannot resurrect individually pre-archived skills.
func planArchive(ctx context.Context, state *catalogstore.State, id string, archived bool, revision string) (*archivePlan, *Error) {
	if ctx == nil || state == nil || state.Snapshot == nil {
		return nil, &Error{Stage: "input", Code: "CATALOG_INPUT_INVALID"}
	}
	if e := cancelled(ctx); e != nil {
		return nil, e
	}
	if revision != state.Snapshot.Revision() {
		return nil, revisionConflict()
	}
	if slices.Contains(state.Snapshot.CoreGroupIDs(), id) {
		return nil, registryError("CORE_GROUP_READ_ONLY", "SkillRegistryError", "Core skill groups are read-only.")
	}
	index := slices.IndexFunc(state.CustomGroups, func(group catalog.Group) bool { return group.ID == id })
	if index < 0 {
		return nil, registryError("SKILL_GROUP_NOT_FOUND", "SkillRegistryError", "Custom skill group was not found.")
	}
	group := state.CustomGroups[index]
	group.CascadeArchivedSkillIDs = append([]string{}, group.CascadeArchivedSkillIDs...)
	plan := &archivePlan{result: ArchiveResult{Record: GroupRecord{group, "custom"}, AffectedSkillIDs: []string{}, Revision: revision}, replacements: map[string][]byte{}, prospective: state.Snapshot}
	if group.Archived == archived {
		return plan, nil
	}
	affected := map[string]bool{}
	restorable := make(map[string]bool, len(group.CascadeArchivedSkillIDs))
	for _, id := range group.CascadeArchivedSkillIDs {
		if e := cancelled(ctx); e != nil {
			return nil, e
		}
		restorable[id] = true
	}
	for _, term := range state.Snapshot.Skills() {
		if e := cancelled(ctx); e != nil {
			return nil, e
		}
		if catalog.EffectiveGroupID(term) != id {
			continue
		}
		if archived && !term.Archived || !archived && term.Archived && restorable[term.ID] {
			affected[term.ID] = true
			plan.result.AffectedSkillIDs = append(plan.result.AffectedSkillIDs, term.ID)
		}
	}
	terms := slices.Clone(state.CustomSkills)
	termsChanged := false
	for i := range terms {
		if !affected[terms[i].ID] {
			continue
		}
		terms[i].Archived = archived
		terms[i].FieldsSet = slices.Clone(terms[i].FieldsSet)
		if !slices.Contains(terms[i].FieldsSet, "archived") {
			terms[i].FieldsSet = append(terms[i].FieldsSet, "archived")
		}
		termsChanged = true
	}
	groups := slices.Clone(state.CustomGroups)
	group.Archived = archived
	group.CascadeArchivedSkillIDs = []string{}
	if archived {
		group.CascadeArchivedSkillIDs = slices.Clone(plan.result.AffectedSkillIDs)
	}
	groups[index] = group
	rawGroups, err := encodeGroups(groups)
	if err != nil {
		return nil, &Error{Stage: "encode", Code: "CATALOG_ENCODE_FAILED"}
	}
	rawSkills := state.RawSkills
	if termsChanged {
		rawSkills, err = encodeSkills(terms)
		if err != nil {
			return nil, &Error{Stage: "encode", Code: "CATALOG_ENCODE_FAILED"}
		}
		// Validate exactly what the reader will observe, including field presence.
		var inputErr *cataloginput.DecodeError
		terms, inputErr = cataloginput.DecodeTerms(rawSkills, true)
		if inputErr != nil {
			return nil, &Error{Stage: "encode", Code: "CATALOG_ENCODE_FAILED"}
		}
		plan.replacements[catalogstore.CustomSkillsPath] = rawSkills
	}
	prospective, err := catalog.Build(state.CoreGroups, groups, state.CoreSkills, terms, rawGroups, rawSkills)
	if err != nil {
		if domain, ok := err.(*catalog.Error); ok {
			return nil, &Error{Stage: "validation", Code: domain.Code, Registry: domain}
		}
		return nil, &Error{Stage: "validation", Code: "CATALOG_VALIDATION_FAILED"}
	}
	if e := cancelled(ctx); e != nil {
		return nil, e
	}
	plan.replacements[catalogstore.CustomGroupsPath] = rawGroups
	plan.result.Record = GroupRecord{group, "custom"}
	plan.result.AffectedSkillCount = len(plan.result.AffectedSkillIDs)
	plan.result.Revision = prospective.Revision()
	plan.prospective = prospective
	return plan, nil
}
