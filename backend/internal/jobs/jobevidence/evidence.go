// Package jobevidence prepares deterministic evidence for job review. It does
// not infer a candidate skill from a JD or publish an assessment verdict.
package jobevidence

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

const MaxTextBytes = 4 << 20
const MaxIndexBytes = 16 << 20
const MaxSources = 20000
const maxNodes = 500000

type Error struct{ Code string }

func (*Error) Error() string                    { return "Job evidence could not be prepared." }
func (e *Error) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, e.Error()) }
func stopped(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return &Error{Code: "EVIDENCE_CANCELED"}
	}
	return nil
}

// Normalize preserves Python casefold + non-word substitution, not NFKC or
// punctuation-preserving skill-registry identity. Unicode tables are pinned.
func Normalize(ctx context.Context, text string) (string, *Error) {
	if err := stopped(ctx); err != nil {
		return "", err
	}
	if len(text) > MaxTextBytes {
		return "", &Error{Code: "EVIDENCE_TEXT_LIMIT"}
	}
	if !utf8.ValidString(text) {
		return "", &Error{Code: "EVIDENCE_TEXT_INVALID"}
	}
	folded := textcompat.Fold(text)
	var out strings.Builder
	out.Grow(min(len(folded), MaxTextBytes))
	separator, count := false, 0
	for _, point := range folded {
		count++
		if count%1024 == 0 {
			if err := stopped(ctx); err != nil {
				return "", err
			}
		}
		if !textcompat.IsWord(point) {
			separator = out.Len() != 0
			continue
		}
		needed := utf8.RuneLen(point)
		if separator {
			needed++
		}
		if needed > MaxTextBytes-out.Len() {
			return "", &Error{Code: "EVIDENCE_TEXT_LIMIT"}
		}
		if separator {
			out.WriteByte(' ')
			separator = false
		}
		out.WriteRune(point)
	}
	if err := stopped(ctx); err != nil {
		return "", err
	}
	return out.String(), nil
}

type flattenState struct {
	budget *walkBudget
	text   strings.Builder
}
type walkBudget struct {
	ctx               context.Context
	nodes, inputBytes int
}

