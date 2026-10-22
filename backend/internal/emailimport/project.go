// Package emailimport projects already parsed email candidates into Discovery
// inputs. It does not write records, open a mailbox or infer a complete JD.
package emailimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/emailalerts"
	"careerhub.local/backend/internal/emailcandidates"
)

const (
	ChannelMailbox = "gmail_imap_v1"
	ChannelFile    = "email_alert_v1"
)

var ErrProjectionInvalid = errors.New("EMAIL_IMPORT_FILE_INVALID")
var fingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ProjectCandidates preserves the Python email import's link-only Discovery
// request and metadata. The caller must filter by sender purpose before this
// call and use the native, idempotent Discovery owner to persist each request.
func ProjectCandidates(parsed emailalerts.Result, channel string) ([]discovery.CaptureRequest, error) {
	if parsed.SchemaVersion != emailalerts.SchemaVersion ||
		!fingerprintPattern.MatchString(parsed.MessageFingerprint) ||
		(channel != ChannelMailbox && channel != ChannelFile) ||
		len(parsed.Candidates) > emailcandidates.MaxCandidatesPerMessage {
		return nil, ErrProjectionInvalid
	}
	requests := make([]discovery.CaptureRequest, 0, len(parsed.Candidates))
	ordinals := make(map[int]bool, len(parsed.Candidates))
	for _, candidate := range parsed.Candidates {
		if candidate.Ordinal < 1 || ordinals[candidate.Ordinal] {
			return nil, ErrProjectionInvalid
		}
		ordinals[candidate.Ordinal] = true
		request, err := projectCandidate(parsed, candidate, channel)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}

// projectCandidate is called only after envelope validation. It never fetches
// the URL or promotes snippet text to a verified JD.
func projectCandidate(parsed emailalerts.Result, candidate emailcandidates.EnrichedCandidate, channel string) (discovery.CaptureRequest, error) {
	recommendation, err := nativeJSONValue(candidate.SourceRecommendation)
	if err != nil {
		return nil, err
	}
	facts, err := nativeJSONValue(candidate.CaptureFacts)
	if err != nil {
		return nil, err
	}
	contentScope := candidate.ContentScope
	if contentScope == "" {
		contentScope = "link_only"
	}
	provider := candidate.SourceProvider
	if provider == "" {
		provider = "generic"
	}
	section := candidate.EmailSectionKind
	if section == "" {
		section = "job_alert"
	}
	metadata := map[string]any{
		"parser_schema_version": emailalerts.SchemaVersion,
		"email_content_scope":   contentScope,
		"needs_content":         true,
		"source_provider":       provider,
		"email_section_kind":    section,
		"sender_domain":         parsed.Sender.Domain,
		"message_fingerprint":   parsed.MessageFingerprint,
		"message_id_hash":       parsed.MessageIDHash,
		"source_recommendation": recommendation,
		"capture_facts":         facts,
	}
	request := discovery.CaptureRequest{
		"source_kind":         "email_file",
		"source_channel":      channel,
		"request_fingerprint": fmt.Sprintf("%s:v%d:%d", parsed.MessageFingerprint, emailalerts.SchemaVersion, candidate.Ordinal),
		"observed_url":        candidate.SourceURL,
		"posting_identity":    candidate.PostingIdentity,
		"title":               candidate.Title,
		"company":             candidate.Company,
		"location":            candidate.Location,
		"content_scope":       "link_only",
		"source_text":         candidate.SourceText,
		"metadata":            metadata,
	}
	if parsed.ReceivedAt != "" {
		request["observed_at"] = parsed.ReceivedAt
	}
	return request, nil
}

// Discovery's strict metadata encoder accepts JSON values, not Go structs or
// typed slices. UseNumber preserves exact compensation numbers in capture facts.
func nativeJSONValue(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, ErrProjectionInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var projected any
	if err := decoder.Decode(&projected); err != nil {
		return nil, ErrProjectionInvalid
	}
	return projected, nil
}
