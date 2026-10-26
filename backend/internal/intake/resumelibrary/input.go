// Package resumelibrary owns immutable resume versions and explicit references.
// It does not render, parse, rewrite templates, or select a resume for the user.
package resumelibrary

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const ContractVersion = 1
const MaxResumeBytes = 10 << 20
const LibraryPath = "applications/resumes/library"
const maxPlanBytes = 2 << 20
const maxRequestBytes = maxPlanBytes + (64 << 10)
const maxCellBytes = maxPlanBytes
const maxScanBytes = 8 << 20

// Source details are private compatibility evidence, not safe diagnostic text.
type Error struct{ Stage, Code, SourceClass, SourceMessage string }

func (*Error) Error() string                { return "Resume operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func problem(class, message string) *Error {
	return &Error{"resume", "RESUME_LIBRARY_INVALID", class, message}
}
func invalid(message string) *Error  { return problem("ApplicationHistoryError", message) }
func unsupported(code string) *Error { return &Error{Stage: "bounds", Code: "RESUME_LIBRARY_" + code} }

// Content is binary, never JSON/base64 request data. The bounded adapter must
// own it until Apply returns; the proposed publication and returned bytes are
// copied and are not a mutable alias into caller memory.
type Request struct {
	Operation string
	Fields    map[string]any
	Content   []byte
}

func (Request) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted resume request]") }

var fields = map[string][]string{
	"upload":   {"file_name", "display_name"},
	"approved": {"file_name", "source_id", "source_sha256"},
	"attach":   {"job_id", "file_name", "label"},
	"archive":  {"resume_id", "archived"}, "version_file": {"resume_id", "require_active"},
	"delete": {"resume_id"}, "select": {"job_id", "resume_id"},
	"submitted":       {"job_id", "resume_id", "note", "occurred_at"},
	"attachment_file": {"job_id"}, "submitted_file": {"job_id"},
	"get_plan": {"job_id"}, "save_plan": {"job_id", "plan_json", "status", "expected_revision"},
	"list": {"search", "include_archived"}, "identities": {}, "get_application": {"job_id"},
}

func (r Request) validate() *Error {
	allowed, ok := fields[r.Operation]
	if !ok || r.Fields == nil {
		return unsupported("REQUEST_UNSUPPORTED")
	}
	if len(r.Content) > 0 && !slices.Contains([]string{"upload", "approved", "attach"}, r.Operation) {
		return unsupported("REQUEST_UNSUPPORTED")
	}
	if len(r.Content) > MaxResumeBytes+1 {
		return unsupported("INPUT_LIMIT")
	}
	size := 0
	for k, v := range r.Fields {
		if !slices.Contains(allowed, k) {
			return unsupported("REQUEST_UNSUPPORTED")
		}
		switch x := v.(type) {
		case string:
			if !utf8.ValidString(x) {
				return unsupported("UNICODE_UNSUPPORTED")
			}
			size += len(x)
		case nil, bool, int, int64, float64:
			size += 8
		case json.Number:
			if len(x) > 64 {
				return unsupported("INPUT_LIMIT")
			}
			size += len(x)
		default:
			return unsupported("INPUT_UNSUPPORTED")
		}
		if size > maxRequestBytes {
			return unsupported("INPUT_LIMIT")
		}
	}
	return nil
}
func value(m map[string]any, k string, fallback any) any {
	if v, ok := m[k]; ok {
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
		i, e := strconv.ParseInt(string(n), 10, 64)
		return i, e == nil
	}
	return 0, false
}
func plainName(s string) bool {
	r := []rune(s)
	return s != "" && s != "." && !strings.ContainsAny(s, "/\\\x00") && !(len(r) > 1 && r[1] == ':')
}
func suffix(s string) string {
	i := strings.LastIndexByte(s, '.')
	if i <= 0 || i == len(s)-1 {
		return ""
	}
	return textcompat.Fold(s[i:])
}
func supportedSuffix(s string) bool {
	return slices.Contains([]string{".doc", ".docx", ".pdf", ".rtf", ".txt"}, s)
}

type Upload struct{ FileName, Suffix, DisplayName string }

func NormalizeUpload(fileName any, content []byte, displayName any) (Upload, *Error) {
	var out Upload
	name, ok := fileName.(string)
	if !ok {
		return out, invalid("resume file name must be a string")
	}
	if !utf8.ValidString(name) {
		return out, unsupported("UNICODE_UNSUPPORTED")
	}
	name = textcompat.Strip(name)
	if utf8.RuneCountInString(name) > 180 || !plainName(name) {
		return out, invalid("resume must use a plain file name")
	}
	sfx := suffix(name)
	if !supportedSuffix(sfx) {
		return out, invalid("resume must be DOC, DOCX, PDF, RTF, or TXT")
	}
	if len(content) == 0 || len(content) > MaxResumeBytes {
		return out, invalid("resume must be between 1 byte and 10 MB")
	}
	display, ok := displayName.(string)
	if !ok {
		return out, invalid("resume display name must be a string")
	}
	if !utf8.ValidString(display) {
		return out, unsupported("UNICODE_UNSUPPORTED")
	}
	display = textcompat.Strip(display)
	if display == "" {
		display = name[:strings.LastIndexByte(name, '.')]
	}
	if utf8.RuneCountInString(display) > 120 {
		return out, invalid("resume display name must be 120 characters or fewer")
	}
	return Upload{name, sfx, display}, nil
}
func resumeID(v any, deleting bool) (string, *Error) {
	s, ok := v.(string)
	if !ok || textcompat.Strip(s) == "" {
		if deleting {
			return "", invalid("resume id must be a plain non-empty string")
		}
		return "", invalid("resume id must be a non-empty string")
	}
	s = textcompat.Strip(s)
	if deleting && !plainName(s) {
		return "", invalid("resume id must be a plain non-empty string")
	}
	return s, nil
}
func optionalResume(v any) (*string, *Error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok || textcompat.Strip(s) == "" {
		return nil, invalid("resume id must be a non-empty string or null")
	}
	// Reference operations use the exact identifier, unlike archive/file/delete.
	return &s, nil
}
