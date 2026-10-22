package discovery

import (
	"slices"
	"strings"
)

type Fields struct {
	Title    string `json:"title"`
	Company  string `json:"company"`
	Location string `json:"location"`
}
type Overrides struct {
	Title     *string `json:"title"`
	Company   *string `json:"company"`
	Location  *string `json:"location"`
	Revision  int64   `json:"revision"`
	UpdatedAt string  `json:"updated_at"`
}
type Decision struct {
	Queue               string  `json:"queue"`
	AssessmentAvailable bool    `json:"assessment_available"`
	Label               string  `json:"label"`
	PrimaryEvidence     string  `json:"primary_evidence"`
	ComputedAt          *string `json:"computed_at,omitempty"`
}
type Verification struct {
	Verified   bool   `json:"verified"`
	VerifiedAt string `json:"verified_at"`
	FromScope  string `json:"from_scope"`
}
type Record struct {
	DiscoveryID string `json:"discovery_id"`
	State       string `json:"state"`
	Fields
	ExtractedFields     Fields       `json:"extracted_fields"`
	FieldOverrides      Overrides    `json:"field_overrides"`
	ObservedURL         string       `json:"observed_url"`
	CanonicalURL        string       `json:"canonical_url"`
	ResolvedURL         string       `json:"resolved_url"`
	PostingIdentity     string       `json:"posting_identity"`
	ContentScope        string       `json:"content_scope"`
	ContentReadiness    string       `json:"content_readiness"`
	NeedsContent        bool         `json:"needs_content"`
	Decision            Decision     `json:"decision"`
	ContentVerification Verification `json:"content_verification"`
	SourceRelativePath  string       `json:"source_relative_path"`
	ContentSHA256       string       `json:"content_sha256"`
	ContentChars        int64        `json:"content_chars"`
	FirstSeenAt         string       `json:"first_seen_at"`
	LastSeenAt          string       `json:"last_seen_at"`
	UpdatedAt           string       `json:"updated_at"`
	PromotedJobID       string       `json:"promoted_job_id"`
	PromotedAt          string       `json:"promoted_at"`
	Revision            int64        `json:"revision"`
	SourceKinds         []string     `json:"source_kinds"`
	SourceProviders     []string     `json:"source_providers"`
	CaptureQuality      *Quality     `json:"capture_quality"`
	CaptureFacts        *Facts       `json:"capture_facts"`
}
type Observation struct {
	ObservationID      int64        `json:"observation_id"`
	DiscoveryID        string       `json:"discovery_id"`
	SourceKind         string       `json:"source_kind"`
	SourceChannel      string       `json:"source_channel"`
	RequestFingerprint string       `json:"request_fingerprint"`
	ObservedURL        string       `json:"observed_url"`
	ObservedAt         string       `json:"observed_at"`
	ContentSHA256      string       `json:"content_sha256"`
	Metadata           JSONDocument `json:"metadata"`
	RecordedAt         string       `json:"recorded_at"`
}
type EmailSource struct {
	SenderAddress string `json:"sender_address"`
	SenderDomain  string `json:"sender_domain"`
	Disposition   string `json:"disposition"`
	Purpose       string `json:"purpose"`
	Configurable  bool   `json:"configurable"`
}
type Detail struct {
	Record
	SourceText   string        `json:"source_text"`
	Observations []Observation `json:"observations"`
	EmailSource  *EmailSource  `json:"email_source"`
}
type Assessment struct {
	DiscoveryID               string       `json:"discovery_id"`
	ContentSHA256             string       `json:"content_sha256"`
	AnalyzerVersion           int64        `json:"analyzer_version"`
	EvaluatorVersion          int64        `json:"evaluator_version"`
	CandidateRevision         string       `json:"candidate_revision"`
	AssessmentContextRevision string       `json:"assessment_context_revision"`
	AssessmentVersion         int64        `json:"assessment_version"`
	Scope                     string       `json:"scope"`
	Assessment                JSONDocument `json:"assessment"`
	ComputedAt                string       `json:"computed_at"`
}

const columns = "discovery_id,state,title,company,location,observed_url,canonical_url,posting_identity,content_scope,source_relative_path,content_sha256,content_chars,first_seen_at,last_seen_at,updated_at,promoted_job_id,promoted_at,revision,content_verified_at,content_verified_from_scope"

func baseRecord(raw []any) (Record, error) {
	r := Record{SourceKinds: []string{}, SourceProviders: []string{}}
	textTargets := map[int]*string{0: &r.DiscoveryID, 1: &r.State, 2: &r.Title, 3: &r.Company, 4: &r.Location, 5: &r.ObservedURL, 6: &r.CanonicalURL, 7: &r.PostingIdentity, 8: &r.ContentScope, 9: &r.SourceRelativePath, 10: &r.ContentSHA256, 12: &r.FirstSeenAt, 13: &r.LastSeenAt, 14: &r.UpdatedAt, 15: &r.PromotedJobID, 16: &r.PromotedAt, 18: &r.ContentVerification.VerifiedAt, 19: &r.ContentVerification.FromScope}
	for i, target := range textTargets {
		v, e := textCell(raw[i], slices.Contains([]int{9, 15, 16, 18, 19}, i))
		if e != nil {
			return Record{}, e
		}
		*target = v
	}
	var e error
	if r.ContentChars, e = integerCell(raw[11]); e != nil {
		return Record{}, e
	}
	if r.Revision, e = integerCell(raw[17]); e != nil {
		return Record{}, e
	}
	switch r.ContentScope {
	case "link_only":
		r.ContentReadiness = "needs_jd"
	case "selection", "page_visible":
		r.ContentReadiness = "needs_review"
	case "confirmed_full":
		r.ContentReadiness = "ready"
	default:
		return Record{}, ErrUnsupported
	}
	r.NeedsContent = r.ContentScope != "confirmed_full"
	r.ContentVerification.Verified = r.ContentVerification.VerifiedAt != ""
	r.ExtractedFields = r.Fields
	return r, nil
}

