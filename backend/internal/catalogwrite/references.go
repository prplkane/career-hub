package catalogwrite

import (
	"bytes"
	"context"
	"slices"
	"strconv"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/textcompat"
)

const DefaultCustomGroupID = "user_my_custom_skills"
const DefaultCustomGroupName = "My custom skills"

// SkillReferenceDraft is approved input, not text extracted from an unreviewed
// JD. Explicit nil provenance means source None. Callers wishing to use the
// Python dataclass default supply CreatedVia="unspecified".
type SkillReferenceDraft struct {
	EntityID              string
	CompetencyIDs         []string
	PendingSkills         []PendingSkill
	CreatedVia, CreatedAt *string
}
type PendingSkill struct {
	Name    string
	GroupID *string
}
type SkillReferenceResult struct {
	SkillIDsByEntity   map[string][]string `json:"skill_ids_by_entity"`
	SkillNamesByEntity map[string][]string `json:"skill_names_by_entity"`
	CreatedSkills      []catalog.Term      `json:"created_skills"`
	PreviousRevision   string              `json:"previous_revision"`
	Revision           string              `json:"revision"`
}

// SkillReferencePlan has no public writable fields or arbitrary path input.
// Accessors return owned copies. This is a proposal, not authority to persist a
// Vault entity independently of its vocabulary. A future Vault transaction must
// bind its own complete read set, native owner and recovery boundary.
type SkillReferencePlan struct {
	result       SkillReferenceResult
	replacements map[string][]byte
	prospective  *catalog.Snapshot
}

func cloneReferenceTerm(term catalog.Term) catalog.Term {
	term.Aliases = slices.Clone(term.Aliases)
	term.Relations = slices.Clone(term.Relations)
	term.FieldsSet = slices.Clone(term.FieldsSet)
	clone := func(p *string) *string {
		if p == nil {
			return nil
		}
		value := *p
		return &value
	}
	term.PrimaryGroupID, term.Parent, term.PrimaryDomain = clone(term.PrimaryGroupID), clone(term.Parent), clone(term.PrimaryDomain)
	term.CreatedVia, term.CreatedAt = clone(term.CreatedVia), clone(term.CreatedAt)
	return term
}
func (p *SkillReferencePlan) Result() SkillReferenceResult {
	result := p.result
	result.SkillIDsByEntity, result.SkillNamesByEntity = map[string][]string{}, map[string][]string{}
	for key, values := range p.result.SkillIDsByEntity {
		result.SkillIDsByEntity[key] = slices.Clone(values)
	}
	for key, values := range p.result.SkillNamesByEntity {
		result.SkillNamesByEntity[key] = slices.Clone(values)
	}
	result.CreatedSkills = make([]catalog.Term, len(p.result.CreatedSkills))
	for i, term := range p.result.CreatedSkills {
		result.CreatedSkills[i] = cloneReferenceTerm(term)
	}
	return result
}
func (p *SkillReferencePlan) CatalogReplacements() map[string][]byte {
	result := make(map[string][]byte, len(p.replacements))
	for path, raw := range p.replacements {
		result[path] = bytes.Clone(raw)
	}
	return result
}
func newReferencePlan(state *catalogstore.State) *SkillReferencePlan {
	return &SkillReferencePlan{result: SkillReferenceResult{SkillIDsByEntity: map[string][]string{},
		SkillNamesByEntity: map[string][]string{}, CreatedSkills: []catalog.Term{},
		PreviousRevision: state.Snapshot.Revision(), Revision: state.Snapshot.Revision()},
		replacements: map[string][]byte{}, prospective: state.Snapshot}
}
func referenceBounds(ctx context.Context, references []SkillReferenceDraft) *Error {
	nodes, size := len(references), 0
	limit := &Error{Stage: "input", Code: "CATALOG_REFERENCE_INPUT_LIMIT"}
	if nodes > cataloginput.YAMLMaxNodes {
		return limit
	}
	var problem *Error
	check := func(value string) bool {
		if !utf8.ValidString(value) {
			problem = &Error{Stage: "input", Code: "CATALOG_NAME_UNICODE_UNSUPPORTED"}
			return false
		}
		if len(value) > cataloginput.YAMLMaxInputBytes-size {
			problem = limit
			return false
		}
		size += len(value)
		return true
	}
	for _, reference := range references {
		if e := cancelled(ctx); e != nil {
			return e
		}
		if len(reference.CompetencyIDs) > cataloginput.YAMLMaxNodes-nodes {
			return limit
		}
		nodes += len(reference.CompetencyIDs)
		if len(reference.PendingSkills) > cataloginput.YAMLMaxNodes-nodes {
			return limit
		}
		nodes += len(reference.PendingSkills)
		if !check(reference.EntityID) {
			return problem
		}
		for _, id := range reference.CompetencyIDs {
			if !check(id) {
				return problem
			}
		}
		for _, pending := range reference.PendingSkills {
			if !check(pending.Name) || pending.GroupID != nil && !check(*pending.GroupID) {
				return problem
			}
		}
		for _, value := range []*string{reference.CreatedVia, reference.CreatedAt} {
			if value != nil && !check(*value) {
				return problem
			}
		}
	}
	return nil
}
func referenceTerm(id, name, groupID string, source, stamp *string) (catalog.Term, *Error) {
	text := func(s string) cataloginput.Value { return cataloginput.Value{Kind: "string", Text: s} }
	optional := func(p *string) cataloginput.Value {
		if p == nil {
			return cataloginput.Value{Kind: "null"}
		}
		return text(*p)
	}
	record := cataloginput.Value{Kind: "mapping"}
	pair := func(key string, value cataloginput.Value) {
		record.Entries = append(record.Entries, cataloginput.Entry{Key: text(key), Value: value})
	}
	pair("id", text(id))
	pair("name", text(name))
	pair("aliases", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}})
	pair("category", text("custom_skill"))
	pair("primary_group_id", text(groupID))
	pair("created_via", optional(source))
	pair("created_at", optional(stamp))
	terms, e := cataloginput.ValidateTerms(cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{record}})
	if e != nil {
		if e.Unsupported != "" {
			return catalog.Term{}, &Error{Stage: "input", Code: "CATALOG_MODEL_UNSUPPORTED"}
		}
		// Source calls one TechnologyTerm constructor, not a list TypeAdapter.
		for i := range e.Issues {
			e.Issues[i].Loc = slices.Clone(e.Issues[i].Loc[1:])
		}
		return catalog.Term{}, &Error{Stage: "model", Code: "CATALOG_MODEL_INVALID", Model: e}
	}
	return terms[0], nil
}
func referenceID(name string, used map[string]bool, next map[string]int) (string, *Error) {
	base, e := customID(name, nil, "custom_skill")
	if e != nil {
		return "", e
	}
	id := base
	if used[id] {
		n := max(2, next[base])
		for used[base+"_"+strconv.Itoa(n)] {
			n++
		}
		id = base + "_" + strconv.Itoa(n)
		next[base] = n + 1
	}
	used[id] = true
	return id, nil
}

