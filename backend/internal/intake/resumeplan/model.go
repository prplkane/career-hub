// Package resumeplan owns evidence selection, never resume prose or rendering.
package resumeplan

import (
	"encoding/json"
	"fmt"
	"io"
)

const MaxPlanBytes = 2 << 20
const MaxResponseBytes = 8 << 20
const MaxItems = 10000

type Error struct{ Stage, Code, SourceClass string }

func (*Error) Error() string                { return "Resume Plan operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func failure(stage, code string) *Error {
	return &Error{Stage: stage, Code: code, SourceClass: "ResumePlanError"}
}
func invalid() *Error { return failure("validation", "RESUME_PLAN_STRUCTURE_INVALID") }

var sectionKeys = [6]string{"professional_summary", "technical_skills", "professional_experience", "projects", "education", "certifications"}

type Section struct {
	Key      string `json:"key"`
	Included bool   `json:"included"`
}
type Experience struct {
	BlockID       string   `json:"block_id"`
	Decision      string   `json:"decision"`
	Presentation  *string  `json:"presentation"`
	TargetBullets *int     `json:"target_bullets"`
	ProofIDs      []string `json:"proof_ids"`
	Reason        string   `json:"reason"`
}
type Project struct {
	BlockID  string   `json:"block_id"`
	ProofIDs []string `json:"proof_ids"`
	Reason   string   `json:"reason"`
}
type Skill struct {
	BlockID  string   `json:"block_id"`
	Keywords []string `json:"keywords"`
}
type Education struct {
	BlockID    string `json:"block_id"`
	RecordType string `json:"record_type"`
}
type Plan struct {
	SchemaVersion  int          `json:"schema_version"`
	JobID          string       `json:"job_id"`
	Status         string       `json:"status"`
	VaultRevision  string       `json:"vault_revision"`
	JobFingerprint string       `json:"job_fingerprint"`
	TargetPages    int          `json:"target_pages"`
	Sections       []Section    `json:"sections"`
	Experiences    []Experience `json:"experiences"`
	Projects       []Project    `json:"projects"`
	Skills         []Skill      `json:"skills"`
	Education      []Education  `json:"education"`
}

func (Plan) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted resume plan]") }
func JSON(value any) (json.RawMessage, *Error) {
	raw, e := json.Marshal(value)
	if e != nil || len(raw) > MaxResponseBytes {
		return nil, failure("response", "RESUME_PLAN_RESPONSE_LIMIT")
	}
	return raw, nil
}

type Proof struct {
	ID               string    `json:"id"`
	Claim            string    `json:"claim"`
	MatchedJobSkills *[]string `json:"matched_job_skills,omitempty"`
}
type ExperienceSource struct {
	ID           string  `json:"id"`
	Company      any     `json:"company"`
	Role         any     `json:"role"`
	Dates        any     `json:"dates"`
	Location     any     `json:"location"`
	Category     any     `json:"category"`
	Technologies any     `json:"technologies"`
	Proofs       []Proof `json:"proofs"`
}
type ProjectSource struct {
	ID           string  `json:"id"`
	Name         any     `json:"name"`
	Category     any     `json:"category"`
	Summary      any     `json:"summary"`
	Technologies any     `json:"technologies"`
	Proofs       []Proof `json:"proofs"`
}
type SkillSource struct {
	ID       string   `json:"id"`
	Family   any      `json:"skill_family"`
	Keywords []string `json:"keywords"`
}
type EducationSource struct {
	ID         string `json:"id"`
	RecordType string `json:"record_type"`
	Name       any    `json:"name"`
	Detail     string `json:"detail"`
}
type Catalog struct {
	VaultRevision string             `json:"vault_revision"`
	Experiences   []ExperienceSource `json:"experiences"`
	Projects      []ProjectSource    `json:"projects"`
	Skills        []SkillSource      `json:"skills"`
	Education     []EducationSource  `json:"education"`
}

func (Catalog) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted resume plan catalog]")
}

func Default(jobID, fingerprint string, c Catalog) Plan {
	p := Plan{SchemaVersion: 1, JobID: jobID, Status: "draft", VaultRevision: c.VaultRevision,
		JobFingerprint: fingerprint, TargetPages: 1, Sections: []Section{}, Experiences: []Experience{},
		Projects: []Project{}, Skills: []Skill{}, Education: []Education{}}
	for _, key := range sectionKeys {
		p.Sections = append(p.Sections, Section{key, key != "projects" && key != "certifications"})
	}
	for _, item := range c.Experiences {
		p.Experiences = append(p.Experiences, Experience{BlockID: item.ID, Decision: "exclude", ProofIDs: []string{}})
	}
	for _, item := range c.Education {
		if item.RecordType == "school" {
			p.Education = append(p.Education, Education{item.ID, "school"})
		}
	}
	return p
}
