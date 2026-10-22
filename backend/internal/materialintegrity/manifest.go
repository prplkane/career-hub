package materialintegrity

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ManifestVersion  = 1
	ValidatorVersion = "material-integrity-v1"
	MaxManifestBytes = 256 << 10
	MaxArtifactBytes = 2500000
)

// Sources records identities only. The owning service must derive these from
// reviewed sources and recheck them at publication/read time, not trust a client.
type Sources struct {
	JobID            string   `json:"job_id"`
	Kind             string   `json:"material_kind"`
	MaterialID       string   `json:"material_id"`
	DraftRevision    int64    `json:"draft_revision"`
	DraftSHA256      string   `json:"draft_sha256"`
	JobFingerprint   string   `json:"job_fingerprint"`
	ProfileRevision  string   `json:"profile_revision"`
	EvidenceRevision string   `json:"evidence_revision"`
	PlanRevision     *int64   `json:"plan_revision"`
	PlanFingerprint  string   `json:"plan_fingerprint"`
	TemplateID       string   `json:"template_id"`
	ProofIDs         []string `json:"proof_ids"`
	ManualCheckIDs   []string `json:"manual_check_ids"`
	PolicySHA256     string   `json:"policy_sha256"`
}

// Finding is the prose-free durable form. Full local review UI can resolve its
// exact draft path and byte span; the manifest never copies a claim or proof.
type Finding struct {
	Severity      string `json:"severity"`
	Code          string `json:"code"`
	DraftPath     string `json:"draft_path"`
	DraftStart    int    `json:"draft_start"`
	DraftEnd      int    `json:"draft_end"`
	ArtifactStart int    `json:"artifact_start"`
	ArtifactEnd   int    `json:"artifact_end"`
	TokenSHA256   string `json:"token_sha256"`
	ManualCheckID string `json:"manual_check_id"`
}

type Validation struct {
	Status   string    `json:"status"`
	Findings []Finding `json:"findings"`
}

type FormatMetadata struct {
	TargetPages          int    `json:"target_pages"`
	PageCount            *int   `json:"page_count"`
	PageCountUnavailable string `json:"page_count_unavailable"`
	PageBudget           string `json:"page_budget"`
}

// Identity is the content-addressed, immutable export identity. The creation
// time is not part of the ID: an exact rerender can reuse the original receipt.
type Identity struct {
	Sources          Sources        `json:"sources"`
	RendererVersion  string         `json:"renderer_version"`
	ValidatorVersion string         `json:"validator_version"`
	Format           string         `json:"format"`
	Filename         string         `json:"filename"`
	SizeBytes        int64          `json:"size_bytes"`
	SHA256           string         `json:"sha256"`
	TextSHA256       string         `json:"text_sha256"`
	Metadata         FormatMetadata `json:"format_metadata"`
	Validation       Validation     `json:"validation"`
}

type Manifest struct {
	SchemaVersion int      `json:"schema_version"`
	ArtifactID    string   `json:"artifact_id"`
	CreatedAt     string   `json:"created_at"`
	Identity      Identity `json:"identity"`
}

var versionName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)
var draftPath = regexp.MustCompile(`^(artifact|summary|candidate\.(name|email|location_line|phone|linkedin|github|website)|` +
	`content\.(letter_date|salutation|opening|evidence_lead_in|closing|sign_off|signature_name|credential_line)|` +
	`content\.evidence_items\[[0-9]{1,4}\]\.(label|claim)|(?:experiences|projects)\[[0-9]{1,4}\]\.bullets\[[0-9]{1,4}\]\.text|` +
	`experiences\[[0-9]{1,4}\]\.(company|role|dates|location)|projects\[[0-9]{1,4}\]\.(name|descriptor)|` +
	`skills\[[0-9]{1,4}\]\.(label|keywords\[[0-9]{1,4}\])|education\[[0-9]{1,4}\]\.(heading|subheading|location|date_text|details\[[0-9]{1,4}\]))$`)

