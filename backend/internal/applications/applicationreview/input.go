// Package applicationreview persists existing assessment and checklist records.
// These primitives do not approve evidence, calculate findings or change status.
package applicationreview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/textcompat"
)

const maxInputBytes = (2 << 20) + (64 << 10)
const maxCellBytes = 2 << 20
const maxScanBytes = 8 << 20
const maxRows = 10000

type Error struct{ Stage, Code, SourceClass, SourceMessage string }

func (*Error) Error() string                { return "Application review operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func problem(class, message string) *Error {
	return &Error{"review", "APPLICATION_REVIEW_SOURCE_ERROR", class, message}
}
func invalid(message string) *Error { return problem("ApplicationHistoryError", message) }
func unsupported(code string) *Error {
	return &Error{Stage: "bounds", Code: "APPLICATION_REVIEW_" + code}
}

type Request struct {
	Operation string
	Fields    map[string]any
}

func (Request) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted application review request]")
}

var fields = map[string][]string{
	"get_assessment":   {"job_id"},
	"save_assessment":  {"job_id", "job_fingerprint", "job_analyzer_version", "candidate_revision", "assessment_context_revision", "assessment_version", "assessment"},
	"list_resolutions": {"job_id", "current_assessment_revision"},
	"save_resolution":  {"job_id", "finding_key", "resolution", "note"},
	"list_manual":      {"job_id"}, "list_actions": {"job_id"},
	"save_manual": {"job_id", "source_fingerprint", "item"},
	"save_action": {"job_id", "source_fingerprint", "item", "action", "origin"},
}

func (r Request) validate() *Error {
	allowed, ok := fields[r.Operation]
	if !ok || r.Fields == nil {
		return unsupported("REQUEST_UNSUPPORTED")
	}
	for k := range r.Fields {
		if !slices.Contains(allowed, k) {
			return unsupported("REQUEST_UNSUPPORTED")
		}
	}
	if !boundedJSON(r.Fields) {
		return unsupported("INPUT_UNSUPPORTED")
	}
	return nil
}

// Bound before encoding. Reject non-scalar text, cycles, non-finite extensions
// and non-JSON objects instead of coercing a stored review or its identity.
func boundedJSON(value any) bool {
	nodes, size := 0, 0
	var visit func(any, int) bool
	visit = func(v any, depth int) bool {
		nodes++
		size += 2
		if nodes > 10000 || depth > 32 || size > maxInputBytes {
			return false
		}
		switch x := v.(type) {
		case nil, bool, int, int64:
			return true
		case float64:
			return !math.IsNaN(x) && !math.IsInf(x, 0)
		case json.Number:
			size += len(x)
			if len(x) > 4096 || !json.Valid([]byte(x)) {
				return false
			}
			if strings.ContainsAny(string(x), ".eE") {
				n, e := strconv.ParseFloat(string(x), 64)
				return e == nil && !math.IsInf(n, 0)
			}
			return true
		case string:
			size += len(x)
			return size <= maxInputBytes && utf8.ValidString(x)
		case []any:
			for _, item := range x {
				if !visit(item, depth+1) {
					return false
				}
			}
		case map[string]any:
			for k, item := range x {
				if !visit(k, depth+1) || !visit(item, depth+1) {
					return false
				}
			}
		default:
			return false
		}
		return size <= maxInputBytes
	}
	return visit(value, 0)
}

func get(f map[string]any, key string, fallback any) any {
	if v, ok := f[key]; ok {
		return v
	}
	return fallback
}
func sourceString(v any) string {
	if v == nil {
		return "None"
	}
	if b, ok := v.(bool); ok {
		if b {
			return "True"
		}
		return "False"
	}
	return fmt.Sprint(v)
}
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) != 0
	case map[string]any:
		return len(x) != 0
	case json.Number:
		n, _ := strconv.ParseFloat(string(x), 64)
		return n != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	}
	return false
}
func sha(v any, name string, revision bool) (string, *Error) {
	s, ok := v.(string)
	value := s
	if revision {
		if !strings.HasPrefix(s, "sha256:") {
			ok = false
		} else {
			value = s[7:]
		}
	}
	if len(value) != 64 {
		ok = false
	}
	for _, c := range value {
		if !strings.ContainsRune("0123456789abcdef", c) {
			ok = false
		}
	}
	if !ok {
		if revision {
			return "", invalid(name + " must be a sha256 revision")
		}
		return "", invalid(name + " must be a lowercase SHA-256")
	}
	return s, nil
}

// Keep Python's integer validation order before SQLite's later signed-integer
// binding limit. A huge line_start can still fail line_end validation first.
func integer(v any, minimum int64, message string) (*big.Int, *Error) {
	var raw string
	switch n := v.(type) {
	case int:
		raw = strconv.Itoa(n)
	case int64:
		raw = strconv.FormatInt(n, 10)
	case json.Number:
		if strings.ContainsAny(string(n), ".eE") {
			return nil, invalid(message)
		}
		raw = string(n)
	default:
		return nil, invalid(message)
	}
	n, ok := new(big.Int).SetString(raw, 10)
	if !ok || n.Cmp(big.NewInt(minimum)) < 0 {
		return nil, invalid(message)
	}
	return n, nil
}
func normalizedNote(v any) (string, *Error) {
	s, ok := v.(string)
	if !ok {
		return "", invalid("resolution note must be a string")
	}
	s = textcompat.Strip(s)
	if utf8.RuneCountInString(s) > 1000 {
		return "", invalid("resolution note must not exceed 1000 characters")
	}
	return s, nil
}

