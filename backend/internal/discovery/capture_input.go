package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobs/joburls"
	"careerhub.local/backend/internal/textcompat"
)

// CaptureRequest preserves omitted defaults versus explicit null. Only these
// closed source fields are accepted; the transport must preserve JSON numbers.
type CaptureRequest map[string]any
type captureInput struct {
	kind, channel, request, url, canonical, identity                string
	fields                                                          Fields
	scope, text, observed, metadata, target, payloadSHA, contentSHA string
	revision                                                        int64
}

func sourceSHA(value string) string {
	if value == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
func captureSource(value any) (string, *WriteError) {
	s, ok := value.(string)
	if !ok {
		return "", invalidWrite("source_text must be a string")
	}
	if len(s) > 4<<20 {
		return "", writeFailure("request", "INPUT_LIMIT")
	}
	if !utf8.ValidString(s) {
		return "", writeFailure("request", "INPUT_UNSUPPORTED")
	}
	if strings.ContainsRune(s, 0) {
		return "", invalidWrite("source_text contains a null byte")
	}
	s = textcompat.Strip(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
	if utf8.RuneCountInString(s) > 500000 {
		return "", invalidWrite("source_text exceeds the configured character limit")
	}
	return s, nil
}
func metadataOrEmpty(value any) any {
	switch v := value.(type) {
	case nil:
		return map[string]any{}
	case bool:
		if !v {
			return map[string]any{}
		}
	case string:
		if v == "" {
			return map[string]any{}
		}
	case int:
		if v == 0 {
			return map[string]any{}
		}
	case int64:
		if v == 0 {
			return map[string]any{}
		}
	case float64:
		if v == 0 {
			return map[string]any{}
		}
	case json.Number:
		if len(v) < 4096 {
			n, err := v.Float64()
			if err == nil && n == 0 {
				return map[string]any{}
			}
		}
	case []any:
		if len(v) == 0 {
			return map[string]any{}
		}
	}
	return value
}
func normalizeCapture(ctx context.Context, input CaptureRequest, now string) (captureInput, *WriteError) {
	var out captureInput
	if ctx == nil {
		return out, writeFailure("context", "CONTEXT_REQUIRED")
	}
	if ctx.Err() != nil {
		return out, writeFailure("context", "WRITE_CANCELED")
	}
	allowed := []string{"source_kind", "source_channel", "request_fingerprint", "observed_url", "posting_identity", "title", "company", "location", "content_scope", "source_text", "observed_at", "metadata", "target_discovery_id", "expected_target_revision"}
	if len(input) > len(allowed) {
		return out, writeFailure("request", "CAPTURE_FIELDS_UNSUPPORTED")
	}
	for k := range input {
		if !slices.Contains(allowed, k) {
			return out, writeFailure("request", "CAPTURE_FIELDS_UNSUPPORTED")
		}
	}
	get := func(key string, fallback any) any {
		if v, ok := input[key]; ok {
			return v
		}
		return fallback
	}
	var ok bool
	out.kind, ok = input["source_kind"].(string)
	if !ok || !slices.Contains([]string{"extension", "email_file", "manual", "watcher", "public_resolver"}, out.kind) {
		return out, invalidWrite("source_kind is unsupported")
	}
	var e *WriteError
	out.channel, e = writeText(input["source_channel"], "source_channel", 100)
	if e != nil {
		return out, e
	}
	out.request, e = writeText(input["request_fingerprint"], "request_fingerprint", 300)
	if e != nil {
		return out, e
	}
	if out.channel == "" || out.request == "" {
		return out, invalidWrite("source_channel and request_fingerprint are required")
	}
	out.scope, ok = get("content_scope", "link_only").(string)
	if !ok || !slices.Contains([]string{"link_only", "selection", "page_visible", "confirmed_full"}, out.scope) {
		return out, invalidWrite("content_scope is unsupported")
	}
	out.target, e = writeText(get("target_discovery_id", ""), "target_discovery_id", 300)
	if e != nil {
		return out, e
	}
	if out.target != "" {
		if out.target, e = writeID(out.target, "target_discovery_id"); e != nil {
			return out, e
		}
		if out.kind != "public_resolver" || out.scope != "page_visible" {
			return out, invalidWrite("Only public_resolver page content may target an exact Discovery")
		}
		out.revision, e = revisionInput(input["expected_target_revision"], false)
		if e != nil {
			if e.SourceClass != "" {
				e = invalidWrite("expected_target_revision must be a positive integer")
			}
			return out, e
		}
	} else if input["expected_target_revision"] != nil {
		return out, invalidWrite("expected_target_revision requires target_discovery_id")
	}
	out.url, e = writeText(get("observed_url", ""), "observed_url", 4096)
	if e != nil {
		return out, e
	}
	posting, err := joburls.Resolve(out.url)
	if err != nil {
		return out, writeFailure("request", "INPUT_UNSUPPORTED")
	}
	if posting.URL != "" {
		out.url = posting.URL
	}
	out.canonical, err = joburls.CanonicalDiscovery(out.url)
	if err != nil {
		switch err {
		case joburls.ErrDiscoveryURL, joburls.ErrURLCredentials:
			return out, invalidWrite(err.Error())
		default:
			return out, writeFailure("request", "INPUT_UNSUPPORTED")
		}
	}
	var identity any = get("posting_identity", "")
	if posting.Identity != "" {
		identity = posting.Identity
	}
	out.identity, e = writeText(identity, "posting_identity", 300)
	if e != nil {
		return out, e
	}
	out.identity = textcompat.Fold(out.identity)
	for _, field := range []struct {
		name string
		dest *string
	}{{"title", &out.fields.Title}, {"company", &out.fields.Company}, {"location", &out.fields.Location}} {
		*field.dest, e = writeText(get(field.name, ""), field.name, 500)
		if e != nil {
			return out, e
		}
	}
	out.text, e = captureSource(get("source_text", ""))
	if e != nil {
		return out, e
	}
	if out.scope != "link_only" && out.text == "" {
		return out, invalidWrite("Partial or complete content scopes require source_text")
	}
	if out.canonical == "" && out.identity == "" && out.text == "" && out.fields.Title == "" && out.fields.Company == "" {
		return out, invalidWrite("Discovery capture must contain a source identity or visible content")
	}
	out.observed = now
	if value := input["observed_at"]; value != nil {
		s, ok := value.(string)
		if !ok || textcompat.Strip(s) == "" {
			return out, invalidWrite("observed_at must be a non-empty ISO timestamp")
		}
		var failure *Error
		out.observed, failure = normalizedTimestamp(s)
		if failure != nil {
			if failure.Code == "INVALID_TIMESTAMP" {
				return out, invalidWrite("observed_at must use ISO format")
			}
			return out, fromRead(failure)
		}
	}
	metadata, ok := metadataOrEmpty(input["metadata"]).(map[string]any)
	if !ok {
		return out, invalidWrite("metadata must be a mapping")
	}
	out.metadata, e = captureJSON(ctx, metadata, 4096, true)
	if e != nil {
		return out, e
	}
	payload := map[string]any{"source_kind": out.kind, "source_channel": out.channel, "observed_url": out.url, "posting_identity": out.identity, "title": out.fields.Title, "company": out.fields.Company, "location": out.fields.Location, "content_scope": out.scope, "source_text": out.text, "metadata": metadata, "target_discovery_id": out.target}
	raw, e := captureJSON(ctx, payload, 4<<20, false)
	if e != nil {
		return out, e
	}
	out.payloadSHA, out.contentSHA = sourceSHA(raw), sourceSHA(out.text)
	return out, nil
}
