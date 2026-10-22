package catalog

import "sort"

// MergeForMutation is the source pre-write id merge, not core reconciliation.
// Mutation validation must not silently resolve a new naming collision as if
// it came from a shipped core update. Inputs are validated catalog models.
func MergeForMutation(core, private []Term) ([]Term, error) {
	byID := make(map[string]Term, len(core)+len(private))
	for _, term := range core {
		if _, exists := byID[term.ID]; exists {
			return nil, graphError("Shared catalog contains duplicate technology ids")
		}
		byID[term.ID] = cloneTerm(term)
	}
	for _, term := range private {
		if existing, present := byID[term.ID]; present {
			byID[term.ID] = mergeTerm(existing, term)
		} else {
			byID[term.ID] = cloneTerm(term)
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	merged := make([]Term, 0, len(ids))
	for _, id := range ids {
		merged = append(merged, byID[id])
	}
	if e := ValidateTerms(merged); e != nil {
		return nil, e
	}
	return merged, nil
}
