package materials

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"careerhub.local/backend/internal/intake/resumeplan"
	"careerhub.local/backend/internal/textcompat"
)

func selectedEvidence(p resumeplan.Plan) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, x := range p.Experiences {
		if x.Decision == "include" {
			out[x.BlockID] = stringSet(x.ProofIDs)
		}
	}
	for _, x := range p.Projects {
		out[x.BlockID] = stringSet(x.ProofIDs)
	}
	return out
}
func allSelected(p resumeplan.Plan) map[string]bool {
	out := map[string]bool{}
	for _, refs := range selectedEvidence(p) {
		for id := range refs {
			out[id] = true
		}
	}
	return out
}

func ValidateResume(ctx context.Context, d ResumeDraft, s Sources) (*Validation, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("context", "MATERIAL_CANCELED")
	}
	base, e := DefaultResume(s)
	if e != nil {
		return nil, e
	}
	plan := s.Plan
	v := newValidation()
	for _, check := range []struct {
		different           bool
		code, message, path string
	}{
		{d.JobFingerprint != base.JobFingerprint, "RESUME_JOB_STALE", "The saved job description changed. Review the Resume Plan before continuing.", "job_fingerprint"},
		{d.ProfileRevision != base.ProfileRevision, "RESUME_PROFILE_STALE", "Career Vault Profile changed. Review the contact block before continuing.", "profile_revision"},
		{d.EvidenceRevision != base.EvidenceRevision, "RESUME_EVIDENCE_STALE", "Career Vault evidence changed. Refresh the Resume Plan before continuing.", "evidence_revision"},
		{d.PlanRevision != base.PlanRevision, "RESUME_PLAN_STALE", "The Resume Plan changed. Rebuild this application resume from the current plan.", "plan_revision"},
		{d.PlanFingerprint != base.PlanFingerprint, "RESUME_PLAN_CONTENT_STALE", "The Resume Plan content changed. Rebuild this application resume from the current plan.", "plan_fingerprint"},
	} {
		if check.different {
			v.add(check.code, check.message, check.path, true, true)
		}
	}
	if !plan.Persisted || plan.Plan.Status != "approved" {
		v.add("RESUME_PLAN_NOT_APPROVED", "Approve the Resume Plan before exporting a tailored resume.", "plan_revision", false, true)
	}
	if !plan.Validation.Ready {
		v.add("RESUME_PLAN_INVALID", "Resolve Resume Plan issues before exporting a tailored resume.", "plan_revision", false, true)
	}
	for _, check := range []struct {
		different bool
		path      string
	}{{d.Company != base.Company, "company"}, {d.RoleTitle != base.RoleTitle, "role_title"}, {d.TargetPages != base.TargetPages, "target_pages"}, {!slices.Equal(d.Sections, base.Sections), "sections"}} {
		if check.different {
			v.add("RESUME_PLAN_STRUCTURE_MISMATCH", "This field is controlled by the approved Resume Plan.", check.path, true, true)
		}
	}
	selectedByBlock, selected := selectedEvidence(plan.Plan), allSelected(plan.Plan)
	evidence, e := EvidenceCatalog(s.Catalog)
	if e != nil {
		return nil, e
	}
	available := map[string]bool{}
	for _, proof := range evidence {
		available[proof.ID] = true
	}
	summaryEnabled := slices.Contains(d.Sections, "professional_summary")
	if summaryEnabled && d.Summary == "" {
		v.add("RESUME_SUMMARY_REQUIRED", "Write a concise, evidence-backed summary or disable the section in Resume Plan.", "summary", false, true)
	}
	if !summaryEnabled && (d.Summary != "" || len(d.SummaryEvidenceRefs) > 0) {
		v.add("RESUME_SUMMARY_DISABLED", "Summary content must be removed when the section is disabled.", "summary", true, true)
	}
	checkPlaceholder := func(text, path string) {
		matched, e := placeholder(text, "resume")
		if e != nil {
			v.err = e
			return
		}
		if matched {
			v.add("RESUME_PLACEHOLDER", "Remove placeholder text before export.", path, false, true)
		}
	}
	if d.Summary != "" {
		valid := map[string]bool{}
		for id := range available {
			if selected[id] {
				valid[id] = true
			}
		}
		if invalid := absent(d.SummaryEvidenceRefs, valid); len(invalid) > 0 {
			v.add("RESUME_SUMMARY_EVIDENCE_INVALID", "Summary evidence must come from proofs selected in Resume Plan: "+strings.Join(invalid, ", "), "summary_evidence_refs", true, true)
		}
		if len(d.SummaryEvidenceRefs) == 0 {
			v.add("RESUME_SUMMARY_UNVERIFIED", "Link the summary to at least one proof selected in Resume Plan.", "summary_evidence_refs", false, true)
		}
		checkPlaceholder(d.Summary, "summary")
	}
	// A shared local projection keeps inventory order significant without tying
	// different record types together or accepting caller-controlled field paths.
	ids := func(records any) []string {
		out := []string{}
		switch a := records.(type) {
		case []ResumeExperience:
			for _, x := range a {
				out = append(out, x.BlockID)
			}
		case []ResumeProject:
			for _, x := range a {
				out = append(out, x.BlockID)
			}
		case []ResumeSkill:
			for _, x := range a {
				out = append(out, x.BlockID)
			}
		case []ResumeEducation:
			for _, x := range a {
				out = append(out, x.BlockID)
			}
		}
		return out
	}
	for _, x := range []struct {
		name             string
		actual, expected any
	}{{"experiences", d.Experiences, base.Experiences}, {"projects", d.Projects, base.Projects}, {"skills", d.Skills, base.Skills}, {"education", d.Education, base.Education}} {
		if !slices.Equal(ids(x.actual), ids(x.expected)) {
			v.add("RESUME_PLAN_INVENTORY_MISMATCH", x.name+" must match the approved Resume Plan.", x.name, true, true)
		}
	}
	checkBullets := func(path, id string, bullets []ResumeBullet, expectedCount int, metadataMatches bool) {
		if !metadataMatches {
			v.add("RESUME_SOURCE_METADATA_MISMATCH", "Source metadata is controlled by Career Vault and Resume Plan.", path, true, true)
		}
		if len(bullets) != expectedCount {
			v.add("RESUME_BULLET_COUNT_MISMATCH", "Bullet count must match the approved Resume Plan.", path+".bullets", true, true)
		}
		for i, b := range bullets {
			bp := fmt.Sprintf("%s.bullets[%d]", path, i)
			if invalid := absent(b.EvidenceRefs, selectedByBlock[id]); len(invalid) > 0 {
				v.add("RESUME_BULLET_EVIDENCE_INVALID", "Bullet evidence must come from this block's selected Resume Plan proofs: "+strings.Join(invalid, ", "), bp+".evidence_refs", true, true)
			}
			checkPlaceholder(b.Text, bp+".text")
		}
	}
	expectedExp := map[string]ResumeExperience{}
	for _, r := range base.Experiences {
		expectedExp[r.BlockID] = r
	}
	for i, r := range d.Experiences {
		if x, ok := expectedExp[r.BlockID]; ok {
			checkBullets(fmt.Sprintf("experiences[%d]", i), r.BlockID, r.Bullets, len(x.Bullets), r.Company == x.Company && r.Role == x.Role && r.Dates == x.Dates && r.Location == x.Location)
		}
	}
	expectedProj := map[string]ResumeProject{}
	for _, r := range base.Projects {
		expectedProj[r.BlockID] = r
	}
	for i, r := range d.Projects {
		if x, ok := expectedProj[r.BlockID]; ok {
			checkBullets(fmt.Sprintf("projects[%d]", i), r.BlockID, r.Bullets, len(x.Bullets), r.Name == x.Name && r.Descriptor == x.Descriptor)
		}
	}
	if !reflect.DeepEqual(d.Skills, base.Skills) {
		v.add("RESUME_SKILLS_MISMATCH", "Skills are selected in Resume Plan and cannot be invented in the material editor.", "skills", true, true)
	}
	if !reflect.DeepEqual(d.Education, base.Education) {
		v.add("RESUME_EDUCATION_MISMATCH", "Education is sourced from Career Vault and cannot be changed here.", "education", true, true)
	}
	text, e := ResumePlainText(d)
	if e != nil {
		return nil, e
	}
	limit := 1000
	if d.TargetPages != 1 {
		limit = 1800
	}
	if len(strings.FieldsFunc(text, textcompat.IsSpace)) > limit {
		v.add("RESUME_CONTENT_BUDGET_EXCEEDED", fmt.Sprintf("Resume exceeds the %d-word safety budget for %d page(s).", limit, d.TargetPages), "content", false, true)
	}
	return v.finish(ctx)
}
