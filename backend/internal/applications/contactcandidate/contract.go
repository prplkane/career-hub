// Package contactcandidate owns the pure contract and projections for one
// source-backed JD contact candidate. It performs no filesystem, SQL or HTTP
// access and grants no mutation authority.
package contactcandidate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/textcompat"
)

const ContractVersion = 1

// MaxRequestBytes is shared with the local HTTP adapter so the transport
// cannot accept an envelope that the pure contract would later reject.
const MaxRequestBytes = 256 << 10

const (
	requestInvalid      = "CONTACT_CANDIDATE_REQUEST_INVALID"
	protocolUnsupported = "CONTACT_CANDIDATE_PROTOCOL_UNSUPPORTED"
	sourceIncomplete    = "CONTACT_CANDIDATE_SOURCE_INCOMPLETE"
	sourceInvalid       = "CONTACT_CANDIDATE_SOURCE_INVALID"
	staleCandidate      = "CONTACT_CANDIDATE_STALE"
)

type Problem struct {
	Code    string
	Status  int
	Message string
}

func (p *Problem) Error() string { return p.Message }

func problem(code string, status int, message string) *Problem {
	return &Problem{Code: code, Status: status, Message: message}
}

type SourceRequest struct {
	SourceKind            string `json:"source_kind"`
	SourceID              string `json:"source_id"`
	CandidateID           string `json:"candidate_id"`
	ExpectedContentSHA256 string `json:"expected_content_sha256"`
}

type ConfirmationRequest struct {
	SourceRequest
	Action           string         `json:"action"`
	Contact          map[string]any `json:"contact,omitempty"`
	ContactID        string         `json:"contact_id,omitempty"`
	ExpectedRevision int64          `json:"expected_revision,omitempty"`
}

type Source struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Company string `json:"company"`
}

// Material is the guarded source owner's pure handoff. Complete material has
// already been decoded and path-checked; this package only re-extracts it.
type Material struct {
	State      string `json:"state"`
	Source     Source `json:"source"`
	SourceText string `json:"source_text"`
}

type Defaults struct {
	Name     string                        `json:"name"`
	Role     string                        `json:"role"`
	Company  string                        `json:"company"`
	Context  string                        `json:"context"`
	Channels []applicationanalysis.Channel `json:"channels"`
}

type Resolved struct {
	Source          Source                               `json:"source"`
	Candidate       applicationanalysis.ContactCandidate `json:"candidate"`
	Defaults        Defaults                             `json:"defaults"`
	SourceReference string                               `json:"source_reference"`
	ContentSHA256   string                               `json:"content_sha256"`
}

type StatedFields struct {
	Name     string                        `json:"name"`
	Role     string                        `json:"role"`
	Company  string                        `json:"company"`
	Context  string                        `json:"context"`
	Channels []applicationanalysis.Channel `json:"channels"`
}

type Provenance struct {
	Origin              string       `json:"origin"`
	SourceReference     string       `json:"source_reference"`
	DiscoveryID         string       `json:"discovery_id"`
	JobID               string       `json:"job_id"`
	SourceContentSHA256 string       `json:"source_content_sha256"`
	SourceText          string       `json:"source_text"`
	LineStart           int          `json:"line_start"`
	LineEnd             int          `json:"line_end"`
	StatedFields        StatedFields `json:"stated_fields"`
}

type Projection struct {
	Status int            `json:"status"`
	Body   map[string]any `json:"body"`
}

func decodeObject(raw []byte) (map[string]any, *Problem) {
	if len(raw) == 0 || len(raw) > MaxRequestBytes || !utf8.Valid(raw) {
		return nil, problem(requestInvalid, 400, "Contact candidate request fields are invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, problem(requestInvalid, 400, "Contact candidate request fields are invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, problem(requestInvalid, 400, "Contact candidate request fields are invalid")
	}
	return payload, nil
}

func exactKeys(payload map[string]any, keys ...string) bool {
	if len(payload) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := payload[key]; !ok {
			return false
		}
	}
	return true
}