type assessmentSummary struct{ sha, scope, raw, computed string }

func summarizeDecision(record Record, assessment *assessmentSummary) (Decision, error) {
	d := Decision{Queue: "review", Label: "Review"}
	if record.ContentScope == "link_only" {
		d.Queue, d.Label = "needs_jd", "Needs JD"
		return d, nil
	}
	if record.ContentScope != "confirmed_full" {
		d.Label = "Review capture"
		return d, nil
	}
	if assessment == nil || assessment.sha != record.ContentSHA256 || assessment.scope != record.ContentScope {
		return d, nil
	}
	artifact, e := parseObject(assessment.raw)
	if e != nil {
		return d, failure("assessment", "ASSESSMENT_INVALID")
	}
	receipt, ok := artifact["receipt"].(map[string]any)
	if !ok {
		return d, failure("assessment", "ASSESSMENT_INVALID")
	}
	status, e := metadataText(receipt["status"])
	if e != nil {
		return d, e
	}
	switch status {
	case "clear_conflict":
		d.Queue, d.Label = "clear_conflict", "Clear conflict"
	case "no_clear_conflict":
		d.Queue, d.Label = "review_first", "Review first"
	}
	key := "review_items"
	if d.Queue == "clear_conflict" {
		key = "blockers"
	}
	if evidence, ok := receipt[key].([]any); ok && len(evidence) > 0 {
		if first, ok := evidence[0].(map[string]any); ok {
			message, e := metadataText(first["message"])
			if e != nil {
				return d, e
			}
			if message == "" {
				message, e = metadataText(first["text"])
				if e != nil {
					return d, e
				}
			}
			d.PrimaryEvidence = string([]rune(message)[:min(len([]rune(message)), 500)])
		}
	}
	d.AssessmentAvailable = receipt["availability"] == "available"
	d.ComputedAt = &assessment.computed
	return d, nil
}

func finishRecord(r *Record, assessment *assessmentSummary, stored map[string]any) error {
	var err error
	if slices.Contains(r.SourceKinds, "email_file") {
		provider := ""
		if len(r.SourceProviders) > 0 {
			provider = r.SourceProviders[0]
		}
		projected, e := ProjectEmailFacts(FactInput{Provider: provider, Title: r.Title, Company: r.Company, Location: r.Location})
		if e != nil {
			return e
		}
		facts, e := MergeFacts(stored, projected)
		if e != nil {
			return e
		}
		r.CaptureFacts = &facts
		if facts.Location != "" {
			r.ExtractedFields.Location = facts.Location
		}
	}
	r.Fields = r.ExtractedFields
	for _, pair := range []struct {
		override *string
		field    *string
	}{{r.FieldOverrides.Title, &r.Title}, {r.FieldOverrides.Company, &r.Company}, {r.FieldOverrides.Location, &r.Location}} {
		if pair.override != nil && *pair.override != "" {
			*pair.field = *pair.override
		}
	}
	if r.Decision, err = summarizeDecision(*r, assessment); err != nil {
		return err
	}
	if r.CaptureFacts != nil {
		url := r.CanonicalURL
		if url == "" {
			url = r.ObservedURL
		}
		quality, e := ClassifyEmailQuality(QualityInput{Title: r.Title, Company: r.Company, Location: r.Location, SourceURL: url})
		if e != nil {
			return e
		}
		r.CaptureQuality = &quality
	}
	return nil
}

// These source metadata fields use str(value or ""), not the fact parser's
// whitespace-collapsing helper. Evidence and sender identities retain wording.
func metadataText(value any) (string, error) {
	if raw, ok := value.(string); ok {
		if !validTexts(raw) {
			return "", ErrUnsupported
		}
		return raw, nil
	}
	return storedText(value, MaxCellBytes)
}

// Identifier validation uses the source host's Path.name boundary, not an
// application ID as a filesystem path. Windows drive/UNC separators are also
// refused on every target so cross-platform data cannot acquire new path powers.
func NormalizeID(value string) (string, *Error) {
	cleaned, e := plainText(value, 300)
	if e != nil {
		return "", e
	}
	points := []rune(cleaned)
	if cleaned == "" || cleaned == "." || strings.ContainsAny(cleaned, "/\\") || (len(points) >= 2 && points[1] == ':') {
		return "", failure("request", "INVALID_ID")
	}
	return cleaned, nil
}
