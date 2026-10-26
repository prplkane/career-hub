package httpapi

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/jobs/joburls"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vault/calendarinput"
)

func extensionCaptureRequest(p map[string]json.RawMessage) (ExtensionRequest, string) {
	r := ExtensionRequest{Operation: "capture"}
	invalid := func(code string) (ExtensionRequest, string) { return ExtensionRequest{}, code }
	const bad = "EXTENSION_CAPTURE_INVALID"
	const unsupported = "EXTENSION_CAPTURE_PROTOCOL_UNSUPPORTED"
	if !materialKeysExact(p, "contract_version", "request_id", "capture_kind", "destination", "observed_url", "page_title", "captured_at_client", "source_text", "structured", "extractor") {
		return invalid(bad)
	}
	version, ok := materialRevision(p["contract_version"])
	if !ok || version != 1 {
		return invalid("EXTENSION_CAPTURE_CONTRACT_UNSUPPORTED")
	}
	text := func(raw json.RawMessage, max int, collapse bool) (string, bool) {
		var s string
		if string(raw) == "null" || json.Unmarshal(raw, &s) != nil || strings.ContainsRune(s, 0) {
			return "", false
		}
		if collapse {
			s = strings.Join(strings.FieldsFunc(s, textcompat.IsSpace), " ")
		}
		return s, utf8.RuneCountInString(s) <= max
	}
	id, ok := text(p["request_id"], 36, false)
	if !ok || len(id) != 36 {
		return invalid(bad)
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return invalid(bad)
			}
		} else if !strings.ContainsRune("0123456789abcdef", c) {
			return invalid(bad)
		}
	}
	kind, ok := text(p["capture_kind"], 16, false)
	destination, destinationOK := text(p["destination"], 16, false)
	if !ok || kind != "link" && kind != "selection" && kind != "page" || !destinationOK || destination != "feed" {
		return invalid(unsupported)
	}
	observed, ok := text(p["observed_url"], 4096, false)
	if !ok || textcompat.Strip(observed) == "" {
		return invalid(bad)
	}
	observed = textcompat.Strip(observed)
	parsed, err := url.Parse(observed)
	if err != nil {
		return invalid(bad)
	}
	if strings.ToLower(parsed.Scheme) != "http" && strings.ToLower(parsed.Scheme) != "https" {
		return invalid(unsupported)
	}
	if parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil {
		return invalid(bad)
	}
	if port := parsed.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n > 65535 {
			return invalid(bad)
		}
	}
	title, ok := text(p["page_title"], 500, true)
	if !ok {
		return invalid(bad)
	}
	stamp, ok := text(p["captured_at_client"], 4096, false)
	if !ok {
		return invalid(bad)
	}
	stamp = textcompat.Strip(stamp)
	isoStamp := strings.ReplaceAll(stamp, "Z", "+00:00")
	if !calendarinput.ValidAwareISODateTime(isoStamp) {
		if _, valid := calendarinput.ISODateTimeUTCSeconds(isoStamp); !valid {
			return invalid(bad)
		}
	}
	source, ok := text(p["source_text"], 500000, false)
	if !ok {
		return invalid(bad)
	}
	source = textcompat.Strip(strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n"))
	if kind == "link" && source != "" || kind != "link" && source == "" {
		return invalid(bad)
	}
	var structured, extractor map[string]json.RawMessage
	if json.Unmarshal(p["structured"], &structured) != nil || !materialKeysExact(structured, "title", "company", "location", "posting_id") {
		return invalid(bad)
	}
	normal := map[string]string{}
	// The discovery owner's bounded JSON encoder accepts decoded JSON types.
	// Keep this an array of any even when empty; []string marshals identically
	// but is deliberately not an admitted internal metadata type.
	present := []any{}
	for _, key := range []string{"company", "location", "posting_id", "title"} {
		max := 500
		if key == "posting_id" {
			max = 300
		}
		value, valid := text(structured[key], max, true)
		if !valid {
			return invalid(bad)
		}
		normal[key] = value
		if value != "" {
			present = append(present, key)
		}
	}
	if kind != "page" && len(present) != 0 {
		return invalid(bad)
	}
	if json.Unmarshal(p["extractor"], &extractor) != nil || !materialKeysExact(extractor, "version", "strategy") {
		return invalid(bad)
	}
	extractorVersion, ok := materialRevision(extractor["version"])
	if !ok || extractorVersion != 1 && extractorVersion != 2 {
		return invalid(unsupported)
	}
	strategy, ok := text(extractor["strategy"], 32, false)
	if !ok {
		return invalid(unsupported)
	}
	strategies := ""
	if kind == "page" {
		if extractorVersion == 1 {
			strategies = " jobposting_jsonld semantic_container visible_body "
		} else {
			strategies = " jobposting_jsonld provider_container ranked_container guarded_body "
		}
	} else {
		if extractorVersion != 1 {
			return invalid(unsupported)
		}
		strategies = " " + kind + " "
	}
	if strategy == "" || strings.Contains(strategy, " ") || !strings.Contains(strategies, " "+strategy+" ") {
		return invalid(unsupported)
	}
	provider, err := joburls.ProviderName(observed)
	if err != nil {
		return invalid(bad)
	}
	identity := ""
	if normal["posting_id"] != "" {
		identity = provider + ":" + textcompat.Fold(normal["posting_id"])
		if provider == "" || utf8.RuneCountInString(identity) > 300 {
			return invalid(bad)
		}
	}
	if normal["title"] != "" {
		title = normal["title"]
	}
	r.Scope = map[string]string{"link": "link_only", "selection": "selection", "page": "page_visible"}[kind]
	r.Readiness = "needs_review"
	if kind == "link" {
		r.Readiness = "needs_content"
	}
	r.ContentChars = utf8.RuneCountInString(source)
	r.Capture = discovery.CaptureRequest{"source_kind": "extension", "source_channel": kind, "request_fingerprint": id, "observed_url": observed, "posting_identity": identity, "title": title, "company": normal["company"], "location": normal["location"], "content_scope": r.Scope, "source_text": source, "observed_at": stamp, "metadata": map[string]any{"capture_contract_version": 1, "capture_kind": kind, "extractor": map[string]any{"version": extractorVersion, "strategy": strategy}, "source_provider": provider, "structured_fields": present}}
	return r, ""
}
