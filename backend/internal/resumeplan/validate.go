package resumeplan

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type Issue struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	Path         string `json:"path"`
	BlockingSave bool   `json:"blocking_save"`
}
type Validation struct {
	Ready         bool    `json:"ready_for_approval"`
	IssueCount    int     `json:"issue_count"`
	BlockingCount int     `json:"blocking_issue_count"`
	Issues        []Issue `json:"issues"`
}

func unique(items []string) map[string]bool {
	s := map[string]bool{}
	for _, x := range items {
		s[x] = true
	}
	return s
}
func missing(items []string, allowed map[string]bool) []string {
	out := []string{}
	for x := range unique(items) {
		if !allowed[x] {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
func proofSet(proofs []Proof) map[string]bool {
	s := map[string]bool{}
	for _, p := range proofs {
		s[p.ID] = true
	}
	return s
}

// Validate takes a normalized Plan and a freshly validated Catalog. It never
// promotes a skill, chooses proof, writes a record or repairs stale references.
func Validate(ctx context.Context, p Plan, c Catalog, fingerprint string) (*Validation, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("context", "RESUME_PLAN_CANCELED")
	}
	if len(p.Experiences)+len(p.Projects)+len(p.Skills)+len(p.Education) > 4*MaxItems {
		return nil, failure("input", "RESUME_PLAN_INPUT_LIMIT")
	}
	// The prototype's loose YAML required-field checks can admit non-text
	// display names. Do not emit Go-specific fmt representations as if they
	// were equivalent Python messages or meaningful candidate evidence.
	for _, item := range c.Experiences {
		if _, ok := item.Company.(string); !ok {
			return nil, sourceError()
		}
		if _, ok := item.Role.(string); !ok {
			return nil, sourceError()
		}
	}
	for _, item := range c.Projects {
		if _, ok := item.Name.(string); !ok {
			return nil, sourceError()
		}
	}
	for _, item := range c.Skills {
		if _, ok := item.Family.(string); !ok {
			return nil, sourceError()
		}
	}
	v := &Validation{Issues: []Issue{}}
	limited := false
	add := func(code, message, path string, blocking bool) {
		if len(v.Issues) >= 1000 {
			limited = true
			return
		}
		v.Issues = append(v.Issues, Issue{code, message, path, blocking})
		if blocking {
			v.BlockingCount++
		}
	}
	if p.VaultRevision != c.VaultRevision {
		add("PLAN_VAULT_STALE", "Career Vault changed after this plan was loaded. Review current records before saving.", "vault_revision", true)
	}
	if p.JobFingerprint != fingerprint {
		add("PLAN_JOB_STALE", "The job description changed after this plan was loaded. Review the plan before saving.", "job_fingerprint", true)
	}
	sections := map[string]bool{}
	for _, s := range p.Sections {
		sections[s.Key] = s.Included
	}
	experiences := map[string]ExperienceSource{}
	for _, s := range c.Experiences {
		experiences[s.ID] = s
	}
	seen := map[string]bool{}
	included := 0
	for i, item := range p.Experiences {
		if ctx.Err() != nil {
			return nil, failure("context", "RESUME_PLAN_CANCELED")
		}
		if item.Decision == "include" {
			included++
		}
		path := fmt.Sprintf("experiences[%d]", i)
		if seen[item.BlockID] {
			add("PLAN_EXPERIENCE_DUPLICATE", "Experience is listed more than once: "+item.BlockID, path, true)
			continue
		}
		seen[item.BlockID] = true
		source, ok := experiences[item.BlockID]
		if !ok {
			add("PLAN_EXPERIENCE_UNKNOWN", "Career Vault experience no longer exists or is archived: "+item.BlockID, path, true)
			continue
		}
		if len(item.ProofIDs) != len(unique(item.ProofIDs)) {
			add("PLAN_PROOF_DUPLICATE", "Selected experience proof IDs must be unique.", path+".proof_ids", true)
		}
		if unknown := missing(item.ProofIDs, proofSet(source.Proofs)); len(unknown) > 0 {
			add("PLAN_PROOF_UNKNOWN", "Proof does not belong to this experience: "+strings.Join(unknown, ", "), path+".proof_ids", true)
		}
		if item.Decision == "exclude" {
			if item.Presentation != nil || item.TargetBullets != nil || len(item.ProofIDs) > 0 {
				add("PLAN_EXCLUSION_HAS_CONTENT", "Excluded experience cannot retain presentation or proof selections.", path, true)
			}
		} else if item.TargetBullets == nil {
			add("PLAN_BULLET_TARGET_REQUIRED", fmt.Sprintf("Choose a bullet target for %v / %v.", source.Company, source.Role), path, false)
		} else if len(item.ProofIDs) < *item.TargetBullets {
			add("PLAN_PROOFS_INCOMPLETE", fmt.Sprintf("Select at least %d proof points for %v / %v.", *item.TargetBullets, source.Company, source.Role), path+".proof_ids", false)
		}
	}
	ids := []string{}
	for id := range experiences {
		ids = append(ids, id)
	}
	if absent := missing(ids, seen); len(absent) > 0 {
		add("PLAN_EXPERIENCE_MISSING", "Every active experience needs a decision: "+strings.Join(absent, ", "), "experiences", false)
	}
	if included == 0 {
		add("PLAN_EXPERIENCE_REQUIRED", "Include at least one professional experience.", "experiences", false)
	}
	if included > 0 && !sections["professional_experience"] {
		add("PLAN_EXPERIENCE_SECTION_DISABLED", "Professional Experience must be enabled when experience is included.", "sections", false)
	}
	projects := map[string]ProjectSource{}
	for _, s := range c.Projects {
		projects[s.ID] = s
	}
	seen = map[string]bool{}
	for i, item := range p.Projects {
		path := fmt.Sprintf("projects[%d]", i)
		if seen[item.BlockID] {
			add("PLAN_PROJECT_DUPLICATE", "Project is selected more than once: "+item.BlockID, path, true)
			continue
		}
		seen[item.BlockID] = true
		source, ok := projects[item.BlockID]
		if !ok {
			add("PLAN_PROJECT_UNKNOWN", "Career Vault project no longer exists or is archived: "+item.BlockID, path, true)
			continue
		}
		if len(item.ProofIDs) != len(unique(item.ProofIDs)) {
			add("PLAN_PROJECT_PROOF_DUPLICATE", "Selected project proof IDs must be unique.", path+".proof_ids", true)
		}
		if unknown := missing(item.ProofIDs, proofSet(source.Proofs)); len(unknown) > 0 {
			add("PLAN_PROJECT_PROOF_UNKNOWN", "Project proof no longer exists: "+strings.Join(unknown, ", "), path+".proof_ids", true)
		}
		if len(item.ProofIDs) == 0 {
			add("PLAN_PROJECT_PROOF_REQUIRED", fmt.Sprintf("Select at least one proof point for %v.", source.Name), path+".proof_ids", false)
		}
	}
	if len(p.Projects) > 0 && !sections["projects"] {
		add("PLAN_PROJECT_SECTION_DISABLED", "Projects must be enabled when a project is selected.", "sections", false)
	}
	skills := map[string]SkillSource{}
	for _, s := range c.Skills {
		skills[s.ID] = s
	}
	seen = map[string]bool{}
	for i, item := range p.Skills {
		path := fmt.Sprintf("skills[%d]", i)
		if seen[item.BlockID] {
			add("PLAN_SKILL_DUPLICATE", "Skill group is selected more than once: "+item.BlockID, path, true)
			continue
		}
		seen[item.BlockID] = true
		source, ok := skills[item.BlockID]
		if !ok {
			add("PLAN_SKILL_UNKNOWN", "Career Vault skill group no longer exists or is archived: "+item.BlockID, path, true)
			continue
		}
		if unknown := missing(item.Keywords, unique(source.Keywords)); len(unknown) > 0 {
			add("PLAN_KEYWORD_UNKNOWN", "Skill keyword no longer exists in this group: "+strings.Join(unknown, ", "), path+".keywords", true)
		}
		if len(item.Keywords) == 0 {
			add("PLAN_KEYWORD_REQUIRED", fmt.Sprintf("Select at least one keyword from %v.", source.Family), path+".keywords", false)
		}
	}
	if sections["technical_skills"] && len(p.Skills) == 0 {
		add("PLAN_SKILLS_REQUIRED", "Select at least one skill group or disable Technical Skills.", "skills", false)
	}
	if len(p.Skills) > 0 && !sections["technical_skills"] {
		add("PLAN_SKILL_SECTION_DISABLED", "Technical Skills must be enabled when skills are selected.", "sections", false)
	}
	type key struct{ kind, id string }
	education := map[key]bool{}
	selected := map[key]bool{}
	for _, s := range c.Education {
		education[key{s.RecordType, s.ID}] = true
	}
	school, certificate := false, false
	for i, item := range p.Education {
		path := fmt.Sprintf("education[%d]", i)
		k := key{item.RecordType, item.BlockID}
		school = school || item.RecordType == "school"
		certificate = certificate || item.RecordType == "certification"
		if selected[k] {
			add("PLAN_EDUCATION_DUPLICATE", "Education record is selected more than once: "+item.BlockID, path, true)
			continue
		}
		selected[k] = true
		if !education[k] {
			add("PLAN_EDUCATION_UNKNOWN", "Career Vault education record no longer exists or is archived: "+item.BlockID, path, true)
		}
	}
	if school && !sections["education"] {
		add("PLAN_EDUCATION_SECTION_DISABLED", "Education must be enabled when a school is selected.", "sections", false)
	}
	if certificate && !sections["certifications"] {
		add("PLAN_CERTIFICATION_SECTION_DISABLED", "Certifications must be enabled when a certification is selected.", "sections", false)
	}
	if ctx.Err() != nil {
		return nil, failure("context", "RESUME_PLAN_CANCELED")
	}
	if limited {
		return nil, failure("validation", "RESUME_PLAN_ISSUE_LIMIT")
	}
	v.IssueCount = len(v.Issues)
	v.Ready = v.IssueCount == 0
	if _, e := JSON(v); e != nil {
		return nil, e
	}
	return v, nil
}

type Summary struct {
	Status                  string `json:"status"`
	TargetPages             int    `json:"target_pages"`
	IncludedExperienceCount int    `json:"included_experience_count"`
	ProjectCount            int    `json:"selected_project_count"`
	SkillCount              int    `json:"selected_skill_group_count"`
	EducationCount          int    `json:"selected_education_count"`
	Ready                   bool   `json:"ready_for_approval"`
	IssueCount              int    `json:"issue_count"`
}
type Editor struct {
	Plan        Plan       `json:"plan"`
	Catalog     Catalog    `json:"catalog"`
	Revision    int64      `json:"revision"`
	Persisted   bool       `json:"persisted"`
	Fingerprint string     `json:"current_job_fingerprint"`
	Stale       bool       `json:"sources_stale"`
	Validation  Validation `json:"validation"`
	Summary     Summary    `json:"summary"`
}

func NewEditor(p Plan, c Catalog, fingerprint string, revision int64, v Validation) Editor {
	count := 0
	for _, item := range p.Experiences {
		if item.Decision == "include" {
			count++
		}
	}
	return Editor{p, c, revision, revision > 0, fingerprint, p.VaultRevision != c.VaultRevision || p.JobFingerprint != fingerprint, v,
		Summary{p.Status, p.TargetPages, count, len(p.Projects), len(p.Skills), len(p.Education), v.Ready, v.IssueCount}}
}
