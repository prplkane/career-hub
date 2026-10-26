// Package applicationchecklist composes the existing saved-JD checklist. It
// never fetches a posting, creates candidate facts or changes application status.
package applicationchecklist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const maxItems = 10000
const maxHistoryBytes = 8 << 20

type Error struct{ Stage, Code, SourceClass, SourceMessage string }

func (*Error) Error() string                { return "Application checklist operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func sourceError(class, message string) *Error {
	return &Error{"checklist", "APPLICATION_CHECKLIST_SOURCE_ERROR", class, message}
}
func invalid(message string) *Error {
	return sourceError("ApplicationInstructionChecklistError", message)
}
func conflict(message string) *Error {
	return sourceError("ApplicationInstructionRevisionConflict", message)
}
func unsupported(code string) *Error {
	return &Error{Stage: "checklist", Code: "APPLICATION_CHECKLIST_" + code}
}
func active(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return unsupported("CANCELED")
	}
	return nil
}

// Source must confine regular single-link files to its retained managed root,
// bound reads and retain byte/presence observations until the owner's precommit
// recheck. Preparation alone is not a coherent filesystem transaction.
type Source interface {
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}

// Fields is a first-party finite scalar request, not a public JSON API. Callers
// must not mutate it concurrently. Preparation copies the accepted scalars.
type Request struct {
	Operation string
	Fields    map[string]any
}

func (Request) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted checklist request]") }

type Prepared struct {
	request               Request
	id, fingerprint, text string
	analysis              cataloginput.Value
}

func (*Prepared) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted prepared checklist]")
}

func requestCopy(r Request) (Request, *Error) {
	keys := map[string][]string{
		"load":       {"job_id"},
		"add_manual": {"job_id", "expected_source_fingerprint", "category", "selected_text"},
		"set_state":  {"job_id", "expected_source_fingerprint", "instruction_fingerprint", "state"},
	}
	allowed, ok := keys[r.Operation]
	if !ok || r.Fields == nil {
		return Request{}, unsupported("REQUEST_UNSUPPORTED")
	}
	out := Request{Operation: r.Operation, Fields: map[string]any{}}
	size := 0
	for key, value := range r.Fields {
		if !slices.Contains(allowed, key) {
			return Request{}, unsupported("REQUEST_UNSUPPORTED")
		}
		switch x := value.(type) {
		case nil, bool, int, int64:
		case json.Number:
			if len(x) == 0 || len(x) > 4096 || (x[0] != '-' && (x[0] < '0' || x[0] > '9')) || !json.Valid([]byte(x)) || strings.ContainsAny(string(x), ".eE") {
				return Request{}, unsupported("INPUT_UNSUPPORTED")
			}
		case string:
			size += len(x)
			if size > 16<<10 || !utf8.ValidString(x) {
				return Request{}, unsupported("INPUT_UNSUPPORTED")
			}
		default:
			// Collections, non-finite numbers and arbitrary objects are outside
			// the first-party scalar request contract.
			return Request{}, unsupported("INPUT_UNSUPPORTED")
		}
		out.Fields[key] = value
	}
	return out, nil
}

