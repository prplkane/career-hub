package jobevidence

import (
	"context"
	"sort"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

const MaxConcepts = 4096
const MaxConceptTerms = 256
const maxQueryWork = 128 << 20

type concept struct {
	id    string
	terms []string
}

// Concepts is an immutable, validated projection of the reviewed overlay, not
// a learned synonym table. Unknown source metadata is not used for matching.
type Concepts struct {
	items   []concept
	byAlias map[string]int
}

// QuerySize describes the exact-index scans and evidence-term bytes normalized
// by FindConcepts for an already normalized requirement. Batch callers bound
// both, including on an empty index; no mutable overlay data is exposed.
func (concepts *Concepts) QuerySize(normalizedRequirement string) (scans, termBytes int) {
	if concepts != nil {
		if position, exists := concepts.byAlias[normalizedRequirement]; exists {
			terms := concepts.items[position].terms
			for _, term := range terms {
				termBytes += len(term)
			}
			return len(terms) + 1, termBytes
		}
	}
	return 1, 0
}

// LoadConcepts accepts the optional overlay's decoded value. Missing/falsey
// documents mean no overlay, as in the source; malformed entries fail atomically.
// Static errors deliberately do not reproduce source prose containing aliases.
func LoadConcepts(ctx context.Context, value cataloginput.Value) (*Concepts, *Error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	value = cataloginput.PrepareCatalog(value, false)
	if value.Kind != "sequence" {
		return nil, &Error{Code: "EVIDENCE_CONCEPT_INVALID"}
	}
	if len(value.Items) > MaxConcepts {
		return nil, &Error{Code: "EVIDENCE_CONCEPT_LIMIT"}
	}
	out := &Concepts{items: []concept{}, byAlias: map[string]int{}}
	seen := map[string]bool{}
	bytes, count := 0, 0
	for _, item := range value.Items {
		if err := stopped(ctx); err != nil {
			return nil, err
		}
		if item.Kind != "mapping" {
			return nil, &Error{Code: "EVIDENCE_CONCEPT_INVALID"}
		}
		count += len(item.Entries)
		if count > MaxSources {
			return nil, &Error{Code: "EVIDENCE_CONCEPT_LIMIT"}
		}
		id, _ := field(item, "id")
		if id.Kind != "string" || !utf8.ValidString(id.Text) || textcompat.Strip(id.Text) == "" || seen[id.Text] {
			return nil, &Error{Code: "EVIDENCE_CONCEPT_INVALID"}
		}
		if len(id.Text) > 1024 {
			return nil, &Error{Code: "EVIDENCE_CONCEPT_LIMIT"}
		}
		seen[id.Text] = true
		bytes += len(id.Text)
		current := concept{id: id.Text, terms: []string{}}
		for _, key := range []string{"aliases", "evidence_terms"} {
			values, _ := field(item, key)
			if values.Kind != "sequence" || len(values.Items) == 0 {
				return nil, &Error{Code: "EVIDENCE_CONCEPT_INVALID"}
			}
			if len(values.Items) > MaxConceptTerms {
				return nil, &Error{Code: "EVIDENCE_CONCEPT_LIMIT"}
			}
			for _, entry := range values.Items {
				if entry.Kind != "string" || !utf8.ValidString(entry.Text) || textcompat.Strip(entry.Text) == "" {
					return nil, &Error{Code: "EVIDENCE_CONCEPT_INVALID"}
				}
				bytes += len(entry.Text)
				count++
				if bytes > MaxIndexBytes || count > MaxSources {
					return nil, &Error{Code: "EVIDENCE_CONCEPT_LIMIT"}
				}
				alias, err := Normalize(ctx, entry.Text)
				if err != nil {
					return nil, err
				}
				if key == "aliases" {
					if previous, exists := out.byAlias[alias]; exists && previous != len(out.items) {
						return nil, &Error{Code: "EVIDENCE_CONCEPT_INVALID"}
					}
					out.byAlias[alias] = len(out.items)
				} else {
					current.terms = append(current.terms, entry.Text)
				}
			}
		}
		out.items = append(out.items, current)
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

type ConceptMatches struct {
	Matches
	ConceptIDs []string `json:"concept_ids"`
}

// FindConcepts expands only an exact normalized reviewed alias. It never turns
// taxonomy hints into direct evidence. A nil overlay is the valid empty overlay.
func FindConcepts(ctx context.Context, requirement string, index Index, concepts *Concepts) (ConceptMatches, *Error) {
	exact, err := Find(ctx, requirement, index)
	if err != nil {
		return ConceptMatches{}, err
	}
	out := ConceptMatches{Matches: exact, ConceptIDs: []string{}}
	text, err := Normalize(ctx, requirement)
	if err != nil {
		return ConceptMatches{}, err
	}
	if concepts == nil {
		return out, nil
	}
	position, found := concepts.byAlias[text]
	if !found {
		return out, nil
	}
	matched := concepts.items[position]
	// A validated overlay has at most one owner for any normalized alias.
	// Bound aggregate scans, not each term independently, without partial matches.
	bytes := 0
	if len(index) > MaxSources {
		return ConceptMatches{}, &Error{Code: "EVIDENCE_INDEX_LIMIT"}
	}
	for _, source := range index {
		bytes += len(source.Text) + len(source.EvidenceIDs)
		if bytes > MaxIndexBytes+MaxSources {
			return ConceptMatches{}, &Error{Code: "EVIDENCE_INDEX_LIMIT"}
		}
	}
	if bytes > maxQueryWork/(len(matched.terms)+1) {
		return ConceptMatches{}, &Error{Code: "EVIDENCE_QUERY_LIMIT"}
	}
	direct, taxonomy := map[string]bool{}, map[string]bool{}
	for _, id := range exact.Direct {
		direct[id] = true
	}
	for _, id := range exact.Taxonomy {
		taxonomy[id] = true
	}
	for _, term := range matched.terms {
		matches, err := Find(ctx, term, index)
		if err != nil {
			return ConceptMatches{}, err
		}
		for _, id := range matches.Direct {
			direct[id] = true
		}
		for _, id := range matches.Taxonomy {
			taxonomy[id] = true
		}
	}
	out.Direct, out.Taxonomy = []string{}, []string{}
	for id := range direct {
		out.Direct = append(out.Direct, id)
	}
	for id := range taxonomy {
		out.Taxonomy = append(out.Taxonomy, id)
	}
	sort.Strings(out.Direct)
	sort.Strings(out.Taxonomy)
	out.ConceptIDs = append(out.ConceptIDs, matched.id)
	if err := stopped(ctx); err != nil {
		return ConceptMatches{}, err
	}
	return out, nil
}
