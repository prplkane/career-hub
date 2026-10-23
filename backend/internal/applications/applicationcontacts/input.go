// Package applicationcontacts owns explicit contact identity and provenance.
// It does not infer contacts, merge facts, send messages or change job status.
package applicationcontacts

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobs/joburls"
	"careerhub.local/backend/internal/textcompat"
)

const ContractVersion = 1
const maxRequestBytes = 256 << 10
const maxRows = 10000
const maxScanBytes = 8 << 20
const maxCellBytes = 64 << 10

// Source metadata is private diagnostic/test data, not safe log text.
type Error struct{ Stage, Code, SourceClass, SourceMessage string }

func (*Error) Error() string                { return "Contact operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func problem(kind, message string) *Error {
	classes := map[string]string{"INVALID": "Error", "NOT_FOUND": "NotFound", "REVISION_CONFLICT": "RevisionConflict", "ARCHIVED": "Archived", "EXACT_MATCH_AMBIGUOUS": "ExactMatchAmbiguous", "PROVENANCE_CONFLICT": "ProvenanceConflict"}
	return &Error{"contact", "APPLICATION_CONTACT_" + kind, "ApplicationContact" + classes[kind], message}
}
func invalid(message string) *Error { return problem("INVALID", message) }
func unsupported(code string) *Error {
	return &Error{Stage: "bounds", Code: "APPLICATION_CONTACT_" + code}
}

// Ordered fact updates retain the source's validation order. First-party
// adapters must not silently discard duplicate JSON keys or object ordering.
type FactChange struct {
	Field string
	Value any
}

func (FactChange) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted contact fact]") }

type Request struct {
	Operation   string
	Fields      map[string]any
	UpdateOrder []string
}

func (Request) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted contact request]") }

var operationFields = map[string][]string{
	"create": {"name", "role", "company", "context", "channels", "provenance", "job_ids", "discovery_ids"},
	"find":   {"name", "role", "company", "context", "channels"},
	"get":    {"contact_id"}, "list": {"include_archived"},
	"find_provenance": {"source_reference", "source_content_sha256"},
	"update":          {"contact_id", "expected_revision", "updates", "provenance"},
	"link":            {"contact_id", "expected_revision", "job_ids", "discovery_ids", "provenance"},
	"unlink":          {"contact_id", "expected_revision", "job_ids", "discovery_ids"},
	"archive":         {"contact_id", "expected_revision"}, "restore": {"contact_id", "expected_revision"},
}

func (r Request) validate() *Error {
	allowed, ok := operationFields[r.Operation]
	if !ok || r.Fields == nil {
		return unsupported("REQUEST_UNSUPPORTED")
	}
	for k := range r.Fields {
		if !slices.Contains(allowed, k) {
			return unsupported("REQUEST_UNSUPPORTED")
		}
	}
	// Marshal supplies an overall bound, but not scalar validation: Go replaces
	// malformed UTF-8. Walk first so it cannot corrupt persisted identity.
	nodes, size := 0, 0
	var check func(any, int) bool
	check = func(v any, depth int) bool {
		nodes++
		if depth > 16 || nodes > 4096 || size > maxRequestBytes {
			return false
		}
		switch x := v.(type) {
		case string:
			size += len(x)
			return utf8.ValidString(x) && size <= maxRequestBytes
		case map[string]any:
			for k, item := range x {
				if !check(k, depth+1) || !check(item, depth+1) {
					return false
				}
			}
		case []any:
			for _, item := range x {
				if !check(item, depth+1) {
					return false
				}
			}
		case nil, bool, int, int64, json.Number, float64:
		default:
			return false
		}
		return true
	}
	if !check(r.Fields, 0) {
		return unsupported("INPUT_UNSUPPORTED")
	}
	b, err := json.Marshal(r.Fields)
	if err != nil || len(b) > maxRequestBytes {
		return unsupported("INPUT_LIMIT")
	}
	if r.Operation != "update" && len(r.UpdateOrder) > 0 {
		return unsupported("REQUEST_UNSUPPORTED")
	}
	return nil
}
func value(m map[string]any, key string, fallback any) any {
	if v, ok := m[key]; ok {
		return v
	}
	return fallback
}
func integer(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		if strings.ContainsAny(string(n), ".eE") {
			return 0, false
		}
		x, e := strconv.ParseInt(string(n), 10, 64)
		return x, e == nil
	default:
		return 0, false
	}
}
func text(v any, field string, maximum int, empty, exact bool) (string, *Error) {
	s, ok := v.(string)
	if !ok {
		return "", invalid(field + " must be a string")
	}
	if !utf8.ValidString(s) || len(s) > maxRequestBytes {
		return "", unsupported("INPUT_UNSUPPORTED")
	}
	if exact {
		s = textcompat.Strip(s)
	} else {
		s = strings.Join(strings.FieldsFunc(s, textcompat.IsSpace), " ")
	}
	if !empty && s == "" {
		return "", invalid(field + " must not be empty")
	}
	if utf8.RuneCountInString(s) > maximum {
		return "", invalid(field + " is too long")
	}
	return s, nil
}
func identifier(v any, field string, empty bool) (string, *Error) {
	s, p := text(v, field, 256, empty, false)
	if p != nil {
		return "", p
	}
	// Pin Windows pathlib semantics on every target OS; these are IDs, not
	// local paths. Drive-relative IDs and '.' are not plain names; '..' is.
	r := []rune(s)
	if s != "" && (s == "." || strings.ContainsAny(s, "/\\\x00") || len(r) >= 2 && r[1] == ':') {
		return "", invalid(field + " must be a plain identifier")
	}
	return s, nil
}
func id(v any) (string, *Error) {
	s, ok := v.(string)
	if !ok || !strings.HasPrefix(s, "contact_") || !hexadecimal(strings.TrimPrefix(s, "contact_"), 32) {
		return "", invalid("contact_id is invalid")
	}
	return s, nil
}
func hexadecimal(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func revision(v any) (int64, *Error) {
	n, ok := integer(v)
	if !ok || n <= 0 {
		return 0, invalid("expected_revision must be a positive integer")
	}
	return n, nil
}

type channel struct {
	Kind       string `json:"kind"`
	Value      string `json:"value"`
	Normalized string `json:"normalized_value"`
}

func (channel) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted contact channel]") }

