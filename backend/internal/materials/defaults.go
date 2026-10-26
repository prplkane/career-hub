package materials

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"careerhub.local/backend/internal/intake/resumeplan"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

// Sources is assembled from the owner's same-read Vault and SQL snapshot. It is
// never accepted as a replacement for sources supplied by a browser or agent.
type Sources struct {
	JobID, Company, RoleTitle, JobFingerprint string
	Candidate                                 Candidate
	ProfileRevision, CredentialLine           string
	Catalog                                   resumeplan.Catalog
	Plan                                      *resumeplan.Editor
	Education                                 map[string]any
}

// PlanFingerprint hashes the existing compact sorted-key, unescaped UTF-8 JSON
// contract. Canonicalization only processes the already validated Plan model.
func PlanFingerprint(p resumeplan.Plan) (string, *Error) {
	raw, e := resumeplan.PlanJSON(p)
	if e != nil {
		return "", failure("plan", "MATERIAL_PLAN_INVALID")
	}
	var canonical func(json.RawMessage) ([]byte, error)
	canonical = func(b json.RawMessage) ([]byte, error) {
		switch b[0] {
		case '{':
			var m map[string]json.RawMessage
			if e := json.Unmarshal(b, &m); e != nil {
				return nil, e
			}
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var out bytes.Buffer
			out.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					out.WriteByte(',')
				}
				key, _ := json.Marshal(k)
				out.Write(key)
				out.WriteByte(':')
				v, e := canonical(m[k])
				if e != nil {
					return nil, e
				}
				out.Write(v)
			}
			out.WriteByte('}')
			return out.Bytes(), nil
		case '[':
			var a []json.RawMessage
			if e := json.Unmarshal(b, &a); e != nil {
				return nil, e
			}
			var out bytes.Buffer
			out.WriteByte('[')
			for i, x := range a {
				if i > 0 {
					out.WriteByte(',')
				}
				v, e := canonical(x)
				if e != nil {
					return nil, e
				}
				out.Write(v)
			}
			out.WriteByte(']')
			return out.Bytes(), nil
		default:
			return b, nil
		}
	}
	b, err := canonical(raw)
	if err != nil {
		return "", failure("plan", "MATERIAL_PLAN_INVALID")
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b)), nil
}

type sourceProjection struct{ err *Error }

// Scalar metadata retains Python's display convention, including numeric GPA.
// Collections never become Go debug strings in candidate-facing prose.
func (p *sourceProjection) text(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.(bool); ok {
		if b {
			return "True"
		}
		return ""
	}
	if n, ok := v.(json.Number); ok {
		value := n.String()
		if strings.ContainsAny(value, ".eE") {
			parsed, e := cataloginput.ParseYAML([]byte("!!float " + value))
			if e != nil || parsed.Kind != "float" {
				p.err = failure("source", "MATERIAL_SOURCE_UNSUPPORTED")
				return ""
			}
			value = parsed.Text
		}
		if value == "0" || value == "0.0" || value == "-0.0" {
			return ""
		}
		return value
	}
	p.err = failure("source", "MATERIAL_SOURCE_UNSUPPORTED")
	return ""
}
func (p *sourceProjection) first(values ...any) string {
	for _, v := range values {
		s := p.text(v)
		if s != "" {
			return s
		}
	}
	return ""
}

func EvidenceCatalog(c resumeplan.Catalog) ([]Evidence, *Error) {
	out := []Evidence{}
	p := sourceProjection{}
	add := func(kind, id, label string, proofs []resumeplan.Proof) {
		for _, proof := range proofs {
			pid, claim := textcompat.Strip(proof.ID), textcompat.Strip(proof.Claim)
			if pid == "" || claim == "" {
				continue
			}
			skills := []string{}
			if proof.MatchedJobSkills != nil {
				skills = append(skills, (*proof.MatchedJobSkills)...)
			}
			out = append(out, Evidence{pid, kind, id, label, claim, skills})
		}
	}
	for _, r := range c.Experiences {
		label := joined(" / ", textcompat.Strip(p.text(r.Company)), textcompat.Strip(p.text(r.Role)))
		add("experience", r.ID, label, r.Proofs)
	}
	for _, r := range c.Projects {
		add("project", r.ID, p.first(r.Name, r.ID), r.Proofs)
	}
	if p.err != nil {
		return nil, p.err
	}
	return out, nil
}