// PlanSkillReferences does no I/O. State must be the internally captured
// catalogstore state; callers must not mutate inputs concurrently. The native
// service replans inside its owned operation, never trusting a caller plan.
func PlanSkillReferences(ctx context.Context, state *catalogstore.State, references []SkillReferenceDraft, revision string) (*SkillReferencePlan, *Error) {
	if ctx == nil || state == nil || state.Snapshot == nil {
		return nil, &Error{Stage: "input", Code: "CATALOG_INPUT_INVALID"}
	}
	if e := cancelled(ctx); e != nil {
		return nil, e
	}
	if revision != state.Snapshot.Revision() {
		return nil, revisionConflict()
	}
	if e := referenceBounds(ctx, references); e != nil {
		return nil, e
	}
	plan := newReferencePlan(state)
	groups, skills := slices.Clone(state.CustomGroups), slices.Clone(state.CustomSkills)
	allGroups, allSkills := state.Snapshot.Groups(), state.Snapshot.Skills()
	groupsByID, skillsByID, names := map[string]catalog.Group{}, map[string]catalog.Term{}, map[string]catalog.Term{}
	for _, g := range allGroups {
		groupsByID[g.ID] = g
	}
	used, next, createdIDs := map[string]bool{}, map[string]int{}, map[string]bool{}
	for _, t := range allSkills {
		skillsByID[t.ID], used[t.ID] = t, true
		for _, name := range append([]string{t.Name}, t.Aliases...) {
			names[textcompat.Normalize(name)] = t
		}
	}
	coreNames := map[string]catalog.Term{}
	for _, term := range state.CoreSkills {
		for _, name := range append([]string{term.Name}, term.Aliases...) {
			coreNames[textcompat.Normalize(name)] = term
		}
	}
	for _, reference := range references {
		if e := cancelled(ctx); e != nil {
			return nil, e
		}
		ids, seen := []string{}, map[string]bool{}
		addID := func(id string) {
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
		for _, rawID := range reference.CompetencyIDs {
			if e := cancelled(ctx); e != nil {
				return nil, e
			}
			id, e := editGroupID(rawID)
			if e != nil {
				return nil, e
			}
			skill, exists := skillsByID[id]
			if !exists {
				return nil, registryError("SKILL_NOT_FOUND", "SkillRegistryError", "Skill id was not found: "+id)
			}
			if skill.Archived {
				return nil, registryError("SKILL_ARCHIVED", "SkillRegistryError", "Archived skill cannot be added as new evidence: "+skill.Name)
			}
			addID(id)
		}
		for _, pending := range reference.PendingSkills {
			if e := cancelled(ctx); e != nil {
				return nil, e
			}
			name, e := editName(pending.Name, "skill name")
			if e != nil {
				return nil, e
			}
			key := textcompat.Normalize(name)
			if matched, exists := names[key]; exists {
				if createdIDs[matched.ID] && pending.GroupID != nil {
					groupID, e := editGroupID(*pending.GroupID)
					if e != nil {
						return nil, e
					}
					if matched.PrimaryGroupID == nil || groupID != *matched.PrimaryGroupID {
						return nil, registryError("SKILL_GROUP_CONFLICT", "SkillRegistryError", "New skill '"+name+"' was assigned to more than one group.")
					}
				}
				if matched.Archived {
					return nil, registryError("SKILL_ARCHIVED", "SkillRegistryError", "Archived skill cannot be added as new evidence: "+matched.Name)
				}
				addID(matched.ID)
				continue
			}
			if owner, exists := coreNames[key]; exists {
				return nil, registryError("SKILL_NAME_CONFLICT", "SkillRegistryError", "Name or alias '"+name+"' conflicts with skill '"+owner.Name+"'.")
			}
			var group catalog.Group
			if pending.GroupID == nil {
				existing, exists := groupsByID[DefaultCustomGroupID]
				if exists {
					if existing.Archived {
						return nil, registryError("SKILL_GROUP_ARCHIVED", "SkillRegistryError", "The default custom skill group is archived.")
					}
					group = existing
				} else {
					group = catalog.Group{ID: DefaultCustomGroupID, Name: DefaultCustomGroupName, CascadeArchivedSkillIDs: []string{}}
					groups, allGroups = append(groups, group), append(allGroups, group)
					groupsByID[group.ID] = group
				}
			} else {
				groupID, e := editGroupID(*pending.GroupID)
				if e != nil {
					return nil, e
				}
				var exists bool
				group, exists = groupsByID[groupID]
				if !exists {
					return nil, registryError("SKILL_GROUP_NOT_FOUND", "SkillRegistryError", "Skill group was not found: "+groupID)
				}
			}
			if group.Archived {
				return nil, registryError("SKILL_GROUP_ARCHIVED", "SkillRegistryError", "Archived skill group cannot receive new skills: "+group.Name)
			}
			id, e := referenceID(name, used, next)
			if e != nil {
				return nil, e
			}
			skill, e := referenceTerm(id, name, group.ID, reference.CreatedVia, reference.CreatedAt)
			if e != nil {
				return nil, e
			}
			skills, allSkills = append(skills, skill), append(allSkills, skill)
			skillsByID[id], names[key], createdIDs[id] = skill, skill, true
			plan.result.CreatedSkills = append(plan.result.CreatedSkills, skill)
			addID(id)
		}
		resolvedNames := make([]string, len(ids))
		for i, id := range ids {
			resolvedNames[i] = skillsByID[id].Name
		}
		plan.result.SkillIDsByEntity[reference.EntityID], plan.result.SkillNamesByEntity[reference.EntityID] = ids, resolvedNames
	}
	if e := catalog.Validate(allGroups, allSkills); e != nil {
		return nil, editDomainError(e)
	}
	if e := finishReferencePlan(ctx, state, plan, groups, skills, len(groups) != len(state.CustomGroups), len(skills) != len(state.CustomSkills)); e != nil {
		return nil, e
	}
	return plan, nil
}
func finishReferencePlan(ctx context.Context, state *catalogstore.State, plan *SkillReferencePlan, groups []catalog.Group, skills []catalog.Term, groupsChanged, skillsChanged bool) *Error {
	rawGroups, rawSkills := state.RawGroups, state.RawSkills
	var e error
	if groupsChanged {
		rawGroups, e = encodeGroups(groups)
		if e != nil {
			return &Error{Stage: "encode", Code: "CATALOG_ENCODING_FAILED"}
		}
		plan.replacements[catalogstore.CustomGroupsPath] = rawGroups
	}
	if skillsChanged {
		rawSkills, e = encodeSkills(skills)
		if e != nil {
			return &Error{Stage: "encode", Code: "CATALOG_ENCODING_FAILED"}
		}
		plan.replacements[catalogstore.CustomSkillsPath] = rawSkills
	}
	if len(plan.replacements) == 0 {
		return cancelled(ctx)
	}
	var input *cataloginput.DecodeError
	if groupsChanged {
		groups, input = cataloginput.DecodeGroups(rawGroups, true)
		if input != nil {
			return &Error{Stage: "encode", Code: "CATALOG_ENCODING_FAILED"}
		}
	}
	if skillsChanged {
		skills, input = cataloginput.DecodeTerms(rawSkills, true)
		if input != nil {
			return &Error{Stage: "encode", Code: "CATALOG_ENCODING_FAILED"}
		}
	}
	plan.prospective, e = catalog.Build(state.CoreGroups, groups, state.CoreSkills, skills, rawGroups, rawSkills)
	if e != nil {
		return editDomainError(e)
	}
	plan.result.Revision = plan.prospective.Revision()
	return cancelled(ctx)
}
