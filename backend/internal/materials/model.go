// Package materials owns the existing typed application-only drafts. It does
// not generate prose, alter evidence, publish files or decide submission status.
package materials

import (
	"encoding/json"
	"fmt"
	"io"
)

const (
	MaxInputBytes    = 2 << 20
	MaxResponseBytes = 8 << 20
	ResumeTemplate   = "canonical_ats_resume_v1"
	CoverTemplate    = "professional_cover_letter_v1"
)

type Error struct{ Stage, Code, SourceClass string }

func (*Error) Error() string                { return "Application material operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func failure(stage, code string) *Error     { return &Error{stage, code, "ApplicationMaterialError"} }
func invalid() *Error                       { return failure("validation", "MATERIAL_STRUCTURE_INVALID") }
func JSON(v any) (json.RawMessage, *Error) {
	raw, e := json.Marshal(v)
	if e != nil || len(raw) > MaxResponseBytes {
		return nil, failure("response", "MATERIAL_RESPONSE_LIMIT")
	}
	return raw, nil
}

type Candidate struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Location string `json:"location_line"`
	Phone    string `json:"phone"`
	LinkedIn string `json:"linkedin"`
	GitHub   string `json:"github"`
	Website  string `json:"website"`
}

func (c Candidate) Links() []string {
	out := []string{}
	for _, s := range []string{c.LinkedIn, c.GitHub, c.Website} {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

type CoverEvidence struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Claim        string   `json:"claim"`
	EvidenceRefs []string `json:"evidence_refs"`
	ManualChecks []string `json:"manual_checks"`
	Origin       string   `json:"origin"`
}
type CoverContent struct {
	Date           string          `json:"letter_date"`
	Salutation     string          `json:"salutation"`
	Opening        string          `json:"opening"`
	EvidenceLeadIn string          `json:"evidence_lead_in"`
	EvidenceItems  []CoverEvidence `json:"evidence_items"`
	Closing        string          `json:"closing"`
	SignOff        string          `json:"sign_off"`
	SignatureName  string          `json:"signature_name"`
	CredentialLine string          `json:"credential_line"`
}
type CoverDraft struct {
	SchemaVersion    int          `json:"schema_version"`
	ID               string       `json:"id"`
	Kind             string       `json:"kind"`
	TemplateID       string       `json:"template_id"`
	JobID            string       `json:"job_id"`
	JobFingerprint   string       `json:"job_fingerprint"`
	ProfileRevision  string       `json:"profile_revision"`
	EvidenceRevision string       `json:"evidence_revision"`
	Status           string       `json:"status"`
	Company          string       `json:"company"`
	RoleTitle        string       `json:"role_title"`
	Candidate        Candidate    `json:"candidate"`
	Content          CoverContent `json:"content"`
}

func (CoverDraft) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted cover-letter draft]")
}

type ResumeBullet struct {
	ID           string   `json:"id"`
	Text         string   `json:"text"`
	EvidenceRefs []string `json:"evidence_refs"`
	Origin       string   `json:"origin"`
}
type ResumeExperience struct {
	BlockID  string         `json:"block_id"`
	Company  string         `json:"company"`
	Role     string         `json:"role"`
	Dates    string         `json:"dates"`
	Location string         `json:"location"`
	Bullets  []ResumeBullet `json:"bullets"`
}
type ResumeProject struct {
	BlockID    string         `json:"block_id"`
	Name       string         `json:"name"`
	Descriptor string         `json:"descriptor"`
	Bullets    []ResumeBullet `json:"bullets"`
}
type ResumeSkill struct {
	BlockID  string   `json:"block_id"`
	Label    string   `json:"label"`
	Keywords []string `json:"keywords"`
}
type ResumeEducation struct {
	BlockID    string   `json:"block_id"`
	RecordType string   `json:"record_type"`
	Heading    string   `json:"heading"`
	Subheading string   `json:"subheading"`
	Location   string   `json:"location"`
	DateText   string   `json:"date_text"`
	Details    []string `json:"details"`
}
type ResumeDraft struct {
	SchemaVersion       int                `json:"schema_version"`
	ID                  string             `json:"id"`
	Kind                string             `json:"kind"`
	TemplateID          string             `json:"template_id"`
	JobID               string             `json:"job_id"`
	JobFingerprint      string             `json:"job_fingerprint"`
	ProfileRevision     string             `json:"profile_revision"`
	EvidenceRevision    string             `json:"evidence_revision"`
	PlanRevision        int64              `json:"plan_revision"`
	PlanFingerprint     string             `json:"plan_fingerprint"`
	Status              string             `json:"status"`
	Company             string             `json:"company"`
	RoleTitle           string             `json:"role_title"`
	TargetPages         int                `json:"target_pages"`
	Candidate           Candidate          `json:"candidate"`
	Sections            []string           `json:"sections"`
	Summary             string             `json:"summary"`
	SummaryEvidenceRefs []string           `json:"summary_evidence_refs"`
	Experiences         []ResumeExperience `json:"experiences"`
	Projects            []ResumeProject    `json:"projects"`
	Skills              []ResumeSkill      `json:"skills"`
	Education           []ResumeEducation  `json:"education"`
}

func (ResumeDraft) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted resume draft]") }

type CoverStore struct {
	SchemaVersion int          `json:"schema_version"`
	JobID         string       `json:"job_id"`
	Revision      int64        `json:"revision"`
	UpdatedAt     string       `json:"updated_at"`
	Materials     []CoverDraft `json:"materials"`
}
type ResumeStore struct {
	SchemaVersion int         `json:"schema_version"`
	JobID         string      `json:"job_id"`
	Revision      int64       `json:"revision"`
	UpdatedAt     string      `json:"updated_at"`
	Resume        ResumeDraft `json:"resume"`
}
type Issue struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Path           string `json:"path"`
	BlockingSave   bool   `json:"blocking_save"`
	BlockingExport bool   `json:"blocking_export"`
}
type Validation struct {
	Ready               bool    `json:"ready_to_export"`
	IssueCount          int     `json:"issue_count"`
	BlockingSaveCount   int     `json:"blocking_save_count"`
	BlockingExportCount int     `json:"blocking_export_count"`
	Issues              []Issue `json:"issues"`
}