// Assessment JSON uses Python's sorted ensure_ascii=False spelling, including
// literal DEL/U+2028/U+2029 and Python float spelling. No blind escape replacement.
func assessmentJSON(value map[string]any) ([]byte, *Error) {
	var b bytes.Buffer
	var appendValue func(any) bool
	appendString := func(s string) {
		b.WriteByte('"')
		for _, r := range s {
			switch r {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 32 {
					fmt.Fprintf(&b, `\u%04x`, r)
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
	}
	appendValue = func(v any) bool {
		if b.Len() > maxCellBytes {
			return false
		}
		switch x := v.(type) {
		case string:
			appendString(x)
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			b.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					b.WriteByte(',')
				}
				appendString(k)
				b.WriteByte(':')
				if !appendValue(x[k]) {
					return false
				}
			}
			b.WriteByte('}')
		case []any:
			b.WriteByte('[')
			for i, child := range x {
				if i > 0 {
					b.WriteByte(',')
				}
				if !appendValue(child) {
					return false
				}
			}
			b.WriteByte(']')
		default:
			raw, e := protocol.EncodeRecord(map[string]any{"v": v})
			if e != nil {
				return false
			}
			b.Write(raw[5 : len(raw)-2])
		}
		return b.Len() <= maxCellBytes
	}
	if !boundedJSON(value) || !appendValue(value) || !json.Valid(b.Bytes()) {
		return nil, unsupported("ASSESSMENT_JSON_UNSUPPORTED")
	}
	return b.Bytes(), nil
}

// encoding/json replaces escaped unpaired surrogates. Inspect the original
// lexical strings first, including duplicate fields that decoding overwrites.
// The caller establishes valid JSON and bounded UTF-8 bytes before this scan.
func storedJSONSafe(raw string) bool {
	depth := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '{', '[':
			depth++
			if depth > 32 {
				return false
			}
		case '}', ']':
			depth--
		case '"':
			i++
			for i < len(raw) && raw[i] != '"' {
				if raw[i] != '\\' {
					i++
					continue
				}
				if raw[i+1] != 'u' {
					i += 2
					continue
				}
				code, _ := strconv.ParseUint(raw[i+2:i+6], 16, 16)
				i += 6
				if code >= 0xdc00 && code <= 0xdfff {
					return false
				}
				if code < 0xd800 || code > 0xdbff {
					continue
				}
				if i+6 > len(raw) || raw[i:i+2] != `\u` {
					return false
				}
				low, err := strconv.ParseUint(raw[i+2:i+6], 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
		default:
			if raw[i] == '-' || raw[i] >= '0' && raw[i] <= '9' {
				start := i
				for i < len(raw) && !strings.ContainsRune("{}[],: \t\r\n", rune(raw[i])) {
					i++
				}
				if i-start > 4096 {
					return false
				}
				i--
			}
		}
	}
	return true
}

func normalizeStoredNumbers(value any) *Error {
	var normalize func(any) (any, *Error)
	normalize = func(v any) (any, *Error) {
		switch x := v.(type) {
		case json.Number:
			raw, err := protocol.EncodeRecord(map[string]any{"v": x})
			if err != nil {
				return nil, unsupported("ASSESSMENT_JSON_UNSUPPORTED")
			}
			return json.Number(string(raw[5 : len(raw)-2])), nil
		case map[string]any:
			for k, item := range x {
				n, p := normalize(item)
				if p != nil {
					return nil, p
				}
				x[k] = n
			}
		case []any:
			for i, item := range x {
				n, p := normalize(item)
				if p != nil {
					return nil, p
				}
				x[i] = n
			}
		}
		return v, nil
	}
	_, p := normalize(value)
	return p
}

// Python permits non-JSON constants. Recognize that unsupported representation
// without confusing quoted words with constants or pretending it is source JSON
// corruption. This does not rewrite or repair the stored value.
func unsupportedJSONConstants(raw string) bool {
	var normalized strings.Builder
	found := false
	for i := 0; i < len(raw); {
		start := i
		if raw[i] == '"' {
			i++
			for i < len(raw) {
				if raw[i] == '\\' {
					i += 2
					continue
				}
				if raw[i] == '"' {
					i++
					break
				}
				i++
			}
			if i > len(raw) {
				i = len(raw)
			}
			normalized.WriteString(raw[start:i])
			continue
		}
		if strings.ContainsRune("{}[],: \t\r\n", rune(raw[i])) {
			normalized.WriteByte(raw[i])
			i++
			continue
		}
		for i < len(raw) && !strings.ContainsRune("{}[],: \t\r\n\"", rune(raw[i])) {
			i++
		}
		token := raw[start:i]
		if oneOf(token, "NaN", "Infinity", "-Infinity") {
			found = true
			normalized.WriteByte('0')
		} else {
			normalized.WriteString(token)
		}
	}
	return found && json.Valid([]byte(normalized.String()))
}
