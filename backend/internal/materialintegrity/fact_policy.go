package materialintegrity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/fixedregex"
	"careerhub.local/backend/internal/skills/matching"
	"careerhub.local/backend/internal/skills/resolver"
	"careerhub.local/backend/internal/textcompat"
)

const (
	MaxFactBytes       = 4 << 20
	MaxFactTokens      = 4096
	MaxFactWork        = 128 << 20
	numberPattern      = `(?:[$€£]\s*)?[+-]?\d+(?:[.,]\d+)*(?:[-/]\d+(?:[-/]\d+)?)?(?:\s*(?:%|[kKmMbB]\b))?`
	datePattern        = `\b(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\.?\s+(?:\d{1,2}(?:,\s*|\s+))?\d{4}\b`
	placeholderPattern = `(?:\[(?:company|role|position|add|insert|todo|tbd|metric|personalize)[^\]]*\]|<(?:company|role|position|add|insert|todo|tbd|metric|personalize)[^>]*>|\b(?:TODO|TBD)\b)`
)

// NamedTerm is configured vocabulary to CHECK, not candidate evidence. DemandID
// optionally identifies an exact JD technology for the separate coverage aid.
type NamedTerm struct{ Kind, Text, DemandID string }

func (NamedTerm) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted fact term]") }

type FactPolicy struct {
	matcher *matching.Matcher
	terms   map[string]NamedTerm
	digest  string
}

// Static reviewed patterns only: caller text is never compiled as regex.
var factPatterns = fixedregex.MustCompile(map[string]fixedregex.Definition{
	"number":      {Pattern: numberPattern},
	"date":        {Pattern: datePattern, IgnoreCase: true},
	"placeholder": {Pattern: placeholderPattern, IgnoreCase: true},
})

func NewFactPolicy(ctx context.Context, terms []NamedTerm) (*FactPolicy, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("MATERIAL_INTEGRITY_CANCELED")
	}
	if len(terms) > matching.MaxTerms {
		return nil, failure("MATERIAL_FACT_POLICY_LIMIT")
	}
	items := make([]NamedTerm, 0, len(terms))
	seen := map[string]NamedTerm{}
	bytes := 0
	for _, term := range terms {
		bytes += len(term.Text) + len(term.Kind) + len(term.DemandID)
		if bytes > matching.MaxCatalogBytes || len(term.Text) > matching.MaxValueBytes {
			return nil, failure("MATERIAL_FACT_POLICY_LIMIT")
		}
		if !slices.Contains([]string{"employer", "title", "technology", "date", "other"}, term.Kind) ||
			!utf8.ValidString(term.Text) || strings.ContainsRune(term.Text, 0) ||
			term.DemandID != "" && (term.Kind != "technology" || !identifier(term.DemandID)) {
			return nil, failure("MATERIAL_FACT_POLICY_INVALID")
		}
		term.Text = textcompat.Normalize(term.Text)
		if term.Text == "" {
			return nil, failure("MATERIAL_FACT_POLICY_INVALID")
		}
		if old, ok := seen[term.Text]; ok {
			if old != term {
				return nil, failure("MATERIAL_FACT_POLICY_AMBIGUOUS")
			}
			continue
		}
		seen[term.Text] = term
		items = append(items, term)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Text < items[j].Text })
	p := &FactPolicy{terms: map[string]NamedTerm{}}
	literals := make([]resolver.Term, 0, len(items))
	for _, term := range items {
		id := digest([]byte(term.Kind + "\x00" + term.Text))
		p.terms[id] = term
		literals = append(literals, resolver.Term{ID: id, Name: term.Text})
	}
	m, err := matching.New(ctx, literals)
	if err != nil {
		return nil, failure("MATERIAL_FACT_POLICY_UNSUPPORTED")
	}
	p.matcher = m
	raw, _ := json.Marshal(struct {
		Version, Unicode, LiteralMatcher string
		Patterns                         [3]string
		Terms                            []NamedTerm
	}{"fact-policy-v1", textcompat.UnicodeVersion, matching.Extractor, [3]string{numberPattern, datePattern, placeholderPattern}, items})
	p.digest = digest(raw)
	return p, nil
}

func (p *FactPolicy) SHA256() string {
	if p == nil {
		return ""
	}
	return p.digest
}