// Python's current contract compares JSON scalars to integer 1 directly.
// Preserve the characterized true/1.0 compatibility until a versioned change.
func supportedVersion(value any) bool {
	switch item := value.(type) {
	case bool:
		return item
	case json.Number:
		number, err := strconv.ParseFloat(string(item), 64)
		return err == nil && number == 1
	default:
		return false
	}
}

func plainIdentifier(value any) (string, bool) {
	item, ok := value.(string)
	if !ok || !utf8.ValidString(item) {
		return "", false
	}
	item = textcompat.Strip(item)
	runes := []rune(item)
	if len(runes) == 0 || len(runes) > 256 || item == "." || strings.ContainsAny(item, "/\\\x00") || len(runes) >= 2 && runes[1] == ':' {
		return "", false
	}
	return item, true
}

func digestValue(value any) (string, bool) {
	item, ok := value.(string)
	if !ok || !utf8.ValidString(item) {
		return "", false
	}
	item = strings.ToLower(textcompat.Strip(item))
	if len(item) != 64 {
		return "", false
	}
	for _, char := range item {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return "", false
		}
	}
	return item, true
}

func sourceRequest(payload map[string]any) (SourceRequest, *Problem) {
	if !supportedVersion(payload["contract_version"]) {
		return SourceRequest{}, problem(protocolUnsupported, 400, "Contact candidate contract version is unsupported")
	}
	kind, ok := payload["source_kind"].(string)
	if !ok || kind != "discovery" && kind != "job" {
		return SourceRequest{}, problem(requestInvalid, 400, "source_kind is unsupported")
	}
	id, ok := plainIdentifier(payload["source_id"])
	if !ok {
		return SourceRequest{}, problem(requestInvalid, 400, "source_id must be a plain identifier")
	}
	candidate, ok := digestValue(payload["candidate_id"])
	if !ok {
		return SourceRequest{}, problem(requestInvalid, 400, "candidate_id must be a SHA-256 digest")
	}
	content, ok := digestValue(payload["expected_content_sha256"])
	if !ok {
		return SourceRequest{}, problem(requestInvalid, 400, "expected_content_sha256 must be a SHA-256 digest")
	}
	return SourceRequest{
		SourceKind: kind, SourceID: id, CandidateID: candidate,
		ExpectedContentSHA256: content,
	}, nil
}

// ValidateSourceRequest protects non-HTTP first-party callers that already
// hold a decoded v1 request. It does not infer or normalize missing fields.
func ValidateSourceRequest(request SourceRequest) *Problem {
	if request.SourceKind != "discovery" && request.SourceKind != "job" {
		return problem(requestInvalid, 400, "source_kind is unsupported")
	}
	id, ok := plainIdentifier(request.SourceID)
	if !ok || id != request.SourceID {
		return problem(requestInvalid, 400, "source_id must be a plain identifier")
	}
	candidate, ok := digestValue(request.CandidateID)
	if !ok || candidate != request.CandidateID {
		return problem(requestInvalid, 400, "candidate_id must be a SHA-256 digest")
	}
	content, ok := digestValue(request.ExpectedContentSHA256)
	if !ok || content != request.ExpectedContentSHA256 {
		return problem(requestInvalid, 400, "expected_content_sha256 must be a SHA-256 digest")
	}
	return nil
}

// ValidateConfirmationRequest protects first-party callers that already hold a
// decoded v1 request. Contact values remain owned by applicationcontacts; this
// check only preserves the exact action shape produced by DecodeConfirmation.
func ValidateConfirmationRequest(request ConfirmationRequest) *Problem {
	if p := ValidateSourceRequest(request.SourceRequest); p != nil {
		return p
	}
	switch request.Action {
	case "create":
		if request.ContactID != "" || request.ExpectedRevision != 0 || request.Contact == nil ||
			!exactKeys(request.Contact, "name", "role", "company", "context", "channels") {
			return problem(requestInvalid, 400, "contact must contain editable facts and channels")
		}
	case "link_existing":
		contactID := textcompat.Strip(request.ContactID)
		if request.Contact != nil || !utf8.ValidString(request.ContactID) || contactID == "" || contactID != request.ContactID {
			return problem(requestInvalid, 400, "contact_id is required")
		}
		if request.ExpectedRevision <= 0 {
			return problem(requestInvalid, 400, "expected_revision must be a positive integer")
		}
	default:
		return problem(requestInvalid, 400, "Contact confirmation action is unsupported")
	}
	return nil
}