var emailPattern = regexp.MustCompile("^[a-z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$")

func normalizeChannel(v any, field string) (channel, *Error) {
	m, ok := v.(map[string]any)
	if !ok {
		return channel{}, invalid(field + " must be an object")
	}
	key := "kind"
	if _, has := m[key]; !has {
		key = "type"
	}
	_, kindPresent := m[key]
	_, valuePresent := m["value"]
	if len(m) != 2 || !kindPresent || !valuePresent {
		return channel{}, invalid(field + " must contain only kind and value")
	}
	kind, ok := m[key].(string)
	if !ok || !slices.Contains([]string{"email", "phone", "professional_profile"}, kind) {
		return channel{}, invalid(field + ".kind is unsupported")
	}
	s, p := text(m["value"], field+".value", 2048, false, true)
	if p != nil {
		return channel{}, p
	}
	normalized := ""
	switch kind {
	case "email":
		// re.IGNORECASE [A-Z] recognizes four extra scalars. Full casefold
		// before validation would incorrectly accept sharp-s expansions.
		candidate := strings.Map(func(r rune) rune {
			switch r {
			case 'İ', 'ı':
				return 'i'
			case 'ſ':
				return 's'
			case 'K':
				return 'k'
			}
			if r >= 'A' && r <= 'Z' {
				return r + 32
			}
			return r
		}, s)
		if utf8.RuneCountInString(s) > 320 || !emailPattern.MatchString(candidate) {
			return channel{}, invalid("email channel is invalid")
		}
		normalized = textcompat.Fold(s)
	case "phone":
		digits := []rune{}
		for _, r := range s {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
				return channel{}, invalid("phone channel is invalid")
			}
			if _, ok := textcompat.DecimalDigit(r); ok {
				digits = append(digits, r)
			}
		}
		if len(digits) < 7 || len(digits) > 15 {
			return channel{}, invalid("phone channel is invalid")
		}
		normalized = string(digits)
		if strings.HasPrefix(textcompat.Strip(s), "+") {
			normalized = "+" + normalized
		}
	case "professional_profile":
		var err error
		normalized, err = joburls.ContactProfile(s)
		if err != nil {
			return channel{}, invalid("professional_profile channel is invalid")
		}
	}
	return channel{kind, s, normalized}, nil
}
func normalizeChannels(v any) ([]channel, *Error) {
	items, ok := v.([]any)
	if !ok {
		return nil, invalid("channels must be a list")
	}
	if len(items) > 20 {
		return nil, invalid("channels contains too many entries")
	}
	out := []channel{}
	seen := map[string]bool{}
	for i, item := range items {
		c, p := normalizeChannel(item, fmt.Sprintf("channels[%d]", i))
		if p != nil {
			return nil, p
		}
		key := c.Kind + "\x00" + c.Normalized
		if !seen[key] {
			seen[key] = true
			out = append(out, c)
		}
	}
	return out, nil
}

var factNames = []string{"name", "role", "company", "context"}

func factLimit(field string) int {
	if field == "name" {
		return 200
	}
	if field == "context" {
		return 1000
	}
	return 300
}
func facts(m map[string]any) (map[string]string, *Error) {
	out := map[string]string{}
	for _, key := range factNames {
		s, p := text(value(m, key, ""), key, factLimit(key), true, false)
		if p != nil {
			return nil, p
		}
		out[key] = s
	}
	return out, nil
}
func links(v any, field string) ([]string, *Error) {
	items, ok := v.([]any)
	if !ok {
		return nil, invalid(field + " must be a list")
	}
	if len(items) > 100 {
		return nil, invalid(field + " contains too many entries")
	}
	out := []string{}
	seen := map[string]bool{}
	for i, item := range items {
		s, p := identifier(item, fmt.Sprintf("%s[%d]", field, i), false)
		if p != nil {
			return nil, p
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}
