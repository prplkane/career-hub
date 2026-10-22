// Package applicationtexts preserves the existing application-text workbench
// contract. This pure layer has no filesystem, application-state or AI authority.
package applicationtexts

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const MaxInputBytes = 1 << 20

type Error struct{ Code string }

func (*Error) Error() string                { return "The application text draft is invalid." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func invalid() *Error                       { return &Error{Code: "APPLICATION_TEXT_DRAFT_INVALID"} }

type Limits struct {
	Characters *json.Number `json:"characters"`
	Words      *json.Number `json:"words"`
}

type Draft struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Source       string   `json:"source"`
	ExactPrompt  string   `json:"exact_prompt"`
	Limits       Limits   `json:"limits"`
	Response     string   `json:"response"`
	EvidenceRefs []string `json:"evidence_refs"`
	ManualChecks []string `json:"manual_checks"`
	State        string   `json:"state"`
}

func (Draft) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted application text]") }

var itemID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}$`)
var positiveInteger = regexp.MustCompile(`^[1-9][0-9]*$`)

func kindAllowed(value string) bool {
	switch value {
	case "cover_letter", "short_answer", "long_answer", "recruiter_message", "follow_up", "other":
		return true
	}
	return false
}

func cleanLimit(value any) (*json.Number, bool) {
	if value == nil {
		return nil, true
	}
	var raw string
	switch n := value.(type) {
	case int:
		raw = strconv.Itoa(n)
	case int64:
		raw = strconv.FormatInt(n, 10)
	case json.Number:
		raw = string(n)
	default:
		return nil, false
	}
	if len(raw) > 4096 || !positiveInteger.MatchString(raw) {
		return nil, false
	}
	n := json.Number(raw)
	return &n, true
}

func within(count int, limit *json.Number) bool {
	if limit == nil {
		return true
	}
	n, ok := new(big.Int).SetString(string(*limit), 10)
	return ok && n.Cmp(big.NewInt(int64(count))) >= 0
}

// NormalizeDraft accepts an already decoded JSON object (UseNumber preserves
// integer-vs-float limits). The decoder must separately reject lossy Unicode
// escapes. It does not establish an application status, JD identity or evidence.
func NormalizeDraft(payload map[string]any) (Draft, *Error) {
	var draft Draft
	if payload == nil || len(payload) > 10 {
		return Draft{}, invalid()
	}
	for name := range payload {
		switch name {
		case "id", "kind", "source", "exact_prompt", "limits", "response", "evidence_refs", "manual_checks", "state":
		default:
			return Draft{}, invalid()
		}
	}
	budget := MaxInputBytes
	clean := func(value any, maximum int) (string, bool) {
		s, ok := value.(string)
		if !ok || len(s) > budget || !utf8.ValidString(s) {
			return "", false
		}
		budget -= len(s)
		s = textcompat.Strip(s)
		return s, s != "" && utf8.RuneCountInString(s) <= maximum
	}
	for _, field := range []struct {
		name   string
		max    int
		target *string
	}{
		{"id", 80, &draft.ID}, {"kind", 40, &draft.Kind}, {"source", 120, &draft.Source},
		{"exact_prompt", 4000, &draft.ExactPrompt}, {"response", 50000, &draft.Response},
	} {
		value, ok := clean(payload[field.name], field.max)
		if !ok {
			return Draft{}, invalid()
		}
		*field.target = value
	}
	if !itemID.MatchString(draft.ID) || !kindAllowed(draft.Kind) {
		return Draft{}, invalid()
	}
	if state, exists := payload["state"]; exists && state != "draft" {
		return Draft{}, invalid()
	}
	draft.State = "draft"
	limits := map[string]any{}
	if value, exists := payload["limits"]; exists {
		var ok bool
		limits, ok = value.(map[string]any)
		if !ok || limits == nil || len(limits) > 2 {
			return Draft{}, invalid()
		}
	}
	for key := range limits {
		if key != "characters" && key != "words" {
			return Draft{}, invalid()
		}
	}
	var ok bool
	draft.Limits.Characters, ok = cleanLimit(limits["characters"])
	if !ok {
		return Draft{}, invalid()
	}
	draft.Limits.Words, ok = cleanLimit(limits["words"])
	if !ok {
		return Draft{}, invalid()
	}
	if !within(utf8.RuneCountInString(draft.Response), draft.Limits.Characters) ||
		!within(len(strings.FieldsFunc(draft.Response, textcompat.IsSpace)), draft.Limits.Words) {
		return Draft{}, invalid()
	}
	if strings.ContainsAny(draft.Response, "–—") {
		return Draft{}, invalid()
	}
	folded := textcompat.Fold(draft.Response)
	for _, phrase := range []string{"I am writing to apply", "I am writing to express my interest", "I am excited to apply", "I would welcome the opportunity"} {
		if strings.Contains(folded, textcompat.Fold(phrase)) {
			return Draft{}, invalid()
		}
	}
	for _, field := range []struct {
		name   string
		target *[]string
	}{
		{"evidence_refs", &draft.EvidenceRefs}, {"manual_checks", &draft.ManualChecks},
	} {
		*field.target = []string{}
		if payload[field.name] == nil {
			continue
		}
		values, ok := payload[field.name].([]any)
		if !ok || len(values) > 100 {
			return Draft{}, invalid()
		}
		seen := map[string]bool{}
		for _, value := range values {
			text, ok := clean(value, 500)
			if !ok {
				return Draft{}, invalid()
			}
			if !seen[text] {
				*field.target = append(*field.target, text)
				seen[text] = true
			}
		}
	}
	return draft, nil
}
