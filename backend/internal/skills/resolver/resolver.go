// Package resolver resolves complete competency names, aliases and historical
// IDs against an immutable in-memory catalog. A match is vocabulary, not evidence
// that the candidate has a skill. The package does not load or change catalogs.
package resolver

import (
	"errors"
	"sort"

	"careerhub.local/backend/internal/textcompat"
)

// ErrDuplicateID matches the Python resolver's constructor failure. The error
// deliberately does not include a term or user-provided catalog content.
var ErrDuplicateID = errors.New("Duplicate technology id")

// Term is the resolver's projection of an already-validated catalog record.
// Model validation, catalog merging and valid UTF-8 input belong to the caller.
type Term struct {
	ID       string
	Name     string
	Aliases  []string
	Archived bool
}

type Status string

const (
	ExactMatch       Status = "exact_match"
	AliasMatch       Status = "alias_match"
	Ambiguous        Status = "ambiguous"
	Unmatched        Status = "unmatched"
	InvalidReference Status = "invalid_reference"
)

// Resolution preserves source text and the Python TechnologyResolution fields.
// Methods always return nonnil candidate slices, and an absent ID encodes as null.
type Resolution struct {
	SourceText           string   `json:"source_text"`
	Normalized           string   `json:"normalized"`
	Status               Status   `json:"status"`
	CandidateIDs         []string `json:"candidate_ids"`
	ArchivedCandidateIDs []string `json:"archived_candidate_ids"`
	CompetencyID         *string  `json:"competency_id"`
}

type indexedTerm struct {
	normalizedName string
	archived       bool
}

// Resolver is immutable after New returns and supports concurrent resolution.
// Inputs must not be mutated during New; later input and result mutations cannot
// change the resolver. The zero value behaves like an empty catalog.
type Resolver struct {
	terms         map[string]indexedTerm
	activeIndex   map[string][]string
	archivedIndex map[string][]string
}

// New snapshots the fields required by resolution. It rejects repeated IDs, but
// does not reject cross-term name/alias collisions: these resolve as ambiguous.
// All input strings must contain valid UTF-8 Unicode scalar values.
func New(terms []Term) (*Resolver, error) {
	r := &Resolver{terms: make(map[string]indexedTerm, len(terms))}
	for _, term := range terms {
		if _, exists := r.terms[term.ID]; exists {
			return nil, ErrDuplicateID
		}
		r.terms[term.ID] = indexedTerm{
			normalizedName: textcompat.Normalize(term.Name),
			archived:       term.Archived,
		}
	}

	active := make(map[string]map[string]struct{})
	archived := make(map[string]map[string]struct{})
	for _, term := range terms {
		index := active
		if term.Archived {
			index = archived
		}
		addCandidate(index, r.terms[term.ID].normalizedName, term.ID)
		for _, alias := range term.Aliases {
			addCandidate(index, textcompat.Normalize(alias), term.ID)
		}
	}
	r.activeIndex = sortedIndex(active)
	r.archivedIndex = sortedIndex(archived)
	return r, nil
}

func addCandidate(index map[string]map[string]struct{}, normalized, id string) {
	if index[normalized] == nil {
		index[normalized] = make(map[string]struct{})
	}
	index[normalized][id] = struct{}{}
}

func sortedIndex(sets map[string]map[string]struct{}) map[string][]string {
	index := make(map[string][]string, len(sets))
	for normalized, ids := range sets {
		ordered := make([]string, 0, len(ids))
		for id := range ids {
			ordered = append(ordered, id)
		}
		sort.Strings(ordered)
		index[normalized] = ordered
	}
	return index
}

func copyIDs(ids []string) []string {
	result := make([]string, len(ids))
	copy(result, ids)
	return result
}

// Resolve matches the entire normalized source, never a substring or inferred
// synonym. Archived terms remain separate and cannot create an active match.
// Source must contain valid UTF-8 Unicode scalar values.
func (r *Resolver) Resolve(source string) Resolution {
	normalized := textcompat.Normalize(source)
	result := Resolution{
		SourceText:           source,
		Normalized:           normalized,
		Status:               Unmatched,
		CandidateIDs:         copyIDs(r.activeIndex[normalized]),
		ArchivedCandidateIDs: copyIDs(r.archivedIndex[normalized]),
	}
	switch len(result.CandidateIDs) {
	case 0:
		return result
	case 1:
		id := result.CandidateIDs[0]
		result.CompetencyID = &id
		result.Status = AliasMatch
		if r.terms[id].normalizedName == normalized {
			result.Status = ExactMatch
		}
	default:
		result.Status = Ambiguous
	}
	return result
}

// ResolveReference looks up the exact, unnormalized ID, including an archived
// historical term. Case and surrounding whitespace are significant here.
// ID must contain valid UTF-8 Unicode scalar values.
func (r *Resolver) ResolveReference(id string) Resolution {
	result := Resolution{
		SourceText:           id,
		Normalized:           id,
		Status:               InvalidReference,
		CandidateIDs:         []string{},
		ArchivedCandidateIDs: []string{},
	}
	term, exists := r.terms[id]
	if !exists {
		return result
	}
	result.Status = ExactMatch
	result.CandidateIDs = append(result.CandidateIDs, id)
	result.CompetencyID = &id
	if term.archived {
		result.ArchivedCandidateIDs = append(result.ArchivedCandidateIDs, id)
	}
	return result
}
