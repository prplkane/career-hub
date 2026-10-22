package catalog

import (
	"sort"

	"careerhub.local/backend/internal/textcompat"
)

// Snapshot owns an immutable copy of an effective catalog. Build is responsible
// for reconciliation, source ordering, graph validation and registry invariants.
// Accessors and payload methods return independent copies, including nested
// slices and optional values. Callers must not mutate inputs during construction.
type Snapshot struct {
	groups          []Group
	skills          []Term
	coreGroupIDs    map[string]bool
	coreSkillIDs    map[string]bool
	revision        string
	catalogRevision string
	notices         []Notice
}

func newSnapshot(groups []Group, skills []Term, coreGroupIDs, coreSkillIDs map[string]bool,
	notices []Notice, rawGroups, rawSkills []byte) (*Snapshot, error) {
	snapshot := &Snapshot{
		groups:       make([]Group, len(groups)),
		skills:       make([]Term, len(skills)),
		coreGroupIDs: copySet(coreGroupIDs),
		coreSkillIDs: copySet(coreSkillIDs),
		notices:      make([]Notice, len(notices)),
		revision:     RawRevision(rawGroups, rawSkills),
	}
	for i, group := range groups {
		snapshot.groups[i] = cloneGroup(group)
	}
	for i, skill := range skills {
		snapshot.skills[i] = cloneTerm(skill)
	}
	copy(snapshot.notices, notices)
	revision, err := catalogRevision(snapshot.groups, snapshot.skills)
	if err != nil {
		return nil, err
	}
	snapshot.catalogRevision = revision
	return snapshot, nil
}

func copySet(input map[string]bool) map[string]bool {
	result := make(map[string]bool, len(input))
	for id, member := range input {
		if member {
			result[id] = true
		}
	}
	return result
}

func sortedSet(input map[string]bool) []string {
	result := make([]string, 0, len(input))
	for id, member := range input {
		if member {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func (s *Snapshot) Groups() []Group {
	result := make([]Group, len(s.groups))
	for i, group := range s.groups {
		result[i] = cloneGroup(group)
	}
	return result
}

func (s *Snapshot) Skills() []Term {
	result := make([]Term, len(s.skills))
	for i, skill := range s.skills {
		result[i] = cloneTerm(skill)
	}
	return result
}

func (s *Snapshot) Notices() []Notice {
	result := make([]Notice, len(s.notices))
	copy(result, s.notices)
	return result
}

func (s *Snapshot) CoreGroupIDs() []string  { return sortedSet(s.coreGroupIDs) }
func (s *Snapshot) CoreSkillIDs() []string  { return sortedSet(s.coreSkillIDs) }
func (s *Snapshot) Revision() string        { return s.revision }
func (s *Snapshot) CatalogRevision() string { return s.catalogRevision }

func (s *Snapshot) GroupOrigin(id string) string {
	if s.coreGroupIDs[id] {
		return "core"
	}
	return "custom"
}

func (s *Snapshot) SkillOrigin(id string) string {
	if s.coreSkillIDs[id] {
		return "core"
	}
	return "custom"
}

func optionalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func stringValues(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

func groupModel(group Group) map[string]any {
	return map[string]any{
		"id": group.ID, "name": group.Name, "archived": group.Archived,
		"cascade_archived_skill_ids": stringValues(group.CascadeArchivedSkillIDs),
	}
}

func termModel(skill Term) map[string]any {
	relations := make([]any, 0, len(skill.Relations))
	for _, relation := range skill.Relations {
		relations = append(relations, map[string]any{"type": relation.Type, "target_id": relation.TargetID})
	}
	return map[string]any{
		"id": skill.ID, "name": skill.Name, "aliases": stringValues(skill.Aliases),
		"category": skill.Category, "primary_group_id": optionalString(skill.PrimaryGroupID),
		"parent": optionalString(skill.Parent), "kind": skill.Kind,
		"primary_domain": optionalString(skill.PrimaryDomain), "relations": relations,
		"archived": skill.Archived, "created_via": optionalString(skill.CreatedVia),
		"created_at": optionalString(skill.CreatedAt),
	}
}

// Payload preserves snapshot group order and stable casefold-name order within
// each group. Model fields are expanded exactly; provenance nulls remain present.
func (s *Snapshot) Payload() map[string]any {
	groupSkills := make(map[string][]Term, len(s.groups))
	for _, skill := range s.skills {
		id := EffectiveGroupID(skill)
		groupSkills[id] = append(groupSkills[id], skill)
	}
	groups := make([]any, 0, len(s.groups))
	for _, group := range s.groups {
		ordered := groupSkills[group.ID]
		sort.SliceStable(ordered, func(i, j int) bool {
			return textcompat.Fold(ordered[i].Name) < textcompat.Fold(ordered[j].Name)
		})
		skills := make([]any, 0, len(ordered))
		for _, skill := range ordered {
			value := termModel(skill)
			value["primary_group_id"] = EffectiveGroupID(skill)
			value["origin"] = s.SkillOrigin(skill.ID)
			skills = append(skills, value)
		}
		value := groupModel(group)
		value["origin"] = s.GroupOrigin(group.ID)
		value["skills"] = skills
		groups = append(groups, value)
	}
	notices := make([]any, 0, len(s.notices))
	for _, notice := range s.notices {
		notices = append(notices, map[string]any{
			"kind": notice.Kind, "core_id": notice.CoreID, "custom_id": notice.CustomID,
			"value": notice.Value, "resolution": notice.Resolution,
		})
	}
	return map[string]any{
		"revision": s.revision, "catalog_revision": s.catalogRevision,
		"default_custom_group_id": MyCustomGroupID, "groups": groups,
		"skill_count": len(s.skills), "compatibility_notices": notices,
	}
}