type factToken struct {
	kind, key, demand string
	start, end        int
}

func tokenKey(kind, text string) string {
	value := textcompat.Normalize(text)
	if kind == "number" {
		value = strings.Map(func(r rune) rune {
			if textcompat.IsSpace(r) {
				return -1
			}
			return r
		}, value)
	}
	return kind + "\x00" + value
}
func (t factToken) sha256() string { return digest([]byte(t.key)) }

func staticTokens(text string) ([]factToken, *Error) {
	m, err := factPatterns.ForSource(text)
	if err != nil {
		return nil, failure("MATERIAL_FACT_TEXT_UNSUPPORTED")
	}
	t, err := m.Text(text)
	if err != nil {
		return nil, failure("MATERIAL_FACT_TEXT_UNSUPPORTED")
	}
	out := []factToken{}
	for _, kind := range []string{"date", "number", "placeholder"} {
		matches, err := t.AllLimit(kind, MaxFactTokens-len(out))
		if err != nil {
			return nil, failure("MATERIAL_FACT_TOKEN_LIMIT")
		}
		for _, match := range matches {
			out = append(out, factToken{kind: kind, key: tokenKey(kind, match.Value()), start: match.Start(), end: match.End()})
		}
	}
	return out, nil
}

// Whole configured phrases use the existing matcher's longest-literal policy.
// A date/name/placeholder contains its numeric pieces; report that complete token
// once, so a manual check cannot accidentally apply to an unrelated substring.
func compactTokens(tokens []factToken) []factToken {
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].start != tokens[j].start {
			return tokens[i].start < tokens[j].start
		}
		if tokens[i].end != tokens[j].end {
			return tokens[i].end > tokens[j].end
		}
		return tokens[i].kind < tokens[j].kind
	})
	out := make([]factToken, 0, len(tokens))
	placeholderEnd, nonNumberEnd := -1, -1
	for first := 0; first < len(tokens); {
		last := first
		for last < len(tokens) && tokens[last].start == tokens[first].start {
			t := tokens[last]
			if t.kind == "placeholder" {
				placeholderEnd = max(placeholderEnd, t.end)
			}
			if t.kind != "number" {
				nonNumberEnd = max(nonNumberEnd, t.end)
			}
			last++
		}
		for _, token := range tokens[first:last] {
			if token.kind != "placeholder" && placeholderEnd >= token.end || token.kind == "number" && nonNumberEnd >= token.end {
				continue
			}
			if len(out) == 0 || out[len(out)-1] != token {
				out = append(out, token)
			}
		}
		first = last
	}
	return out
}

func bytePositions(text string) []int {
	positions := make([]int, 0, utf8.RuneCountInString(text)+1)
	for index := range text {
		positions = append(positions, index)
	}
	return append(positions, len(text))
}

func (p *FactPolicy) tokens(ctx context.Context, claims []FactClaim) ([][]factToken, *Error) {
	blocks := make([]matching.Block, len(claims))
	for i, c := range claims {
		blocks[i] = matching.Block{ID: fmt.Sprint(i), Text: c.Text}
	}
	matches, err := p.matcher.FindBlocks(ctx, "material-facts", blocks)
	if err != nil {
		return nil, failure("MATERIAL_FACT_MATCH_LIMIT")
	}
	out, count := make([][]factToken, len(claims)), 0
	for i, claim := range claims {
		if ctx.Err() != nil {
			return nil, failure("MATERIAL_INTEGRITY_CANCELED")
		}
		tokens, e := staticTokens(claim.Text)
		if e != nil {
			return nil, e
		}
		if len(matches[i].Issues) != 0 {
			return nil, failure("MATERIAL_FACT_POLICY_AMBIGUOUS")
		}
		positions := bytePositions(claim.Text)
		for _, found := range matches[i].Mentions {
			term := p.terms[found.TechnologyID]
			tokens = append(tokens, factToken{term.Kind, tokenKey(term.Kind, found.SourceText), term.DemandID, positions[found.SourceRef.Start], positions[found.SourceRef.End]})
		}
		count += len(tokens)
		if count > MaxFactTokens {
			return nil, failure("MATERIAL_FACT_TOKEN_LIMIT")
		}
		out[i] = compactTokens(tokens)
	}
	return out, nil
}
