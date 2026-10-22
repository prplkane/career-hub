package catalog

import (
	"fmt"

	"careerhub.local/backend/internal/skills/resolver"
	"careerhub.local/backend/internal/textcompat"
)

func lookupError(message string) error {
	return &Error{Class: "SkillLookupError", Message: message}
}

// CleanLookupTerms is the pure input stage of lookup_skill_terms. External
// adapters must call it before loading/building a registry to preserve Python's
// term-error precedence when both input and catalog are invalid.
func CleanLookupTerms(texts []string) ([]string, error) {
	cleaned := make([]string, 0, len(texts))
	for i, text := range texts {
		value := textcompat.Strip(text)
		if value == "" {
			return nil, lookupError(fmt.Sprintf("term %d must not be blank", i+1))
		}
		cleaned = append(cleaned, value)
	}
	if len(cleaned) == 0 {
		return nil, lookupError("at least one --term is required")
	}
	return cleaned, nil
}

func (s *Snapshot) lookupTermPayload(term Term, groupNames map[string]string) (map[string]any, error) {
	groupID := EffectiveGroupID(term)
	groupName, exists := groupNames[groupID]
	if !exists {
		return nil, lookupError(fmt.Sprintf("Skill '%s' references an unknown group '%s'", term.Name, groupID))
	}
	return map[string]any{
		"id": term.ID, "canonical_name": term.Name, "origin": s.SkillOrigin(term.ID),
		"category": term.Category, "kind": term.Kind, "primary_domain": optionalString(term.PrimaryDomain),
		"primary_group": map[string]any{"id": groupID, "name": groupName, "origin": s.GroupOrigin(groupID)},
		"archived":      term.Archived,
	}, nil
}

func recommendedAction(status resolver.Status, archivedIDs []string) string {
	switch status {
	case resolver.ExactMatch, resolver.AliasMatch:
		return "reuse_existing"
	case resolver.Ambiguous:
		return "review_ambiguous"
	}
	if len(archivedIDs) != 0 {
		return "review_archived_before_adding"
	}
	return "review_before_adding"
}

// Lookup is the in-memory portion of Python lookup_skill_terms. Typed string
// input, a validated snapshot and scalar UTF-8 are caller contracts; iterable
// coercion, managed Data Home loading and CLI behavior remain external adapters.
func (s *Snapshot) Lookup(texts []string) (map[string]any, error) {
	requested, err := CleanLookupTerms(texts)
	if err != nil {
		return nil, err
	}
	termsByID := make(map[string]Term, len(s.skills))
	resolverTerms := make([]resolver.Term, 0, len(s.skills))
	for _, term := range s.skills {
		termsByID[term.ID] = term
		resolverTerms = append(resolverTerms, resolver.Term{ID: term.ID, Name: term.Name, Aliases: term.Aliases, Archived: term.Archived})
	}
	groupNames := make(map[string]string, len(s.groups))
	for _, group := range s.groups {
		groupNames[group.ID] = group.Name
	}
	index, err := resolver.New(resolverTerms)
	if err != nil {
		return nil, &Error{Class: "ValueError", Message: err.Error()}
	}
	counts := map[string]any{
		string(resolver.ExactMatch): 0, string(resolver.AliasMatch): 0,
		string(resolver.Ambiguous): 0, string(resolver.Unmatched): 0, string(resolver.InvalidReference): 0,
	}
	results := make([]any, 0, len(requested))
	for _, source := range requested {
		resolution := index.Resolve(source)
		status := string(resolution.Status)
		counts[status] = counts[status].(int) + 1
		var match any
		if resolution.CompetencyID != nil {
			match, err = s.lookupTermPayload(termsByID[*resolution.CompetencyID], groupNames)
			if err != nil {
				return nil, err
			}
		}
		candidates := make([]any, 0, len(resolution.CandidateIDs))
		for _, id := range resolution.CandidateIDs {
			candidate, err := s.lookupTermPayload(termsByID[id], groupNames)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, candidate)
		}
		archived := make([]any, 0, len(resolution.ArchivedCandidateIDs))
		for _, id := range resolution.ArchivedCandidateIDs {
			candidate, err := s.lookupTermPayload(termsByID[id], groupNames)
			if err != nil {
				return nil, err
			}
			archived = append(archived, candidate)
		}
		results = append(results, map[string]any{
			"source_text": source, "normalized": resolution.Normalized, "status": status,
			"match": match, "candidates": candidates, "archived_candidates": archived,
			"recommended_action": recommendedAction(resolution.Status, resolution.ArchivedCandidateIDs),
		})
	}
	return map[string]any{
		"schema_version": 1, "read_only": true,
		"registry": map[string]any{
			"group_count": len(s.groups), "skill_count": len(s.skills),
			"revision": s.revision, "catalog_revision": s.catalogRevision,
			"compatibility_notice_count": len(s.notices),
		},
		"summary": map[string]any{"input_count": len(requested), "status_counts": counts},
		"results": results,
	}, nil
}