func hash(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, c := range value[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func identifier(value string) bool {
	if len(value) == 0 || len(value) > 800 || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 200 || strings.TrimSpace(value) != value {
		return false
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func validIDs(ids []string) bool {
	if ids == nil || len(ids) > 10000 || !slices.IsSorted(ids) {
		return false
	}
	for i, id := range ids {
		if !identifier(id) || i > 0 && ids[i-1] == id {
			return false
		}
	}
	return true
}

func validSources(s Sources) bool {
	if !identifier(s.JobID) || strings.ContainsAny(s.JobID, `/\:`) || s.JobID == "." || s.JobID == ".." ||
		!identifier(s.MaterialID) || s.DraftRevision < 1 || !hash(s.DraftSHA256, "") || !hash(s.JobFingerprint, "") ||
		!hash(s.ProfileRevision, "sha256:") || !hash(s.EvidenceRevision, "sha256:") || !hash(s.PolicySHA256, "") || !validIDs(s.ProofIDs) || !validIDs(s.ManualCheckIDs) {
		return false
	}
	if s.Kind == "resume" {
		return s.TemplateID == "canonical_ats_resume_v1" && s.MaterialID == "resume_primary" && s.PlanRevision != nil && *s.PlanRevision >= 1 && hash(s.PlanFingerprint, "sha256:")
	}
	return s.Kind == "cover_letter" && s.TemplateID == "professional_cover_letter_v1" && s.PlanRevision == nil && s.PlanFingerprint == ""
}

func span(start, end int) bool {
	return start == -1 && end == -1 || start >= 0 && start <= end && end <= MaxTextBytes
}

func validValidation(v Validation, checks []string) bool {
	if v.Findings == nil || len(v.Findings) > 1000 {
		return false
	}
	status := "pass"
	seen := map[Finding]bool{}
	usedChecks := map[string]bool{}
	for _, f := range v.Findings {
		if !slices.Contains([]string{"pass", "warning", "block"}, f.Severity) ||
			!slices.Contains([]string{"FINAL_TEXT_MISMATCH", "UNSUPPORTED_FACT_TOKEN", "MANUAL_FACT_CHECK", "UNRESOLVED_PLACEHOLDER", "PAGE_BUDGET_EXCEEDED", "PAGINATION_UNAVAILABLE", "TYPOGRAPHY_WARNING", "HARD_TYPOGRAPHY_VIOLATION", "HARD_PAGE_LIMIT"}, f.Code) ||
			!draftPath.MatchString(f.DraftPath) || !span(f.DraftStart, f.DraftEnd) || !span(f.ArtifactStart, f.ArtifactEnd) ||
			f.TokenSHA256 != "" && !hash(f.TokenSHA256, "") || f.ManualCheckID != "" && !slices.Contains(checks, f.ManualCheckID) || seen[f] {
			return false
		}
		seen[f] = true
		if f.Code == "MANUAL_FACT_CHECK" && (f.ManualCheckID == "" || f.TokenSHA256 == "" || f.Severity != "pass") {
			return false
		}
		if slices.Contains([]string{"UNSUPPORTED_FACT_TOKEN", "MANUAL_FACT_CHECK", "UNRESOLVED_PLACEHOLDER"}, f.Code) &&
			(f.DraftPath == "artifact" || f.DraftStart < 0 || f.DraftEnd <= f.DraftStart || !hash(f.TokenSHA256, "") || f.TokenSHA256 == digest(nil)) {
			return false
		}
		if f.Code == "FINAL_TEXT_MISMATCH" && (f.DraftStart < 0 || f.ArtifactStart < 0) {
			return false
		}
		if f.ManualCheckID != "" {
			if f.Code != "MANUAL_FACT_CHECK" {
				return false
			}
			usedChecks[f.ManualCheckID] = true
		}
		if slices.Contains([]string{"FINAL_TEXT_MISMATCH", "UNSUPPORTED_FACT_TOKEN", "UNRESOLVED_PLACEHOLDER", "HARD_TYPOGRAPHY_VIOLATION", "HARD_PAGE_LIMIT"}, f.Code) && f.Severity != "block" {
			return false
		}
		if slices.Contains([]string{"PAGE_BUDGET_EXCEEDED", "PAGINATION_UNAVAILABLE", "TYPOGRAPHY_WARNING"}, f.Code) && f.Severity != "warning" {
			return false
		}
		if slices.Contains([]string{"PAGE_BUDGET_EXCEEDED", "PAGINATION_UNAVAILABLE", "TYPOGRAPHY_WARNING", "HARD_TYPOGRAPHY_VIOLATION", "HARD_PAGE_LIMIT"}, f.Code) && f.DraftPath != "artifact" {
			return false
		}
		if f.Severity == "block" || f.Severity == "warning" && status != "block" {
			status = f.Severity
		}
	}
	return v.Status == status && len(usedChecks) == len(checks)
}

func validIdentity(id Identity) bool {
	if !validSources(id.Sources) || !versionName.MatchString(id.RendererVersion) || id.ValidatorVersion != ValidatorVersion ||
		!slices.Contains([]string{"docx", "pdf", "txt"}, id.Format) || id.Filename != id.Sources.Kind+"."+id.Format ||
		id.SizeBytes < 1 || id.SizeBytes > MaxArtifactBytes || !hash(id.SHA256, "") || !hash(id.TextSHA256, "") ||
		!validValidation(id.Validation, id.Sources.ManualCheckIDs) {
		return false
	}
	m := id.Metadata
	if m.TargetPages < 1 || m.TargetPages > 2 || id.Sources.Kind == "cover_letter" && m.TargetPages != 1 {
		return false
	}
	if !slices.Contains([]string{"pass", "warning", "unavailable"}, m.PageBudget) {
		return false
	}
	hasCode := func(code string) bool {
		for _, f := range id.Validation.Findings {
			if f.Code == code {
				return true
			}
		}
		return false
	}
	if m.PageCount != nil {
		if id.Format == "txt" || *m.PageCount < 1 || *m.PageCount > 100 || m.PageCountUnavailable != "" {
			return false
		}
		if *m.PageCount > m.TargetPages {
			if m.PageBudget != "warning" || !hasCode("PAGE_BUDGET_EXCEEDED") {
				return false
			}
		} else if m.PageBudget != "pass" {
			return false
		}
		return id.Sources.Kind != "resume" || *m.PageCount <= 2 || hasCode("HARD_PAGE_LIMIT")
	}
	return m.PageBudget == "unavailable" && (id.Format == "txt" && m.PageCountUnavailable == "not_paginated" || id.Format == "docx" && m.PageCountUnavailable == "layout_engine_unavailable" && hasCode("PAGINATION_UNAVAILABLE"))
}

func artifactID(id Identity) string {
	// Include the schema domain so a later manifest version cannot accidentally
	// reuse the v1 artifact identity with different semantics.
	raw, _ := json.Marshal(struct {
		Version  int      `json:"schema_version"`
		Identity Identity `json:"identity"`
	}{ManifestVersion, id}) // bounded non-floating wire values
	return "material_" + digest(raw)
}

// NewManifest validates a lineage record supplied by the owning validation
// pipeline. It is not permission to export: a publication owner must still run
// the fact/format validators, verify the bytes, and refuse block status.
func NewManifest(id Identity, created time.Time) (*Manifest, *Error) {
	if !validIdentity(id) || created.Year() < 1 || created.Year() > 9999 {
		return nil, failure("MATERIAL_MANIFEST_INVALID")
	}
	m := &Manifest{SchemaVersion: ManifestVersion, ArtifactID: artifactID(id), CreatedAt: created.UTC().Format(time.RFC3339Nano), Identity: id}
	raw, e := Encode(m)
	if e != nil {
		return nil, e
	}
	// Return an owned snapshot; later caller mutations cannot change identity.
	return Decode(raw)
}

func validManifest(m *Manifest) bool {
	if m == nil || m.SchemaVersion != ManifestVersion || !validIdentity(m.Identity) || m.ArtifactID != artifactID(m.Identity) {
		return false
	}
	t, e := time.Parse(time.RFC3339Nano, m.CreatedAt)
	return e == nil && t.Year() >= 1 && t.UTC().Format(time.RFC3339Nano) == m.CreatedAt
}

func Encode(m *Manifest) ([]byte, *Error) {
	if !validManifest(m) {
		return nil, failure("MATERIAL_MANIFEST_INVALID")
	}
	raw, e := json.Marshal(m)
	if e != nil || len(raw) >= MaxManifestBytes {
		return nil, failure("MATERIAL_MANIFEST_LIMIT")
	}
	return append(raw, '\n'), nil
}

// Decode accepts only canonical v1 bytes emitted by Encode. Exact round-trip
// rejects duplicate/unknown/missing keys, null collections, surrogate repair,
// case-insensitive field aliases and trailing data without permissive fallback.
func Decode(raw []byte) (*Manifest, *Error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes || !utf8.Valid(raw) {
		return nil, failure("MATERIAL_MANIFEST_INVALID")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var m Manifest
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF || !validManifest(&m) {
		return nil, failure("MATERIAL_MANIFEST_INVALID")
	}
	canonical, e := Encode(&m)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(canonical, raw) {
		return nil, failure("MATERIAL_MANIFEST_NONCANONICAL")
	}
	return &m, nil
}

// Verify binds an already published manifest to a separately retained receipt,
// freshly derived source snapshot, and actual output bytes. SHA-256 is corruption
// detection, not authentication against an owner able to rewrite both records.
func Verify(raw []byte, receiptSHA256 string, current Sources, artifact []byte) (*Manifest, *Error) {
	if len(raw) > MaxManifestBytes || !hash(receiptSHA256, "") || digest(raw) != receiptSHA256 {
		return nil, failure("MATERIAL_MANIFEST_CHANGED")
	}
	m, e := Decode(raw)
	if e != nil {
		return nil, e
	}
	if !validSources(current) || !reflect.DeepEqual(m.Identity.Sources, current) {
		return nil, failure("MATERIAL_ARTIFACT_SOURCES_STALE")
	}
	if len(artifact) > MaxArtifactBytes || int64(len(artifact)) != m.Identity.SizeBytes || digest(artifact) != m.Identity.SHA256 {
		return nil, failure("MATERIAL_ARTIFACT_CHANGED")
	}
	if m.Identity.Validation.Status == "block" {
		return nil, failure("MATERIAL_ARTIFACT_BLOCKED")
	}
	return m, nil
}
