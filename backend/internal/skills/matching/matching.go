// Package matching recognizes registered literal skill phrases in a source
// block. It does not discover vocabulary, infer synonyms or candidate evidence,
// load a catalog, or change user data. Normalize only search keys, never quotes.
package matching

import (
	"context"
	"errors"
	"sort"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/resolver"
	"careerhub.local/backend/internal/textcompat"
)

const Extractor = "registered_literal_v1"

// These are aggregate refusal envelopes, not truncation settings. Use FindBlocks
// for a complete JD; it shares input/work/output budgets across all blocks.
const (
	MaxSourceBytes  = 4 << 20
	MaxSourceRunes  = 500000
	MaxBlocks       = 10000
	MaxCatalogBytes = 4 << 20
	MaxTerms        = 10000
	MaxValues       = 50000
	MaxValueBytes   = 4096
	MaxIDBytes      = 512
	MaxNodes        = 250000
	MaxCandidates   = 32768
	MaxResults      = 4096
	MaxWorkBytes    = 32 << 20
	MaxOutputBytes  = 16 << 20
)

var (
	ErrInput = errors.New("invalid registered-skill matching input")
	ErrLimit = errors.New("registered-skill matching limit exceeded")
)

type SourceRef struct {
	SourceID string `json:"source_id"`
	BlockID  string `json:"block_id"`
	Start    int    `json:"start"` // Unicode codepoint offsets, not bytes or UTF-16.
	End      int    `json:"end"`
}

type Mention struct {
	TechnologyID  string    `json:"technology_id"`
	CanonicalName string    `json:"canonical_name"`
	SourceText    string    `json:"source_text"`
	SourceRef     SourceRef `json:"source_ref"`
	Extractor     string    `json:"extractor"`
}

type Issue struct {
	Status               resolver.Status `json:"status"`
	SourceText           string          `json:"source_text"`
	SourceRef            SourceRef       `json:"source_ref"`
	CandidateIDs         []string        `json:"candidate_ids"`
	ArchivedCandidateIDs []string        `json:"archived_candidate_ids"`
}

type Result struct {
	Mentions []Mention `json:"mentions"`
	Issues   []Issue   `json:"issues"`
}

type Block struct{ ID, Text string }

