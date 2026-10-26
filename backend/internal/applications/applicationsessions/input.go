// Package applicationsessions owns transaction-local attended session state.
// The caller owns admission, schema, transaction, authentication and cleanup.
// It neither submits forms nor treats detection as permission to mark Applied.
package applicationsessions

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/isotime"
	"careerhub.local/backend/internal/jobs/joburls"
	"careerhub.local/backend/internal/textcompat"
)

const ContractVersion = 1
const maxRequestBytes = 16384
const maxRows = 10000
const maxScanBytes = 8 << 20

// Errors never format SQL, capabilities, job text or native causes. Source
// metadata is solely for controlled adapter mapping and compatibility tests.
type Error struct{ Stage, Code, SourceClass, SourceMessage string }

func (e *Error) Error() string              { return "Application session operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func problem(kind, message string) *Error {
	names := map[string]string{"INVALID": "Error", "NOT_FOUND": "NotFound", "NOT_PENDING": "ClaimUnavailable", "CLAIM_AMBIGUOUS": "ClaimAmbiguous", "UNAUTHORIZED": "Unauthorized", "EXPIRED": "Expired", "REVISION_CONFLICT": "RevisionConflict", "EVENT_CONFLICT": "EventConflict"}
	return &Error{"session", "APPLICATION_SESSION_" + kind, "ApplicationSession" + names[kind], message}
}
func invalid(message string) *Error { return problem("INVALID", message) }
func unsupported(code string) *Error {
	return &Error{Stage: "bounds", Code: "APPLICATION_SESSION_" + code}
}

func scalar(value any, maxBytes int) (string, bool) {
	s, ok := value.(string)
	return s, ok && utf8.ValidString(s) && len(s) <= maxBytes
}
func integer(value any) (int64, bool) {
	switch n := value.(type) {
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
	default:
		return 0, false
	}
}
func uuid(value any, field string) (string, *Error) {
	s, ok := scalar(value, 256)
	if !ok {
		return "", invalid(field + " must be a UUID")
	}
	// UUID's accepted wrappers determine the distinction between invalid hex
	// and valid UUID encoded in a noncanonical form. Only canonical ASCII
	// identifiers can ever leave this boundary.
	raw := strings.ReplaceAll(strings.ReplaceAll(s, "urn:", ""), "uuid:", "")
	raw = strings.ReplaceAll(strings.Trim(raw, "{}"), "-", "")
	bytes, e := hex.DecodeString(raw)
	if e != nil || len(bytes) != 16 {
		return "", invalid(field + " must be a UUID")
	}
	raw = hex.EncodeToString(bytes)
	normalized := raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
	if normalized != textcompat.Fold(s) {
		return "", invalid(field + " must use canonical UUID form")
	}
	return normalized, nil
}
func sessionID(value any) (string, *Error) {
	s, ok := scalar(value, 52)
	if !ok || len(s) != 52 || !strings.HasPrefix(s, "application_session_") {
		return "", invalid("session_id is invalid")
	}
	for _, c := range s[20:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", invalid("session_id is invalid")
		}
	}
	return s, nil
}
func tabID(value any) (int64, *Error) {
	n, ok := integer(value)
	if !ok || n < 0 || n > 2147483647 {
		return 0, invalid("tab_id must be a non-negative browser tab id")
	}
	return n, nil
}
func token(value any) (string, *Error) {
	s, ok := scalar(value, 1024)
	n := utf8.RuneCountInString(s)
	if !ok || n < 32 || n > 256 || strings.ContainsFunc(s, textcompat.IsSpace) {
		return "", problem("UNAUTHORIZED", "Application session capability is invalid")
	}
	return s, nil
}
func timestamp(value any, field string) (time.Time, *Error) {
	s, ok := scalar(value, 256)
	if !ok || textcompat.Strip(s) == "" {
		return time.Time{}, invalid(field + " must be an ISO timestamp")
	}
	t, aware, e := isotime.Parse(strings.ReplaceAll(textcompat.Strip(s), "Z", "+00:00"), false)
	if e != nil {
		return time.Time{}, invalid(field + " must be an ISO timestamp")
	}
	if !aware {
		return time.Time{}, invalid(field + " must include a timezone")
	}
	return t, nil
}
func origin(value any) (string, *Error) {
	s, ok := scalar(value, maxRequestBytes)
	if ok {
		if v, e := joburls.SessionOrigin(s, true); e == nil {
			return v, nil
		}
	}
	return "", invalid("page_origin must be an HTTP/HTTPS origin")
}
func currentURL(value any) (string, string, *Error) {
	s, ok := scalar(value, maxRequestBytes)
	if !ok {
		return "", "", invalid("current_url must be an absolute HTTP/HTTPS URL")
	}
	s = strings.Join(strings.FieldsFunc(s, textcompat.IsSpace), " ")
	if strings.ContainsRune(s, 0) || utf8.RuneCountInString(s) > 4096 {
		return "", "", invalid("current_url must be an absolute HTTP/HTTPS URL")
	}
	canonical, e := joburls.CanonicalDiscovery(s)
	if e != nil {
		return "", "", invalid("current_url must be an absolute HTTP/HTTPS URL")
	}
	if canonical == "" {
		return "", "", invalid("current_url must not be empty")
	}
	o, e := joburls.SessionOrigin(canonical, false)
	if e != nil {
		return "", "", invalid("current_url has an invalid origin")
	}
	return canonical, o, nil
}

// Request is an internal command, not a public HTTP decoder. Exact fields are
// enforced here too: an extension claim cannot smuggle a caller-chosen job_id.
// A trusted adapter must separately authorize create/review/resolution commands.
type Request struct {
	Operation string
	Fields    map[string]any
}

func (Request) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted application-session request]")
}

func (r Request) validate() *Error {
	fields, ok := map[string]string{
		"create": "job_id", "latest": "job_id", "reviews": "",
		"claim": "request_id current_url tab_id", "claim_create": "request_id current_url tab_id",
		"signal":    "session_id session_token tab_id page_origin event_key success_rule submit_observed_at success_observed_at",
		"interrupt": "session_id session_token tab_id page_origin reason",
		"confirm":   "session_id expected_revision", "dismiss": "session_id expected_revision", "cancel": "session_id expected_revision",
	}[r.Operation]
	if !ok {
		return unsupported("OPERATION_INVALID")
	}
	expected := strings.Fields(fields)
	if len(r.Fields) != len(expected) {
		return unsupported("FIELDS_INVALID")
	}
	total := 0
	for _, k := range expected {
		v, exists := r.Fields[k]
		if !exists {
			return unsupported("FIELDS_INVALID")
		}
		switch value := v.(type) {
		case string:
			if !utf8.ValidString(value) {
				return unsupported("UNICODE_UNSUPPORTED")
			}
			total += len(value)
		case json.Number:
			if len(value) > 256 || !utf8.ValidString(string(value)) {
				return unsupported("INPUT_LIMIT")
			}
			total += len(value)
		case nil, bool, int, int64, float64:
			total += 32
		default:
			return unsupported("VALUE_UNSUPPORTED")
		}
	}
	if total > maxRequestBytes {
		return unsupported("INPUT_LIMIT")
	}
	return nil
}