func DefaultCover(s Sources, letterDate string) (*CoverDraft, *Error) {
	company, role := textcompat.Strip(s.Company), textcompat.Strip(s.RoleTitle)
	if company == "" || role == "" {
		return nil, failure("source", "MATERIAL_JOB_UNRESOLVED")
	}
	school, _, _ := strings.Cut(s.CredentialLine, ",")
	lead := "I offer:"
	if school != "" {
		lead = "As a graduate of " + school + ", I offer:"
	}
	d := CoverDraft{SchemaVersion: 1, ID: "cover_letter_primary", Kind: "cover_letter", TemplateID: CoverTemplate, JobID: s.JobID, JobFingerprint: s.JobFingerprint, ProfileRevision: s.ProfileRevision, EvidenceRevision: s.Catalog.VaultRevision, Status: "draft", Company: company, RoleTitle: role, Candidate: s.Candidate,
		Content: CoverContent{Date: letterDate, Salutation: "Dear " + company + " Hiring Team:", Opening: "[PERSONALIZE: confirm why the " + role + " role at " + company + " is genuinely motivating.]", EvidenceLeadIn: lead, EvidenceItems: []CoverEvidence{}, Closing: "Thank you for considering my application.", SignOff: "Sincerely,", SignatureName: s.Candidate.Name, CredentialLine: s.CredentialLine}}
	raw, e := JSON(d)
	if e != nil {
		return nil, e
	}
	return ParseCover(raw)
}

var slugNonASCII = regexp.MustCompile(`[^a-z0-9]+`)

func bulletFromProof(block string, p resumeplan.Proof, index int) ResumeBullet {
	id := fmt.Sprintf("%s_%d_%s", block, index+1, p.ID)
	clean := strings.Trim(slugNonASCII.ReplaceAllString(textcompat.Fold(id), "_"), "_")
	if clean == "" {
		clean = fmt.Sprintf("bullet_%d", index+1)
	}
	clean = strings.TrimRight(cutRunes(clean, 55), "_")
	sum := sha256.Sum256([]byte(id))
	return ResumeBullet{fmt.Sprintf("%s_%x", clean, sum[:6]), p.Claim, []string{p.ID}, "imported"}
}
func selectedBullets(block string, ids []string, proofs []resumeplan.Proof, limit int) []ResumeBullet {
	byID := map[string]resumeplan.Proof{}
	for _, p := range proofs {
		if p.ID != "" && p.Claim != "" {
			byID[p.ID] = p
		}
	}
	out := []ResumeBullet{}
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			out = append(out, bulletFromProof(block, p, len(out)))
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}
func (p *sourceProjection) education(selection resumeplan.Education, education map[string]any) *ResumeEducation {
	key := "certifications"
	if selection.RecordType == "school" {
		key = "schools"
	}
	records, _ := education[key].([]any)
	var record map[string]any
	for _, raw := range records {
		r, ok := raw.(map[string]any)
		if ok && p.text(r["id"]) == selection.BlockID && r["archived"] != true {
			record = r
			break
		}
	}
	if record == nil {
		return nil
	}
	x := ResumeEducation{BlockID: selection.BlockID, RecordType: selection.RecordType, Details: []string{}}
	if selection.RecordType == "school" {
		x.Heading = p.first(record["institution"], selection.BlockID)
		x.Subheading = joined(", ", textcompat.Strip(p.text(record["degree"])), textcompat.Strip(p.text(record["major"])))
		x.Location = p.text(record["location"])
		x.DateText = p.first(record["graduation_date"], record["dates"])
		gpa := textcompat.Strip(p.text(record["gpa"]))
		if gpa != "" {
			x.Details = append(x.Details, "GPA: "+gpa)
		}
		if honors, ok := record["honors"].([]any); ok && len(honors) > 0 {
			values := []string{}
			for _, v := range honors {
				if s := textcompat.Strip(p.text(v)); s != "" {
					values = append(values, s)
				}
			}
			x.Details = append(x.Details, "Honors: "+strings.Join(values, ", "))
		}
	} else {
		x.Heading = p.first(record["credential"], record["name"], selection.BlockID)
		issuer := p.text(record["name"])
		if issuer != x.Heading {
			x.Subheading = issuer
		}
		x.DateText = p.text(record["completed"])
	}
	return &x
}

