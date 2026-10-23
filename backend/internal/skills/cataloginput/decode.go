package cataloginput

import "careerhub.local/backend/internal/skills/catalog"

// DecodeError identifies the failed read-only byte boundary without retaining
// source bytes, paths or the dependency's rendered exception. A product file/API
// adapter must deliberately map it to its existing public diagnostic contract.
// Model locations and DuplicateID may contain source labels; do not log them as
// if the structured payload were globally redacted.
type DecodeError struct {
	Stage       string
	YAML        *YAMLError
	Model       *ValidationError
	Code        string
	DuplicateID string
}

func (e *DecodeError) Error() string { return "catalog input could not be decoded" }

// DecodeTerms decodes one owned UTF-8 byte snapshot without opening files. It
// preserves Python's preparation, whole-list validation, nonempty check and
// duplicate-ID check in that order. It does not check graph/registry invariants.
// The caller owns any managed-path checks, raw revision and writer coordination.
func DecodeTerms(raw []byte, allowEmpty bool) ([]catalog.Term, *DecodeError) {
	value, parseErr := ParseYAML(raw)
	if parseErr != nil {
		return nil, &DecodeError{Stage: "yaml", YAML: parseErr}
	}
	terms, modelErr := ValidateTerms(PrepareCatalog(value, true))
	if modelErr != nil {
		return nil, &DecodeError{Stage: "model", Model: modelErr}
	}
	if len(terms) == 0 && !allowEmpty {
		return nil, &DecodeError{Stage: "catalog", Code: "CATALOG_EMPTY"}
	}
	seen := make(map[string]bool, len(terms))
	for _, term := range terms {
		if seen[term.ID] {
			return nil, &DecodeError{Stage: "catalog", Code: "CATALOG_DUPLICATE_ID", DuplicateID: term.ID}
		}
		seen[term.ID] = true
	}
	return terms, nil
}

// DecodeGroups has no per-file duplicate-ID guard: the Python group loader
// leaves that check to effective-registry validation after reconciliation.
func DecodeGroups(raw []byte, allowEmpty bool) ([]catalog.Group, *DecodeError) {
	value, parseErr := ParseYAML(raw)
	if parseErr != nil {
		return nil, &DecodeError{Stage: "yaml", YAML: parseErr}
	}
	groups, modelErr := ValidateGroups(PrepareCatalog(value, false))
	if modelErr != nil {
		return nil, &DecodeError{Stage: "model", Model: modelErr}
	}
	if len(groups) == 0 && !allowEmpty {
		return nil, &DecodeError{Stage: "catalog", Code: "CATALOG_EMPTY"}
	}
	return groups, nil
}
