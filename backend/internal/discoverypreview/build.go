package discoverypreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobdigest"
	"careerhub.local/backend/internal/jobevaluation"
)

// digestInput verifies the capture and projects the current eligibility facts.
func digestInput(ctx context.Context, input Input) (jobdigest.Input, error) {
	if err := stopped(ctx); err != nil {
		return jobdigest.Input{}, err
	}
	capture := input.Capture
	identity := capture.Identity
	if err := identity.Validate(); err != nil {
		return jobdigest.Input{}, ErrInput
	}
	if !validText(capture.Source, jobdigest.MaxSourceBytes) || capture.Source == "" || !validText(capture.RelativePath, 16384) {
		return jobdigest.Input{}, ErrInput
	}
	sum := sha256.Sum256([]byte(capture.Source))
	if hex.EncodeToString(sum[:]) != identity.ContentSHA256 {
		return jobdigest.Input{}, ErrSource
	}
	profile, err := jobdigest.ProfileProjection(ctx, input.Evaluation.Profile)
	if err != nil {
		return jobdigest.Input{}, err
	}
	return jobdigest.Input{Source: capture.Source, Company: identity.Company, Title: identity.Title, SourceURL: identity.ObservedURL, Profile: profile, Registry: input.Registry}, nil
}

// Build consumes fresh validated candidate/catalog/policy inputs. It recomputes
// all digest families, verifies source references, validates the full transient
// card against its real discovery capture, evaluates all rules and builds every
// artifact family. No source-v1 cached verdict can substitute for any stage.
func Build(ctx context.Context, input Input) (*Prepared, error) {
	args, err := digestInput(ctx, input)
	if err != nil {
		return nil, err
	}
	digest, err := jobdigest.Analyze(ctx, args)
	if err != nil {
		return nil, err
	}
	return complete(ctx, input, digest)
}

// Restore accepts only a complete, identity/checksum-qualified local cache.
// Full card/reference checks and evaluation/receipt construction run against
// current validated context. Only deterministic extraction is reused.
func Restore(ctx context.Context, input Input, cached *Prepared) (*Prepared, error) {
	args, err := digestInput(ctx, input)
	if err != nil {
		return nil, err
	}
	if cached == nil || cached.Artifact == nil || cached.Digest == nil {
		return nil, ErrInput
	}
	if err := jobdigest.CheckReferences(ctx, args, cached.Digest); err != nil {
		return nil, err
	}
	fresh, err := complete(ctx, input, cached.Digest)
	if err != nil {
		return nil, err
	}
	want, err := json.Marshal(fresh.Artifact)
	if err != nil {
		return nil, ErrInput
	}
	got, err := json.Marshal(cached.Artifact)
	if err != nil || !bytes.Equal(want, got) {
		return nil, ErrInput
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	return fresh, nil
}

func complete(ctx context.Context, input Input, digest *jobdigest.Result) (*Prepared, error) {
	capture := input.Capture
	identity := capture.Identity
	card, err := previewCard(ctx, capture, digest)
	if err != nil {
		return nil, err
	}
	report, cardErr := jobcard.ValidateDiscoveryPreview(ctx, card, input.CheckSource)
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	if cardErr != nil {
		return nil, ErrSource
	}
	if !report.SchemaValid() {
		return nil, ErrInput
	}
	evaluation := input.Evaluation
	evaluation.Job = card
	assessment, evalErr := jobevaluation.Evaluate(ctx, evaluation)
	if evalErr != nil {
		return nil, evalErr
	}
	vocabulary := unknown(digest)
	summary, err := receipt(ctx, assessment, vocabulary, identity.Scope, digest.Instructions.ItemCount, digest.Contacts.CandidateCount)
	if err != nil {
		return nil, err
	}
	if digest.Instructions.SchemaVersion != 1 || digest.Instructions.ExtractorVersion != 1 ||
		digest.Contacts.SchemaVersion != 1 || digest.Contacts.ExtractorVersion != 1 {
		return nil, ErrInput
	}
	artifact := &Artifact{SchemaVersion: Version, AssessmentMode: "advisory_only", ApplicationAllowed: true, Scope: identity.Scope,
		Assessment: assessment, UnknownVocabulary: vocabulary, Receipt: summary,
		Instructions: Instructions{InstructionResult: digest.Instructions, Eligible: true, Message: "Review every exact source-backed step before applying.", Scope: identity.Scope},
		Contacts:     Contacts{ContactResult: digest.Contacts, Eligible: true, Message: "Review exact source evidence before saving a contact later.", Scope: identity.Scope}}
	raw, err := json.Marshal(artifact)
	if err != nil {
		return nil, ErrInput
	}
	if len(raw) > MaxArtifactBytes {
		return nil, ErrLimit
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	return &Prepared{Artifact: artifact, Digest: digest}, nil
}

// This is an in-memory projection only, not a synthetic saved job or file.
// Every data-bearing family is copied from the fresh complete digest.
func previewCard(ctx context.Context, capture Capture, digest *jobdigest.Result) (cataloginput.Value, error) {
	card := map[string]any{
		"id": capture.Identity.DiscoveryID, "status": "draft", "company": "", "title": "",
		"source_url": capture.Identity.ObservedURL, "location": "", "remote_policy": "", "seniority": "",
		"required_skills": digest.RequiredSkills, "required_platforms": digest.RequiredPlatforms, "required_capabilities": []string{},
		"preferred_skills": digest.PreferredSkills, "technology_groups": digest.TechnologyGroups,
		"experience_requirements": digest.Experience, "education_requirements": digest.Education, "responsibilities": digest.Responsibilities,
		"compensation": digest.Compensation, "visa_signals": digest.VisaSignals,
		"raw_jd_path": capture.RelativePath, "notes": []string{"Draft created from the raw job description; manual review required."}, "manual_overrides": []string{},
		"content_fingerprint": digest.ContentFingerprint,
		"digest": map[string]any{"schema_version": 1, "analyzer_version": jobdigest.AnalyzerVersion, "fields": digest.Fields,
			"mentioned_technologies": digest.MentionedTechnologies, "skill_occurrences": digest.Occurrences,
			"eligibility_gates": digest.Gates, "classified_requirements": digest.Classified,
			"application_instructions": digest.Instructions, "contact_candidates": digest.Contacts, "provenance": digest.Provenance,
			"analysis_coverage": digest.Coverage, "unresolved_fields": digest.UnresolvedFields},
	}
	for _, name := range []string{"company", "title", "location", "remote_policy", "seniority"} {
		field, ok := digest.Fields[name]
		if !ok {
			return cataloginput.Value{}, ErrInput
		}
		card[name] = field.Value
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return cataloginput.Value{}, ErrInput
	}
	// The existing pinned JSON-compatible YAML decoder retains numeric types;
	// it imposes its own 2 MiB/structural envelope before full card validation.
	if len(raw) > cataloginput.YAMLMaxInputBytes {
		return cataloginput.Value{}, ErrLimit
	}
	value, parseErr := cataloginput.ParseYAML(raw)
	if parseErr != nil {
		return cataloginput.Value{}, ErrInput
	}
	if err := stopped(ctx); err != nil {
		return cataloginput.Value{}, err
	}
	return value, nil
}