func DecodePreview(raw []byte) (SourceRequest, *Problem) {
	payload, p := decodeObject(raw)
	if p != nil {
		return SourceRequest{}, p
	}
	if !exactKeys(payload, "contract_version", "source_kind", "source_id", "candidate_id", "expected_content_sha256") {
		return SourceRequest{}, problem(requestInvalid, 400, "Contact candidate request fields are invalid")
	}
	return sourceRequest(payload)
}

func DecodeConfirmation(raw []byte) (ConfirmationRequest, *Problem) {
	payload, p := decodeObject(raw)
	if p != nil {
		return ConfirmationRequest{}, p
	}
	action, _ := payload["action"].(string)
	base := []string{"contract_version", "source_kind", "source_id", "candidate_id", "expected_content_sha256", "action"}
	switch action {
	case "create":
		if !exactKeys(payload, append(base, "contact")...) {
			return ConfirmationRequest{}, problem(requestInvalid, 400, "Contact confirmation fields are invalid")
		}
	case "link_existing":
		if !exactKeys(payload, append(base, "contact_id", "expected_revision")...) {
			return ConfirmationRequest{}, problem(requestInvalid, 400, "Contact confirmation fields are invalid")
		}
	default:
		return ConfirmationRequest{}, problem(requestInvalid, 400, "Contact confirmation action is unsupported")
	}
	request, p := sourceRequest(payload)
	if p != nil {
		return ConfirmationRequest{}, p
	}
	result := ConfirmationRequest{SourceRequest: request, Action: action}
	if action == "create" {
		contact, ok := payload["contact"].(map[string]any)
		if !ok || !exactKeys(contact, "name", "role", "company", "context", "channels") {
			return ConfirmationRequest{}, problem(requestInvalid, 400, "contact must contain editable facts and channels")
		}
		result.Contact = contact
		return result, nil
	}
	contactID, ok := payload["contact_id"].(string)
	if !ok || textcompat.Strip(contactID) == "" {
		return ConfirmationRequest{}, problem(requestInvalid, 400, "contact_id is required")
	}
	revision, ok := positiveInteger(payload["expected_revision"])
	if !ok {
		return ConfirmationRequest{}, problem(requestInvalid, 400, "expected_revision must be a positive integer")
	}
	result.ContactID = textcompat.Strip(contactID)
	result.ExpectedRevision = revision
	return result, nil
}

func positiveInteger(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok || strings.ContainsAny(string(number), ".eE") {
		return 0, false
	}
	parsed, err := strconv.ParseInt(string(number), 10, 64)
	return parsed, err == nil && parsed > 0
}

var contextLabels = map[string]string{
	"application_channel":   "Application channel",
	"application_recipient": "Application recipient",
	"recruiter":             "Recruiter",
	"hiring_manager":        "Hiring manager",
	"possible_team_contact": "Possible team contact",
}

