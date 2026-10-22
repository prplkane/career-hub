package catalog

import (
	"fmt"
	"slices"
	"sort"

	"careerhub.local/backend/internal/textcompat"
)

// Build creates the effective read-only catalog from already validated model
// projections and the raw private-file bytes used for optimistic revisions.
// It performs no filesystem access. Raw bytes are not reparsed or rewritten.
// YAML/model decoding and managed-file ownership belong to a separate adapter.
func Build(coreGroups, customGroups []Group, coreSkills, customSkills []Term, rawGroups, rawSkills []byte) (*Snapshot, error) {
	// The source file loader rejects per-catalog duplicate skill IDs before
	// reconciliation, including duplicates that a private overlay would hide.
	for _, terms := range [][]Term{coreSkills, customSkills} {
		seen := make(map[string]bool, len(terms))
		for _, term := range terms {
			if seen[term.ID] {
				return nil, &Error{Class: "ValueError", Message: "Duplicate technology id: " + term.ID}
			}
			seen[term.ID] = true
		}
	}
	if len(coreGroups) == 0 {
		coreGroups = DefaultGroups()
	}
	groups, coreGroupIDs, redirects, notices := reconcileGroups(coreGroups, customGroups)
	skills, coreSkillIDs, skillNotices := reconcileSkills(coreSkills, customSkills, redirects)
	notices = append(notices, skillNotices...)
	privateArchived := make(map[string]bool)
	for _, group := range groups {
		if !coreGroupIDs[group.ID] && group.Archived {
			privateArchived[group.ID] = true
		}
	}
	for index := range skills {
		skill := &skills[index]
		groupID := EffectiveGroupID(*skill)
		if coreSkillIDs[skill.ID] && privateArchived[groupID] && !skill.Archived {
			skill.Archived = true
			markFields(skill, "archived")
			notices = append(notices, Notice{Kind: "core_skill_archived_with_private_group", CoreID: skill.ID,
				CustomID: groupID, Value: skill.Name, Resolution: "private_group_archive_preserved"})
		}
	}
	if err := Validate(groups, skills); err != nil {
		return nil, err
	}
	return newSnapshot(groups, skills, coreGroupIDs, coreSkillIDs, notices, rawGroups, rawSkills)
}

func markFields(term *Term, fields ...string) {
	for _, field := range fields {
		if !slices.Contains(term.FieldsSet, field) {
			term.FieldsSet = append(term.FieldsSet, field)
		}
	}
}

