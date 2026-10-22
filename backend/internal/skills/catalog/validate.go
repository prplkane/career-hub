package catalog

import (
	"fmt"
	"sort"

	"careerhub.local/backend/internal/textcompat"
)

func graphError(format string, values ...any) error {
	return &Error{Class: "ValueError", Message: fmt.Sprintf(format, values...)}
}

func registryError(code, format string, values ...any) error {
	return &Error{Class: "SkillRegistryError", Code: code, Message: fmt.Sprintf(format, values...)}
}

// ValidateTerms ports graph-only checks after model validation and overlay merge.
// It preserves input-order relation errors and sorted member_of traversal, without
// modifying the caller's terms. All relation enums/IDs are prevalidated inputs.
func ValidateTerms(terms []Term) error {
	byID := make(map[string]Term, len(terms))
	for _, term := range terms {
		if _, found := byID[term.ID]; found {
			return graphError("Duplicate technology id: %s", term.ID)
		}
		byID[term.ID] = term
	}

	memberEdges := make(map[string][]string)
	relatedPairs := make(map[[2]string]bool)
	for _, term := range terms {
		relations := make([]Relation, len(term.Relations))
		copy(relations, term.Relations)
		if term.Parent != nil {
			relations = append(relations, Relation{Type: "member_of", TargetID: *term.Parent})
		}
		seen := make(map[Relation]bool, len(relations))
		for _, relation := range relations {
			if seen[relation] {
				return graphError("Duplicate %s relation from %s to %s", relation.Type, term.ID, relation.TargetID)
			}
			seen[relation] = true
			if _, found := byID[relation.TargetID]; !found {
				return graphError("Technology relation from %s targets unknown id: %s", term.ID, relation.TargetID)
			}
			if relation.TargetID == term.ID {
				return graphError("Technology relation may not target itself: %s", term.ID)
			}
			if relation.Type == "member_of" {
				memberEdges[term.ID] = append(memberEdges[term.ID], relation.TargetID)
			}
			if relation.Type == "related_to" {
				pair := [2]string{term.ID, relation.TargetID}
				if pair[1] < pair[0] {
					pair[0], pair[1] = pair[1], pair[0]
				}
				if relatedPairs[pair] {
					return graphError("Duplicate related_to relation between %s and %s", pair[0], pair[1])
				}
				relatedPairs[pair] = true
			}
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, targets := range memberEdges {
		sort.Strings(targets)
	}
	// Explicit frames retain Python's recursive pre/post visitation order while
	// avoiding runtime stack growth on a deep caller-supplied graph.
	type frame struct {
		id   string
		next int
	}
	visited, visiting := make(map[string]bool), make(map[string]bool)
	for _, id := range ids {
		if visited[id] {
			continue
		}
		visiting[id] = true
		stack := []frame{{id: id}}
		for len(stack) != 0 {
			top := &stack[len(stack)-1]
			targets := memberEdges[top.id]
			if top.next == len(targets) {
				delete(visiting, top.id)
				visited[top.id] = true
				stack = stack[:len(stack)-1]
				continue
			}
			target := targets[top.next]
			top.next++
			if visited[target] {
				continue
			}
			if visiting[target] {
				return graphError("member_of relation cycle includes: %s", target)
			}
			visiting[target] = true
			stack = append(stack, frame{id: target})
		}
	}
	return nil
}

// Validate preserves the source's ordered registry checks: group identity/name,
// all graph constraints, each skill's group and names, then cascade bookkeeping.
// Archived records still participate in graph and global vocabulary invariants.
func Validate(groups []Group, skills []Term) error {
	groupByID := make(map[string]Group, len(groups))
	groupNames := make(map[string]string, len(groups))
	for _, group := range groups {
		if _, found := groupByID[group.ID]; found {
			return registryError("SKILL_GROUP_ID_CONFLICT", "Duplicate skill group id: %s", group.ID)
		}
		groupByID[group.ID] = group
		normalized := textcompat.Normalize(group.Name)
		if existing, found := groupNames[normalized]; found {
			return registryError("SKILL_GROUP_NAME_CONFLICT", "Skill group name '%s' conflicts with group '%s'.", group.Name, existing)
		}
		groupNames[normalized] = group.ID
	}
	if err := ValidateTerms(skills); err != nil {
		return err
	}
	skillsByID := make(map[string]Term, len(skills))
	for _, skill := range skills {
		skillsByID[skill.ID] = skill
	}
	type owner struct{ id, name string }
	owners := make(map[string]owner)
	for _, skill := range skills {
		groupID := EffectiveGroupID(skill)
		group, found := groupByID[groupID]
		if !found {
			return registryError("SKILL_GROUP_NOT_FOUND", "Skill '%s' references unknown primary group '%s'.", skill.Name, groupID)
		}
		if group.Archived && !skill.Archived {
			return registryError("SKILL_GROUP_ARCHIVED", "Active skill '%s' cannot use archived group '%s'.", skill.Name, group.Name)
		}
		localNames := make(map[string]bool)
		values := make([]string, 1, len(skill.Aliases)+1)
		values[0] = skill.Name
		values = append(values, skill.Aliases...)
		for _, value := range values {
			normalized := textcompat.Normalize(value)
			if localNames[normalized] {
				return registryError("SKILL_NAME_CONFLICT", "Skill '%s' repeats a name or alias after normalization.", skill.Name)
			}
			localNames[normalized] = true
			if existing, found := owners[normalized]; found && existing.id != skill.ID {
				return registryError("SKILL_NAME_CONFLICT", "Name or alias '%s' conflicts with skill '%s'.", value, existing.name)
			}
			owners[normalized] = owner{id: skill.ID, name: skill.Name}
		}
	}
	for _, group := range groups {
		if len(group.CascadeArchivedSkillIDs) != 0 && !group.Archived {
			return registryError("SKILL_GROUP_CASCADE_INVALID", "Active skill group '%s' retains invalid cascade state.", group.Name)
		}
		for _, skillID := range group.CascadeArchivedSkillIDs {
			skill, found := skillsByID[skillID]
			if !found || EffectiveGroupID(skill) != group.ID || !skill.Archived {
				return registryError("SKILL_GROUP_CASCADE_INVALID", "Skill group '%s' has invalid cascade state for '%s'.", group.Name, skillID)
			}
		}
	}
	return nil
}