func Resolve(material Material, request SourceRequest) (Resolved, *Problem) {
	if p := ValidateSourceRequest(request); p != nil {
		return Resolved{}, p
	}
	if material.Source.Kind != request.SourceKind || material.Source.ID != request.SourceID {
		return Resolved{}, problem(sourceInvalid, 409, "The current job source is unavailable")
	}
	if material.State == "incomplete" && request.SourceKind == "discovery" {
		return Resolved{}, problem(sourceIncomplete, 409, "A complete JD is required before saving a contact")
	}
	if material.State != "complete" {
		return Resolved{}, problem(sourceInvalid, 409, "Invalid job cards cannot provide contact evidence")
	}
	analysis, err := applicationanalysis.ExtractContacts(material.SourceText)
	if err != nil {
		return Resolved{}, problem(sourceInvalid, 409, "The current job source is unavailable")
	}
	if analysis.ContentSHA256 != request.ExpectedContentSHA256 {
		return Resolved{}, problem(staleCandidate, 409, "The JD changed after this contact candidate was loaded. Review it again.")
	}
	var candidate *applicationanalysis.ContactCandidate
	for index := range analysis.Candidates {
		if analysis.Candidates[index].CandidateID == request.CandidateID {
			candidate = &analysis.Candidates[index]
			break
		}
	}
	if candidate == nil {
		return Resolved{}, problem(staleCandidate, 409, "This contact candidate is no longer present in the current JD analysis")
	}
	company := candidate.Company
	if company == "" {
		company = material.Source.Company
	}
	context := contextLabels[candidate.ContextKind]
	if context == "" {
		context = "Job contact"
	}
	sourceKey := sha256.Sum256([]byte(request.SourceID))
	return Resolved{
		Source:    material.Source,
		Candidate: *candidate,
		Defaults: Defaults{
			Name: candidate.Name, Role: candidate.Role, Company: company, Context: context,
			Channels: candidate.Channels,
		},
		SourceReference: request.SourceKind + ":" + hex.EncodeToString(sourceKey[:]) + ":" + request.CandidateID,
		ContentSHA256:   analysis.ContentSHA256,
	}, nil
}

func CandidateProvenance(resolved Resolved, request SourceRequest) Provenance {
	provenance := Provenance{
		Origin: "jd_candidate", SourceReference: resolved.SourceReference,
		SourceContentSHA256: resolved.ContentSHA256, SourceText: resolved.Candidate.SourceText,
		LineStart: resolved.Candidate.LineStart, LineEnd: resolved.Candidate.LineEnd,
		StatedFields: StatedFields{
			Name: resolved.Candidate.Name, Role: resolved.Candidate.Role,
			Company: resolved.Candidate.Company, Context: resolved.Candidate.ContextKind,
			Channels: resolved.Candidate.Channels,
		},
	}
	if request.SourceKind == "discovery" {
		provenance.DiscoveryID = request.SourceID
	} else {
		provenance.JobID = request.SourceID
	}
	return provenance
}

func ProjectPreview(resolved Resolved, matches any, claimedContact any) map[string]any {
	return map[string]any{
		"contract_version": ContractVersion,
		"source":           resolved.Source, "candidate": resolved.Candidate, "defaults": resolved.Defaults,
		"matches": matches, "claimed_contact": claimedContact,
	}
}

// ProjectClaimed returns handled=false when the candidate is not yet claimed.
func ProjectClaimed(request ConfirmationRequest, claimed map[string]any) (Projection, bool, *Problem) {
	if claimed == nil {
		return Projection{}, false, nil
	}
	if request.Action == "link_existing" {
		claimedID, _ := claimed["id"].(string)
		if request.ContactID != claimedID {
			return Projection{}, true, problem("CONTACT_CONFLICT", 409, "This JD contact candidate is already linked to another contact")
		}
	}
	return Projection{Status: 200, Body: map[string]any{
		"contract_version": ContractVersion, "outcome": "provenance_match", "contact": claimed,
	}}, true, nil
}

func ProjectMutation(action string, result map[string]any) (Projection, *Problem) {
	if action == "create" && result["outcome"] == "exact_match" {
		return Projection{}, problem(
			"CONTACT_EXACT_MATCH_REQUIRES_LINK", 409,
			"An exact email or profile already belongs to a contact. Link that contact explicitly.",
		)
	}
	body := map[string]any{"contract_version": ContractVersion}
	for key, value := range result {
		body[key] = value
	}
	status := 200
	if action == "create" && result["outcome"] == "created" {
		status = 201
	}
	return Projection{Status: status, Body: body}, nil
}