func reconcileGroups(core, custom []Group) ([]Group, map[string]bool, map[string]string, []Notice) {
	byID, byName := make(map[string]Group), make(map[string]Group)
	usedNames := make(map[string]bool)
	for _, group := range core {
		usedNames[textcompat.Normalize(group.Name)] = true
	}
	for _, group := range custom {
		byID[group.ID] = group
		byName[textcompat.Normalize(group.Name)] = group
		usedNames[textcompat.Normalize(group.Name)] = true
	}
	groups, notices := make([]Group, 0, len(core)+len(custom)), []Notice{}
	coreIDs, redirects := make(map[string]bool), make(map[string]string)
	for _, source := range core {
		if sameID, ok := byID[source.ID]; ok {
			redirects[source.ID] = sameID.ID
			continue
		}
		group := cloneGroup(source)
		if sameName, ok := byName[textcompat.Normalize(source.Name)]; ok {
			for sequence := 1; ; sequence++ {
				suffix := "built-in"
				if sequence > 1 {
					suffix = fmt.Sprintf("built-in %d", sequence)
				}
				candidate := source.Name + " (" + suffix + ")"
				if !usedNames[textcompat.Normalize(candidate)] {
					group.Name = candidate
					usedNames[textcompat.Normalize(candidate)] = true
					break
				}
			}
			notices = append(notices, Notice{Kind: "core_group_name_disambiguated", CoreID: source.ID,
				CustomID: sameName.ID, Value: source.Name, Resolution: "built_in_display_name_disambiguated"})
		}
		groups = append(groups, group)
		coreIDs[group.ID] = true
	}
	for _, group := range custom {
		groups = append(groups, cloneGroup(group))
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	return groups, coreIDs, redirects, notices
}

func mergeTerm(core, custom Term) Term {
	result := cloneTerm(core)
	result.Name, result.Category = custom.Name, custom.Category
	result.Aliases = []string{}
	seen := map[string]bool{textcompat.Normalize(custom.Name): true}
	values := append(append(append([]string{}, core.Aliases...), core.Name), custom.Aliases...)
	for _, value := range values {
		normalized := textcompat.Normalize(value)
		if normalized != "" && !seen[normalized] {
			result.Aliases = append(result.Aliases, value)
			seen[normalized] = true
		}
	}
	// Clone first so nullable fields and relations cannot retain caller memory.
	overlay := cloneTerm(custom)
	for _, field := range custom.FieldsSet {
		switch field {
		case "parent":
			result.Parent = overlay.Parent
		case "kind":
			result.Kind = overlay.Kind
		case "primary_domain":
			result.PrimaryDomain = overlay.PrimaryDomain
		case "primary_group_id":
			result.PrimaryGroupID = overlay.PrimaryGroupID
		case "relations":
			result.Relations = overlay.Relations
		case "archived":
			result.Archived = overlay.Archived
		case "created_via":
			result.CreatedVia = overlay.CreatedVia
		case "created_at":
			result.CreatedAt = overlay.CreatedAt
		}
	}
	// Python validates a complete model_dump after a same-ID merge: all model
	// fields are now present, including defaults and explicit nulls.
	result.FieldsSet = []string{"id", "name", "aliases", "category", "primary_group_id", "parent", "kind",
		"primary_domain", "relations", "archived", "created_via", "created_at"}
	return result
}

func reconcileSkills(core, custom []Term, groupRedirects map[string]string) ([]Term, map[string]bool, []Notice) {
	coreByID := make(map[string]Term, len(core))
	for _, skill := range core {
		coreByID[skill.ID] = skill
	}
	effectiveCustom, notices := make([]Term, 0, len(custom)), []Notice{}
	shadowed, customOwners := make(map[string]bool), make(map[string]Term)
	for _, skill := range custom {
		if existing, ok := coreByID[skill.ID]; ok {
			effectiveCustom = append(effectiveCustom, mergeTerm(existing, skill))
			shadowed[skill.ID] = true
		} else {
			effectiveCustom = append(effectiveCustom, cloneTerm(skill))
		}
	}
	for _, skill := range effectiveCustom {
		for _, name := range append([]string{skill.Name}, skill.Aliases...) {
			customOwners[textcompat.Normalize(name)] = skill
		}
	}
	skills, coreIDs, redirects := make([]Term, 0, len(core)+len(custom)), make(map[string]bool), make(map[string]string)
	for _, source := range core {
		if shadowed[source.ID] {
			continue
		}
		if owner, ok := customOwners[textcompat.Normalize(source.Name)]; ok {
			redirects[source.ID] = owner.ID
			notices = append(notices, Notice{Kind: "core_skill_shadowed_by_name", CoreID: source.ID,
				CustomID: owner.ID, Value: source.Name, Resolution: "private_skill_preserved"})
			continue
		}
		skill := cloneTerm(source)
		skill.Aliases = []string{}
		for _, alias := range source.Aliases {
			if owner, ok := customOwners[textcompat.Normalize(alias)]; ok {
				notices = append(notices, Notice{Kind: "core_skill_alias_shadowed", CoreID: source.ID,
					CustomID: owner.ID, Value: alias, Resolution: "conflicting_core_alias_ignored"})
			} else {
				skill.Aliases = append(skill.Aliases, alias)
			}
		}
		if !slices.Equal(skill.Aliases, source.Aliases) {
			markFields(&skill, "aliases")
		}
		skills = append(skills, skill)
		coreIDs[skill.ID] = true
	}
	skills = append(skills, effectiveCustom...)
	for index := range skills {
		skill := &skills[index]
		groupID := EffectiveGroupID(*skill)
		if redirected, ok := groupRedirects[groupID]; ok && redirected != groupID {
			skill.PrimaryGroupID = &redirected
			markFields(skill, "primary_group_id")
		}
		previousParent := skill.Parent
		if skill.Parent != nil {
			if redirected, ok := redirects[*skill.Parent]; ok {
				skill.Parent = &redirected
			}
			if *skill.Parent == skill.ID {
				skill.Parent = nil
			}
		}
		relations, seen := []Relation{}, make(map[[2]string]bool)
		for _, relation := range skill.Relations {
			if redirected, ok := redirects[relation.TargetID]; ok {
				relation.TargetID = redirected
			}
			key := [2]string{relation.Type, relation.TargetID}
			if relation.TargetID != skill.ID && !seen[key] {
				relations = append(relations, relation)
				seen[key] = true
			}
		}
		parentChanged := (previousParent == nil) != (skill.Parent == nil)
		if previousParent != nil && skill.Parent != nil {
			parentChanged = *previousParent != *skill.Parent
		}
		if parentChanged || !slices.Equal(skill.Relations, relations) {
			// The source model_copy supplies both keys whenever either value
			// changes. Retain that presence for a future overlay reuse.
			markFields(skill, "parent", "relations")
		}
		skill.Relations = relations
	}
	sort.SliceStable(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	return skills, coreIDs, notices
}
