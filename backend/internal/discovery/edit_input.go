package discovery

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

// WriteError contains fixed diagnostics and separately controlled source-domain
// metadata. No native SQLite cause or captured content is retained or formatted.
type WriteError struct{ Stage, Code, SourceClass, SourceMessage string }

func (e *WriteError) Error() string               { return "Discovery operation could not be completed." }
func (e *WriteError) Format(s fmt.State, _ rune)  { _, _ = io.WriteString(s, e.Error()) }
func writeFailure(stage, code string) *WriteError { return &WriteError{Stage: stage, Code: code} }
func sourceFailure(class, message string) *WriteError {
	return &WriteError{Stage: "request", Code: "DISCOVERY_WRITE_REJECTED", SourceClass: class, SourceMessage: message}
}
func invalidWrite(message string) *WriteError { return sourceFailure("DiscoveryError", message) }
func fromRead(e *Error) *WriteError {
	if e == nil {
		return nil
	}
	return writeFailure(e.Stage, e.Code)
}

// EditRequest is a closed use-case selector, not an arbitrary SQL request.
// Promotion links an ALREADY registered job; it never submits an application.
type EditRequest struct {
	Operation        string
	DiscoveryID      any
	JobID            any
	State            any
	Overrides        any
	ExpectedRevision any
}
type edit struct {
	op, id, job, state string
	revision           int64
	fields             [3]*string
}

func writeText(value any, field string, max int) (string, *WriteError) {
	text, ok := value.(string)
	if !ok {
		return "", invalidWrite(field + " must be a string")
	}
	normalized, e := plainText(text, max)
	if e == nil {
		return normalized, nil
	}
	if e.Code == "INVALID_TEXT" {
		return "", invalidWrite(field + " is invalid or exceeds its limit")
	}
	return "", fromRead(e)
}
func writeID(normalized, field string) (string, *WriteError) {
	id, e := NormalizeID(normalized)
	if e != nil {
		return "", invalidWrite(field + " must be a plain identifier")
	}
	return id, nil
}
func revisionInput(value any, zero bool) (int64, *WriteError) {
	var n int64
	valid := true
	switch v := value.(type) {
	case int:
		n = int64(v)
	case int64:
		n = v
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			valid = false
			break
		}
		var e error
		n, e = strconv.ParseInt(string(v), 10, 64)
		if e != nil {
			return 0, writeFailure("request", "REVISION_LIMIT")
		}
	default:
		valid = false
	}
	if !valid || n < 0 || !zero && n == 0 {
		if zero {
			return 0, invalidWrite("expected_revision must be a non-negative integer")
		}
		return 0, invalidWrite("expected_revision must be a positive integer")
	}
	return n, nil
}
func normalizeEdit(input EditRequest) (edit, *WriteError) {
	out := edit{op: input.Operation}
	if !slices.Contains([]string{"transition", "overrides", "confirm", "prepare", "promote"}, out.op) {
		return out, writeFailure("request", "OPERATION_UNSUPPORTED")
	}
	var e *WriteError
	out.id, e = writeText(input.DiscoveryID, "discovery_id", 300)
	if e != nil {
		return out, e
	}
	if out.op == "promote" {
		out.job, e = writeText(input.JobID, "job_id", 300)
		if e != nil {
			return out, e
		}
	}
	if out.id, e = writeID(out.id, "discovery_id"); e != nil {
		return out, e
	}
	if out.op == "promote" {
		if out.job, e = writeID(out.job, "job_id"); e != nil {
			return out, e
		}
	}
	if out.op == "transition" {
		var ok bool
		out.state, ok = input.State.(string)
		if !ok || !slices.Contains([]string{"inbox", "shortlisted", "dismissed"}, out.state) {
			return out, invalidWrite("state cannot be set through the discovery lifecycle editor")
		}
	}
	var values map[string]any
	if out.op == "overrides" {
		var ok bool
		values, ok = input.Overrides.(map[string]any)
		if !ok || len(values) != 3 {
			return out, invalidWrite("overrides must contain only title, company, and location")
		}
		for _, key := range []string{"title", "company", "location"} {
			if _, ok := values[key]; !ok {
				return out, invalidWrite("overrides must contain only title, company, and location")
			}
		}
	}
	out.revision, e = revisionInput(input.ExpectedRevision, out.op == "overrides")
	if e != nil {
		return out, e
	}
	if out.op == "overrides" {
		for i, key := range []string{"title", "company", "location"} {
			if values[key] == nil {
				continue
			}
			text, e := writeText(values[key], "overrides."+key, 500)
			if e != nil {
				return out, e
			}
			if text == "" {
				return out, invalidWrite("overrides." + key + " must be non-empty or null to clear")
			}
			out.fields[i] = &text
		}
	}
	return out, nil
}
func nextRevision(current int64) *WriteError {
	if current == math.MaxInt64 {
		return writeFailure("revision", "REVISION_LIMIT")
	}
	return nil
}