func (state *flattenState) value(value cataloginput.Value, depth int) *Error {
	if err := stopped(state.budget.ctx); err != nil {
		return err
	}
	state.budget.nodes++
	if state.budget.nodes > maxNodes || depth > cataloginput.YAMLMaxDepth {
		return &Error{Code: "EVIDENCE_INPUT_LIMIT"}
	}
	switch value.Kind {
	case "string":
		state.budget.inputBytes += len(value.Text)
		if state.budget.inputBytes > 32<<20 {
			return &Error{Code: "EVIDENCE_INPUT_LIMIT"}
		}
		if !utf8.ValidString(value.Text) {
			return &Error{Code: "EVIDENCE_TEXT_INVALID"}
		}
		if len(value.Text)+1 > MaxTextBytes-state.text.Len() {
			return &Error{Code: "EVIDENCE_TEXT_LIMIT"}
		}
		state.text.WriteString(value.Text)
		state.text.WriteByte(' ')
	case "sequence":
		for _, item := range value.Items {
			if err := state.value(item, depth+1); err != nil {
				return err
			}
		}
	case "mapping":
		// Python traverses values in insertion order and ignores the keys.
		for _, entry := range value.Entries {
			if err := state.value(entry.Value, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// Flatten ignores non-string scalars like the source. Caller-owned typed input
// must remain unchanged during use; recursive/cyclic inputs hit explicit bounds.
func Flatten(ctx context.Context, values []cataloginput.Value) (string, *Error) {
	return flatten(&walkBudget{ctx: ctx}, values)
}
func flatten(budget *walkBudget, values []cataloginput.Value) (string, *Error) {
	state := flattenState{budget: budget}
	for _, value := range values {
		if err := state.value(value, 0); err != nil {
			return "", err
		}
	}
	return Normalize(budget.ctx, state.text.String())
}

type Documents struct {
	Experience, Projects, Education, Skills cataloginput.Value
}
type Source struct {
	Text        string   `json:"text"`
	Kind        string   `json:"kind"`
	EvidenceIDs []string `json:"evidence_ids"`
}
type Index map[string]Source
type Matches struct {
	Direct   []string `json:"direct_evidence_ids"`
	Taxonomy []string `json:"taxonomy_skill_ids"`
}

func field(value cataloginput.Value, key string) (cataloginput.Value, bool) {
	for _, entry := range value.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == key {
			return entry.Value, true
		}
	}
	return cataloginput.Value{Kind: "null"}, false
}
func active(value cataloginput.Value) bool {
	if value.Kind != "mapping" {
		return false
	}
	archived, _ := field(value, "archived")
	// Pure helper parity only. Owned integration must first validate the Vault's
	// stricter boolean/archive/reference contract; no editor validation is skipped.
	return archived.Kind != "bool" || !archived.Boolean
}

// BuildIndex mirrors all four source collections. Full current Vault validation
// belongs to the owned reader, not to this pure helper. Source IDs must be strings;
// Python's arbitrary object-to-string IDs are an explicit unsupported boundary.
func BuildIndex(ctx context.Context, documents Documents) (Index, *Error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	certifications := cataloginput.Value{Kind: "sequence"}
	if documents.Education.Kind == "mapping" {
		certifications, _ = field(documents.Education, "certifications")
	} else {
		prepared := cataloginput.PrepareCatalog(documents.Education, false)
		if prepared.Kind != "sequence" || len(prepared.Items) != 0 {
			return nil, &Error{Code: "EVIDENCE_EDUCATION_SHAPE"}
		}
	}
	index := Index{}
	count, bytes := 0, 0
	budget := &walkBudget{ctx: ctx}
	for _, source := range []struct {
		value        cataloginput.Value
		prefix, kind string
		fields       []string
	}{
		{documents.Experience, "experience", "direct", []string{"company", "role", "category", "technologies", "proof_points"}},
		{documents.Projects, "project", "direct", []string{"name", "category", "summary", "technologies", "proof_points"}},
		{certifications, "certification", "direct", []string{"name", "credential", "proof_points"}},
		{documents.Skills, "skill", "taxonomy", []string{"skill_family", "keywords"}},
	} {
		if source.value.Kind != "sequence" {
			continue
		}
		for _, item := range source.value.Items {
			if err := stopped(ctx); err != nil {
				return nil, err
			}
			count++
			if count > MaxSources {
				return nil, &Error{Code: "EVIDENCE_INDEX_LIMIT"}
			}
			if !active(item) {
				continue
			}
			id, present := field(item, "id")
			if !present {
				return nil, &Error{Code: "EVIDENCE_ID_MISSING"}
			}
			if id.Kind != "string" || !utf8.ValidString(id.Text) {
				return nil, &Error{Code: "EVIDENCE_ID_UNSUPPORTED"}
			}
			if len(id.Text) > 1024 {
				return nil, &Error{Code: "EVIDENCE_ID_LIMIT"}
			}
			values := make([]cataloginput.Value, 0, len(source.fields))
			for _, key := range source.fields {
				value, _ := field(item, key)
				values = append(values, value)
			}
			text, err := flatten(budget, values)
			if err != nil {
				return nil, err
			}
			bytes += len(text) + 2*len(id.Text) + len(source.prefix) + len(source.kind)
			if bytes > MaxIndexBytes {
				return nil, &Error{Code: "EVIDENCE_INDEX_LIMIT"}
			}
			index[source.prefix+":"+id.Text] = Source{Text: text, Kind: source.kind, EvidenceIDs: []string{id.Text}}
		}
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	return index, nil
}

func Find(ctx context.Context, requirement string, index Index) (Matches, *Error) {
	result := Matches{Direct: []string{}, Taxonomy: []string{}}
	text, err := Normalize(ctx, requirement)
	if err != nil {
		return Matches{}, err
	}
	if text == "" {
		return result, nil
	}
	if len(index) > MaxSources {
		return Matches{}, &Error{Code: "EVIDENCE_INDEX_LIMIT"}
	}
	needle := " " + text + " "
	direct, taxonomy := map[string]bool{}, map[string]bool{}
	bytes, ids := 0, 0
	for _, source := range index {
		if err := stopped(ctx); err != nil {
			return Matches{}, err
		}
		bytes += len(source.Text)
		ids += len(source.EvidenceIDs)
		if bytes > MaxIndexBytes || ids > MaxSources {
			return Matches{}, &Error{Code: "EVIDENCE_INDEX_LIMIT"}
		}
		if !utf8.ValidString(source.Text) || (source.Kind != "direct" && source.Kind != "taxonomy") {
			return Matches{}, &Error{Code: "EVIDENCE_INDEX_INVALID"}
		}
		if !strings.Contains(" "+source.Text+" ", needle) {
			continue
		}
		target := direct
		if source.Kind == "taxonomy" {
			target = taxonomy
		}
		for _, id := range source.EvidenceIDs {
			if !utf8.ValidString(id) || len(id) > 1024 {
				return Matches{}, &Error{Code: "EVIDENCE_ID_UNSUPPORTED"}
			}
			target[id] = true
		}
	}
	for id := range direct {
		result.Direct = append(result.Direct, id)
	}
	for id := range taxonomy {
		result.Taxonomy = append(result.Taxonomy, id)
	}
	sort.Strings(result.Direct)
	sort.Strings(result.Taxonomy)
	if err := stopped(ctx); err != nil {
		return Matches{}, err
	}
	return result, nil
}
