package jobevaluation

import (
	"math"
	"sort"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobevidence"
)

func (s *evaluation) validateEvidence() *Error {
	input := s.input
	if len(input.Index) > jobevidence.MaxSources || len(input.DurationMonths) > jobevidence.MaxSources || len(input.Expansions) > jobevidence.MaxSources {
		return &Error{Code: "EVALUATION_EVIDENCE_LIMIT"}
	}
	bytes, ids := 0, 0
	for key, source := range input.Index {
		if err := stopped(s.ctx); err != nil {
			return err
		}
		bytes += len(key) + len(source.Text) + len(source.Kind)
		ids += len(source.EvidenceIDs)
		if !utf8.ValidString(key) || !utf8.ValidString(source.Text) || (source.Kind != "direct" && source.Kind != "taxonomy") {
			return &Error{Code: "EVALUATION_EVIDENCE_INVALID"}
		}
		if bytes > jobevidence.MaxIndexBytes || ids > jobevidence.MaxSources {
			return &Error{Code: "EVALUATION_EVIDENCE_LIMIT"}
		}
		for _, id := range source.EvidenceIDs {
			if !utf8.ValidString(id) || len(id) > 1024 {
				return &Error{Code: "EVALUATION_EVIDENCE_INVALID"}
			}
			bytes += len(id)
		}
		if bytes > jobevidence.MaxIndexBytes {
			return &Error{Code: "EVALUATION_EVIDENCE_LIMIT"}
		}
	}
	// Include node/ID iteration in addition to scanned text; an all-empty index
	// is not free. ID byte cost conservatively accounts for matched-set work.
	s.scanCost = int64(bytes + ids + len(input.Index))
	for id, months := range input.DurationMonths {
		if err := stopped(s.ctx); err != nil {
			return err
		}
		if len(id) > 1024 || !utf8.ValidString(id) || math.IsNaN(months) || math.IsInf(months, 0) || months < 0 || months > 120000 {
			return &Error{Code: "EVALUATION_DURATION_UNSUPPORTED"}
		}
	}
	bytes, terms := 0, 0
	for key, values := range input.Expansions {
		if err := stopped(s.ctx); err != nil {
			return err
		}
		bytes += len(key)
		terms += len(values)
		if terms > 20000 || bytes > jobevidence.MaxIndexBytes {
			return &Error{Code: "EVALUATION_EVIDENCE_LIMIT"}
		}
		if !utf8.ValidString(key) || len(key) > jobevidence.MaxTextBytes {
			return &Error{Code: "EVALUATION_EVIDENCE_INVALID"}
		}
		for _, term := range values {
			bytes += len(term)
			if bytes > jobevidence.MaxIndexBytes {
				return &Error{Code: "EVALUATION_EVIDENCE_LIMIT"}
			}
			if !utf8.ValidString(term) || len(term) > jobevidence.MaxTextBytes {
				return &Error{Code: "EVALUATION_EVIDENCE_INVALID"}
			}
		}
	}
	return nil
}

func evidenceError(err *jobevidence.Error) *Error {
	if err.Code == "EVIDENCE_CANCELED" {
		return &Error{Code: "EVALUATION_CANCELED"}
	}
	if err.Code == "EVIDENCE_QUERY_LIMIT" {
		return &Error{Code: "EVALUATION_QUERY_LIMIT"}
	}
	return &Error{Code: "EVALUATION_EVIDENCE_INVALID"}
}

func (s *evaluation) match(requirement string) (jobevidence.ConceptMatches, *Error) {
	key, failure := jobevidence.Normalize(s.ctx, requirement)
	if failure != nil {
		return jobevidence.ConceptMatches{}, evidenceError(failure)
	}
	if matches, exists := s.memo[key]; exists {
		return matches, nil
	}
	terms := s.input.Expansions[key]
	scans, termBytes := s.input.Concepts.QuerySize(key)
	work := s.scanCost*int64(scans+len(terms)) + int64(termBytes)
	for _, term := range terms {
		work += int64(len(term))
	}
	work += int64(len(requirement))
	if work > MaxQueryWork-s.queryWork {
		return jobevidence.ConceptMatches{}, &Error{Code: "EVALUATION_QUERY_LIMIT"}
	}
	s.queryWork += work
	matches, failure := jobevidence.FindConcepts(s.ctx, requirement, s.input.Index, s.input.Concepts)
	if failure != nil {
		return jobevidence.ConceptMatches{}, evidenceError(failure)
	}
	if len(terms) != 0 {
		direct, taxonomy := stringSet{}, stringSet{}
		direct.add(matches.Direct)
		taxonomy.add(matches.Taxonomy)
		for _, term := range terms {
			extra, err := jobevidence.Find(s.ctx, term, s.input.Index)
			if err != nil {
				return jobevidence.ConceptMatches{}, evidenceError(err)
			}
			direct.add(extra.Direct)
			taxonomy.add(extra.Taxonomy)
		}
		matches.Direct, matches.Taxonomy = direct.sorted(), taxonomy.sorted()
	}
	// Memoization is per evaluation and normalized label only; never shared
	// across jobs, profiles, catalog revisions or evaluation months.
	if err := s.memoBudget.reserve(matches); err != nil {
		if err.Code == "EVALUATION_CANCELED" {
			return jobevidence.ConceptMatches{}, err
		}
		return jobevidence.ConceptMatches{}, &Error{Code: "EVALUATION_MEMO_LIMIT"}
	}
	s.memo[key] = matches
	return matches, nil
}

type stringSet map[string]bool

func (set stringSet) add(values []string) {
	for _, value := range values {
		set[value] = true
	}
}
func (set stringSet) sorted() []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
