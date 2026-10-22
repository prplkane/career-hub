package agentcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/evaluationinput"
	"careerhub.local/backend/internal/jobdigest"
	"careerhub.local/backend/internal/textcompat"
)

// SourceRequest is advisory text, not a saved-job selector or permission to
// persist a posting. Formatting must not disclose its source or URL.
type SourceRequest struct{ Text, Company, Title, SourceURL string }

func (SourceRequest) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted advisory job source]")
}

func ValidateSourceRequest(request SourceRequest) *Error {
	if !utf8.ValidString(request.Text) || len(request.Text) > jobdigest.MaxSourceBytes || utf8.RuneCountInString(request.Text) > jobdigest.MaxSourceRunes || textcompat.Strip(request.Text) == "" {
		return failure("SOURCE_INVALID")
	}
	for _, field := range []struct {
		value string
		limit int
	}{{request.Company, 4096}, {request.Title, 4096}, {request.SourceURL, 65536}} {
		if !utf8.ValidString(field.value) || len(field.value) > field.limit {
			return failure("SOURCE_INVALID")
		}
	}
	return nil
}

// ReadSourceEvidence mirrors analyze_source without reading or creating any JD
// file. The supplied owner ports still need final native admission/cleanup.
// Source normalization is jobdigest's, not retained-file intake normalization.
func ReadSourceEvidence(ctx context.Context, data, resources evaluationinput.Source, request SourceRequest, today time.Time) (*SavedEvidence, *Error) {
	if e := contextStopped(ctx); e != nil {
		return nil, e
	}
	if e := ValidateSourceRequest(request); e != nil {
		return nil, e
	}
	if data == nil || resources == nil {
		return nil, failure("SOURCE_REQUIRED")
	}
	remaining := contextJSONLimit
	d := &observedEvidenceSource{ctx: ctx, source: data, files: map[string]evidenceFile{}, remaining: &remaining}
	r := &observedEvidenceSource{ctx: ctx, source: resources, files: map[string]evidenceFile{}, remaining: &remaining}
	result, e := readSourceEvidence(ctx, d, r, request, today)
	if e != nil {
		return nil, e
	}
	if e := d.recheck(); e != nil {
		return nil, e
	}
	if e := r.recheck(); e != nil {
		return nil, e
	}
	return result, nil
}

func readSourceEvidence(ctx context.Context, d, r *observedEvidenceSource, request SourceRequest, today time.Time) (*SavedEvidence, *Error) {
	inputs, ie := evaluationinput.Prepare(ctx, d, r, today)
	if ie != nil {
		return nil, failure(ie.Code)
	}
	// The whole Vault validator already observed this exact eligibility file.
	visa, ve := cataloginput.ParseYAML(d.files["career_vault/visa_profile.yaml"].raw)
	if ve != nil {
		return nil, failure("SOURCE_INVALID")
	}
	profile, pe := jobdigest.ProfileProjection(ctx, visa)
	if pe != nil {
		return nil, failure("SOURCE_INVALID")
	}
	digest, de := jobdigest.Analyze(ctx, jobdigest.Input{Source: request.Text, Company: request.Company, Title: request.Title, SourceURL: request.SourceURL, Registry: inputs.Registry, Profile: profile})
	if de != nil {
		if e := contextStopped(ctx); e != nil {
			return nil, e
		}
		return nil, failure("SOURCE_DIGEST_FAILED")
	}
	card, ce := advisoryCard(ctx, request, digest)
	if ce != nil {
		return nil, ce
	}
	return evaluateEvidence(ctx, d, card, inputs, "ephemeral:stdin")
}

// This is the source adapter's _digest_card shape, not PrepareIntake's card:
// no posting identity, retry key, instruction/contact additions or publication.
// Preserve source insertion order for downstream packet fingerprints.
func advisoryCard(ctx context.Context, request SourceRequest, digest *jobdigest.Result) (packetValue, *Error) {
	if digest == nil || len(digest.ContentFingerprint) < 12 {
		return packetValue{}, failure("SOURCE_DIGEST_INVALID")
	}
	raw, err := json.Marshal(digest)
	if err != nil {
		return packetValue{}, failure("SOURCE_DIGEST_INVALID")
	}
	value, e := decodeOrderedContextJSON(ctx, raw)
	if e != nil {
		return packetValue{}, e
	}
	get := func(key string) packetValue { return packetGet(value, key, packetNull()) }
	fields := get("fields")
	field := func(key string) packetValue {
		return packetGet(packetGet(fields, key, packetObject()), "value", packetString(""))
	}
	card := packetObject(
		packetField("id", packetString("advisory_"+digest.ContentFingerprint[:12])),
		packetField("status", packetString("draft")),
		packetField("company", field("company")), packetField("title", field("title")),
		packetField("source_url", packetString(textcompat.Strip(request.SourceURL))),
		packetField("location", field("location")), packetField("remote_policy", field("remote_policy")), packetField("seniority", field("seniority")),
		packetField("required_skills", get("required_skills")), packetField("required_platforms", get("required_platforms")), packetField("required_capabilities", packetList()),
		packetField("preferred_skills", get("preferred_skills")), packetField("technology_groups", get("technology_groups")),
		packetField("experience_requirements", get("experience_requirements")), packetField("education_requirements", get("education_requirements")), packetField("responsibilities", get("responsibilities")),
		packetField("compensation", get("compensation")), packetField("visa_signals", packetPick(get("visa_signals"), "citizenship_required", "clearance_required", "sponsorship_mentioned", "no_visa_or_opt")),
		packetField("raw_jd_path", packetString("jobs/incoming/advisory.md")),
		packetField("notes", packetList(packetString("Draft created from the raw job description; manual review required."))),
		packetField("manual_overrides", packetList()), packetField("content_fingerprint", get("content_fingerprint")),
	)
	detail := packetObject(packetField("schema_version", packetInt(1)), packetField("analyzer_version", packetInt(jobdigest.AnalyzerVersion)))
	for _, key := range []string{"fields", "mentioned_technologies", "skill_occurrences", "eligibility_gates", "classified_requirements", "provenance", "analysis_coverage", "unresolved_fields"} {
		detail.Entries = append(detail.Entries, packetField(key, get(key)))
	}
	card.Entries = append(card.Entries, packetField("digest", detail))
	return card, nil
}