// FindBlocks is the document-wide recognition boundary. Results correspond to
// the supplied blocks in order, including empty results. A late malformed block,
// cancellation or aggregate limit publishes none of the earlier block results.
// It does not join lines: a phrase cannot span two distinct source blocks.
func (m *Matcher) FindBlocks(ctx context.Context, sourceID string, blocks []Block) ([]Result, error) {
	if ctx == nil || m == nil || m.resolver == nil || !validID(sourceID) {
		return nil, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(blocks) > MaxBlocks {
		return nil, ErrLimit
	}
	bytes, points := 0, 0
	seen := make(map[string]struct{}, len(blocks))
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validID(block.ID) {
			return nil, ErrInput
		}
		if _, exists := seen[block.ID]; exists {
			return nil, ErrInput
		}
		seen[block.ID] = struct{}{}
		bytes += len(block.Text)
		if bytes > MaxSourceBytes {
			return nil, ErrLimit
		}
		if !utf8.ValidString(block.Text) {
			return nil, ErrInput
		}
		points += utf8.RuneCountInString(block.Text)
		if points > MaxSourceRunes {
			return nil, ErrLimit
		}
	}
	budget := &scanBudget{}
	results := make([]Result, 0, len(blocks))
	for _, block := range blocks {
		result, err := m.find(ctx, sourceID, block.ID, block.Text, budget)
		if err != nil {
			return nil, err
		}
		results = append(results, *result)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

type scanBudget struct{ visits, workBytes, outputBytes, results int }

type node struct {
	next   map[rune]int
	fail   int
	output int // Nearest terminal failure ancestor; no duplicated output lists.
	length int // Terminal normalized phrase length; zero means nonterminal.
}

// Matcher snapshots its input and is immutable, safe for concurrent Find calls.
// Callers must not mutate input slices during New. Use New for an empty catalog;
// a nil/uninitialized matcher is a programming error reported as ErrInput.
type Matcher struct {
	nodes    []node
	resolver *resolver.Resolver
	names    map[string]string
}

// New builds a bounded Aho-Corasick automaton. Canonical/alias collisions remain
// resolver ambiguity, and archived-only phrases remain issues, not active skills.
func New(ctx context.Context, terms []resolver.Term) (*Matcher, error) {
	if ctx == nil {
		return nil, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(terms) > MaxTerms {
		return nil, ErrLimit
	}
	m := &Matcher{nodes: []node{{}}, names: make(map[string]string, len(terms))}
	bytes, values := 0, 0
	keys := make(map[string]struct{})
	for _, term := range terms {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validID(term.ID) {
			return nil, ErrInput
		}
		if _, exists := m.names[term.ID]; exists {
			return nil, ErrInput
		}
		m.names[term.ID] = term.Name
		bytes += len(term.ID)
		if len(term.Aliases) >= MaxValues || values+len(term.Aliases)+1 > MaxValues {
			return nil, ErrLimit
		}
		// Do not append to the caller's backing array or retain mutable aliases.
		for i := -1; i < len(term.Aliases); i++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			value := term.Name
			if i >= 0 {
				value = term.Aliases[i]
			}
			values++
			bytes += len(value)
			if bytes > MaxCatalogBytes || len(value) > MaxValueBytes {
				return nil, ErrLimit
			}
			if !utf8.ValidString(value) {
				return nil, ErrInput
			}
			key := textcompat.Normalize(value)
			if key == "" {
				return nil, ErrInput
			}
			if len(key) > MaxValueBytes {
				return nil, ErrLimit
			}
			if _, exists := keys[key]; exists {
				continue
			}
			keys[key] = struct{}{}
			if err := m.add(ctx, key); err != nil {
				return nil, err
			}
		}
	}
	if err := m.link(ctx); err != nil {
		return nil, err
	}
	// Resolver construction is bounded above by the validated aggregate catalog;
	// check cancellation before and after its non-contextual snapshot operation.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := resolver.New(terms)
	if err != nil {
		return nil, ErrInput
	}
	m.resolver = r
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

func validID(value string) bool {
	return len(value) > 0 && len(value) <= MaxIDBytes && utf8.ValidString(value) && textcompat.Strip(value) != ""
}

func (m *Matcher) add(ctx context.Context, key string) error {
	state, length := 0, 0
	for _, point := range key {
		if err := ctx.Err(); err != nil {
			return err
		}
		length++
		next, ok := m.nodes[state].next[point]
		if !ok {
			if len(m.nodes) == MaxNodes {
				return ErrLimit
			}
			next = len(m.nodes)
			m.nodes = append(m.nodes, node{})
			if m.nodes[state].next == nil {
				m.nodes[state].next = make(map[rune]int)
			}
			m.nodes[state].next[point] = next
		}
		state = next
	}
	m.nodes[state].length = length
	return nil
}

func (m *Matcher) link(ctx context.Context) error {
	queue := make([]int, 0, len(m.nodes))
	for _, child := range m.nodes[0].next {
		queue = append(queue, child)
	}
	for head := 0; head < len(queue); head++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		state := queue[head]
		for point, child := range m.nodes[state].next {
			if err := ctx.Err(); err != nil {
				return err
			}
			failure := m.nodes[state].fail
			for failure != 0 && m.nodes[failure].next[point] == 0 {
				failure = m.nodes[failure].fail
			}
			failure = m.nodes[failure].next[point]
			m.nodes[child].fail = failure
			m.nodes[child].output = m.nodes[failure].output
			if m.nodes[failure].length > 0 {
				m.nodes[child].output = failure
			}
			queue = append(queue, child)
		}
	}
	return ctx.Err()
}

type candidate struct {
	start, end         int
	byteStart, byteEnd int
	resolution         resolver.Resolution
}

// Find returns an all-or-nothing recognition result for one original block.
// It intentionally does not repair punctuation, HTML, mojibake or normalize
// evidence text. The caller owns raw-JD line mapping and retained-source checks.
func (m *Matcher) Find(ctx context.Context, sourceID, blockID, source string) (*Result, error) {
	return m.find(ctx, sourceID, blockID, source, &scanBudget{})
}

func (m *Matcher) find(ctx context.Context, sourceID, blockID, source string, budget *scanBudget) (*Result, error) {
	if ctx == nil || m == nil || m.resolver == nil || !validID(sourceID) || !validID(blockID) {
		return nil, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source) > MaxSourceBytes {
		return nil, ErrLimit
	}
	if !utf8.ValidString(source) {
		return nil, ErrInput
	}
	if utf8.RuneCountInString(source) > MaxSourceRunes {
		return nil, ErrLimit
	}
	result := &Result{Mentions: []Mention{}, Issues: []Issue{}}
	if source == "" || len(m.nodes) == 1 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return result, nil
	}
	text, err := normalize(ctx, source)
	if err != nil {
		return nil, err
	}
	accepted, issues, err := m.search(ctx, source, text, budget)
	if err != nil {
		return nil, err
	}
	// Longest original codepoint span first, then source order. This is a
	// versioned literal contract, not a claim to reproduce spaCy token counts.
	sort.Slice(accepted, func(i, j int) bool {
		a, b := accepted[i], accepted[j]
		if a.end-a.start != b.end-b.start {
			return a.end-a.start > b.end-b.start
		}
		return a.start < b.start
	})
	used := make(occupancy, (len(text.original)+63)/64)
	selected := make([]candidate, 0, min(len(accepted), MaxResults))
	for _, item := range accepted {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if used.overlaps(item.start, item.end) {
			continue
		}
		if budget.results == MaxResults {
			return nil, ErrLimit
		}
		budget.results++
		used.mark(item.start, item.end)
		selected = append(selected, item)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].start < selected[j].start })
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].start != issues[j].start {
			return issues[i].start < issues[j].start
		}
		return issues[i].end < issues[j].end
	})
	for _, item := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := *item.resolution.CompetencyID
		quote := source[item.byteStart:item.byteEnd]
		budget.outputBytes += len(quote) + len(id) + len(m.names[id]) + len(sourceID) + len(blockID) + len(Extractor)
		if budget.outputBytes > MaxOutputBytes {
			return nil, ErrLimit
		}
		result.Mentions = append(result.Mentions, Mention{id, m.names[id], quote, SourceRef{sourceID, blockID, item.start, item.end}, Extractor})
	}
	for _, item := range issues {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if used.overlaps(item.start, item.end) {
			continue
		}
		if budget.results == MaxResults {
			return nil, ErrLimit
		}
		budget.results++
		r := item.resolution
		quote := source[item.byteStart:item.byteEnd]
		budget.outputBytes += len(quote) + len(sourceID) + len(blockID) + len(r.Status)
		for _, id := range r.CandidateIDs {
			budget.outputBytes += len(id)
		}
		for _, id := range r.ArchivedCandidateIDs {
			budget.outputBytes += len(id)
		}
		if budget.outputBytes > MaxOutputBytes {
			return nil, ErrLimit
		}
		result.Issues = append(result.Issues, Issue{r.Status, quote, SourceRef{sourceID, blockID, item.start, item.end}, r.CandidateIDs, r.ArchivedCandidateIDs})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (m *Matcher) search(ctx context.Context, source string, text normalizedText, budget *scanBudget) ([]candidate, []candidate, error) {
	accepted, issues := []candidate{}, []candidate{}
	state := 0
	for offset, current := range text.units {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		for state != 0 && m.nodes[state].next[current.point] == 0 {
			state = m.nodes[state].fail
		}
		state = m.nodes[state].next[current.point]
		terminal := state
		if m.nodes[terminal].length == 0 {
			terminal = m.nodes[terminal].output
		}
		for ; terminal != 0; terminal = m.nodes[terminal].output {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			budget.visits++
			if budget.visits > MaxCandidates {
				return nil, nil, ErrLimit
			}
			first := text.units[offset+1-m.nodes[terminal].length]
			if !first.first || !current.last || !text.left[first.charStart] || !text.right[current.charEnd] {
				continue
			}
			start, end := int(first.byteStart), int(current.byteEnd)
			budget.workBytes += end - start
			if budget.workBytes > MaxWorkBytes {
				return nil, nil, ErrLimit
			}
			r := m.resolver.Resolve(source[start:end])
			// A resolver result may repeat large ambiguity lists many times. Count
			// these before retaining it, even when a later longest span hides it.
			budget.workBytes += len(r.Normalized)
			for _, id := range r.CandidateIDs {
				budget.workBytes += len(id)
			}
			for _, id := range r.ArchivedCandidateIDs {
				budget.workBytes += len(id)
			}
			if budget.workBytes > MaxWorkBytes {
				return nil, nil, ErrLimit
			}
			item := candidate{int(first.charStart), int(current.charEnd), start, end, r}
			if r.CompetencyID != nil {
				accepted = append(accepted, item)
			} else if r.Status == resolver.Ambiguous || len(r.ArchivedCandidateIDs) > 0 {
				if r.Status == resolver.Ambiguous {
					item.resolution.ArchivedCandidateIDs = []string{}
				}
				issues = append(issues, item)
			}
		}
	}
	return accepted, issues, nil
}