func DefaultResume(s Sources) (*ResumeDraft, *Error) {
	if s.Plan == nil {
		return nil, failure("plan", "MATERIAL_PLAN_MISSING")
	}
	plan, c := s.Plan.Plan, s.Catalog
	fp, e := PlanFingerprint(plan)
	if e != nil {
		return nil, e
	}
	d := ResumeDraft{SchemaVersion: 1, ID: "resume_primary", Kind: "resume", TemplateID: ResumeTemplate, JobID: s.JobID, JobFingerprint: s.Plan.Fingerprint, ProfileRevision: s.ProfileRevision, EvidenceRevision: c.VaultRevision, PlanRevision: s.Plan.Revision, PlanFingerprint: fp, Status: "draft", Company: s.Company, RoleTitle: s.RoleTitle, TargetPages: plan.TargetPages, Candidate: s.Candidate, Sections: []string{}, SummaryEvidenceRefs: []string{}, Experiences: []ResumeExperience{}, Projects: []ResumeProject{}, Skills: []ResumeSkill{}, Education: []ResumeEducation{}}
	for _, section := range plan.Sections {
		if section.Included {
			d.Sections = append(d.Sections, section.Key)
		}
	}
	p := sourceProjection{}
	refs := []string{}
	experiences := map[string]resumeplan.ExperienceSource{}
	for _, x := range c.Experiences {
		experiences[x.ID] = x
	}
	for _, selection := range plan.Experiences {
		if selection.Decision != "include" {
			continue
		}
		source, ok := experiences[selection.BlockID]
		if !ok {
			continue
		}
		target := 2
		if selection.TargetBullets != nil && *selection.TargetBullets != 0 {
			target = *selection.TargetBullets
		}
		bullets := selectedBullets(selection.BlockID, selection.ProofIDs, source.Proofs, target)
		for _, b := range bullets {
			refs = append(refs, b.EvidenceRefs...)
		}
		d.Experiences = append(d.Experiences, ResumeExperience{selection.BlockID, p.text(source.Company), p.text(source.Role), p.text(source.Dates), p.text(source.Location), bullets})
	}
	projects := map[string]resumeplan.ProjectSource{}
	for _, x := range c.Projects {
		projects[x.ID] = x
	}
	for _, selection := range plan.Projects {
		source, ok := projects[selection.BlockID]
		if !ok {
			continue
		}
		bullets := selectedBullets(selection.BlockID, selection.ProofIDs, source.Proofs, 5)
		for _, b := range bullets {
			refs = append(refs, b.EvidenceRefs...)
		}
		d.Projects = append(d.Projects, ResumeProject{selection.BlockID, p.first(source.Name, selection.BlockID), p.first(source.Summary, source.Category), bullets})
	}
	skills := map[string]resumeplan.SkillSource{}
	for _, x := range c.Skills {
		skills[x.ID] = x
	}
	for _, selection := range plan.Skills {
		source, ok := skills[selection.BlockID]
		if ok {
			d.Skills = append(d.Skills, ResumeSkill{selection.BlockID, p.first(source.Family, selection.BlockID), selection.Keywords})
		}
	}
	for _, selection := range plan.Education {
		if x := p.education(selection, s.Education); x != nil {
			d.Education = append(d.Education, *x)
		}
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if !seen[ref] && len(d.SummaryEvidenceRefs) < 30 {
			d.SummaryEvidenceRefs = append(d.SummaryEvidenceRefs, ref)
			seen[ref] = true
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	raw, e := JSON(d)
	if e != nil {
		return nil, e
	}
	return ParseResume(raw)
}