func field(v cataloginput.Value, key string) cataloginput.Value {
	for _, e := range v.Entries {
		if e.Key.Kind == "string" && e.Key.Text == key {
			return e.Value
		}
	}
	return cataloginput.Value{Kind: "null"}
}
func truth(v cataloginput.Value) bool {
	switch v.Kind {
	case "", "null":
		return false
	case "bool":
		return v.Boolean
	case "int":
		return v.Text != "0"
	case "float":
		return v.Text != "0.0" && v.Text != "-0.0"
	case "string":
		return v.Text != ""
	case "bytes":
		return len(v.Bytes) != 0
	case "sequence":
		return len(v.Items) != 0
	case "mapping":
		return len(v.Entries) != 0
	default:
		return true
	}
}
func sha(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func category(v any) bool {
	s, ok := v.(string)
	return ok && slices.Contains([]string{"email_destination", "exact_phrase", "required_artifact", "cover_letter_format", "external_step", "deadline"}, s)
}
func read(ctx context.Context, source Source, path string, limit int64, message string) ([]byte, *Error) {
	if p := active(ctx); p != nil {
		return nil, p
	}
	if source == nil {
		return nil, unsupported("SOURCE_UNAVAILABLE")
	}
	raw, present, e := source.ReadFile(path, limit)
	if p := active(ctx); p != nil {
		return nil, p
	}
	if e != nil || !present || int64(len(raw)) > limit {
		if int64(len(raw)) > limit || e != nil && e.Code == "FILE_SIZE_LIMIT" {
			return nil, unsupported("INPUT_LIMIT")
		}
		return nil, invalid(message)
	}
	return raw, nil
}

// Prepare reads just this normalized card and its incoming JD. The normalized
// content hash is also recomputed: equal fields in a stale card are not proof
// that the retained JD still matches. No SQL/file writes occur here.
func Prepare(ctx context.Context, source Source, r Request) (*Prepared, *Error) {
	if p := active(ctx); p != nil {
		return nil, p
	}
	r, p := requestCopy(r)
	if p != nil {
		return nil, p
	}
	id, ok := r.Fields["job_id"].(string)
	runes := []rune(id)
	if !ok || id == "" || id == "." || strings.ContainsAny(id, "/\\\x00") || len(runes) > 1 && runes[1] == ':' {
		return nil, invalid("job_id must be a plain identifier")
	}
	raw, p := read(ctx, source, "jobs/normalized/"+id+".yaml", cataloginput.YAMLMaxInputBytes, "Saved job card is unavailable")
	if p != nil {
		return nil, p
	}
	card, ye := cataloginput.ParseYAML(raw)
	if ye != nil {
		if ye.Kind != "invalid" {
			return nil, unsupported("CARD_REPRESENTATION_UNSUPPORTED")
		}
		return nil, invalid("Saved job card is invalid")
	}
	identity := field(card, "id")
	if card.Kind != "mapping" || identity.Kind != "string" || identity.Text != id {
		return nil, invalid("Saved job card identity is invalid")
	}
	fp := field(card, "content_fingerprint")
	if fp.Kind != "string" || utf8.RuneCountInString(fp.Text) != 64 {
		return nil, invalid("Saved JD fingerprint is unavailable")
	}
	analysis := field(field(card, "digest"), "application_instructions")
	analyzedFP := field(analysis, "source_fingerprint")
	if analysis.Kind != "mapping" || analyzedFP.Kind != "string" || analyzedFP.Text != fp.Text {
		return nil, invalid("Reanalyze this job before using its application checklist")
	}
	path := field(card, "raw_jd_path")
	if path.Kind != "string" || path.Text == "" {
		return nil, invalid("Saved JD source is unavailable")
	}
	name := strings.ReplaceAll(path.Text, "\\", "/")
	if !strings.HasPrefix(name, "jobs/incoming/") {
		return nil, invalid("Saved JD source is unavailable")
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return nil, invalid("Saved JD source is unavailable")
		}
	}
	raw, p = read(ctx, source, name, applicationanalysis.MaxSourceBytes, "Saved JD source is unavailable")
	if p != nil {
		return nil, p
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(raw) {
		return nil, invalid("Saved JD source is unavailable")
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
	current, ae := applicationanalysis.SourceFingerprint(text)
	if ae != nil {
		return nil, unsupported("SOURCE_REPRESENTATION_UNSUPPORTED")
	}
	if !sha(fp.Text) || current != fp.Text {
		return nil, unsupported("SOURCE_FINGERPRINT_MISMATCH")
	}
	if p := active(ctx); p != nil {
		return nil, p
	}
	return &Prepared{r, id, fp.Text, text, analysis}, nil
}

func yamlItem(v cataloginput.Value) (map[string]any, *Error) {
	out := map[string]any{}
	for _, key := range []string{"fingerprint", "category", "confidence", "source_text"} {
		x := field(v, key)
		if x.Kind != "string" {
			return nil, unsupported("DETECTED_ITEM_UNSUPPORTED")
		}
		out[key] = x.Text
	}
	for _, key := range []string{"line_start", "line_end", "extractor_version"} {
		x := field(v, key)
		n, err := strconv.ParseInt(x.Text, 10, 64)
		if x.Kind != "int" || err != nil {
			return nil, unsupported("DETECTED_ITEM_UNSUPPORTED")
		}
		out[key] = n
	}
	return out, nil
}
