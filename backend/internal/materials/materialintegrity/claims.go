package materialintegrity

import (
	"fmt"

	"careerhub.local/backend/internal/materials"
)

// DraftClaims is a complete typed projection for the fact gate. No arbitrary
// field selectors are accepted. Source-only metadata is still placeholder-
// checked; the existing material validator remains responsible for its binding.
type DraftClaims struct {
	SHA256   string
	ProofIDs []string
	Claims   []FactClaim
}

func (p *DraftClaims) add(path, text string, refs []string, sourceOnly bool) {
	if text == "" {
		return
	}
	set := map[string]bool{}
	for _, id := range refs {
		set[id] = true
	}
	p.Claims = append(p.Claims, FactClaim{path, text, sortedKeys(set), sourceOnly})
}

func (p *DraftClaims) candidate(c materials.Candidate) {
	for _, field := range []struct{ name, value string }{{"name", c.Name}, {"email", c.Email}, {"location_line", c.Location}, {"phone", c.Phone}, {"linkedin", c.LinkedIn}, {"github", c.GitHub}, {"website", c.Website}} {
		p.add("candidate."+field.name, field.value, nil, true)
	}
}

func (p *DraftClaims) finish(draft any) (*DraftClaims, *Error) {
	raw, e := materials.JSON(draft)
	if e != nil {
		return nil, failure("MATERIAL_FACT_DRAFT_INVALID")
	}
	p.SHA256 = digest(raw)
	refs := map[string]bool{}
	for _, c := range p.Claims {
		for _, id := range c.ProofIDs {
			refs[id] = true
		}
	}
	p.ProofIDs = sortedKeys(refs)
	return p, nil
}

// ResumeClaims reparses the draft through the bounded typed model. This is not
// a replacement for ValidateResume against the current owner-built sources.
func ResumeClaims(payload []byte) (*DraftClaims, *Error) {
	d, e := materials.ParseResume(payload)
	if e != nil {
		return nil, failure("MATERIAL_FACT_DRAFT_INVALID")
	}
	p := &DraftClaims{Claims: []FactClaim{}}
	p.candidate(d.Candidate)
	p.add("summary", d.Summary, d.SummaryEvidenceRefs, false)
	for i, x := range d.Experiences {
		path := fmt.Sprintf("experiences[%d]", i)
		for _, f := range []struct{ name, value string }{{"company", x.Company}, {"role", x.Role}, {"dates", x.Dates}, {"location", x.Location}} {
			p.add(path+"."+f.name, f.value, nil, true)
		}
		for j, b := range x.Bullets {
			p.add(fmt.Sprintf("%s.bullets[%d].text", path, j), b.Text, b.EvidenceRefs, false)
		}
	}
	for i, x := range d.Projects {
		path := fmt.Sprintf("projects[%d]", i)
		p.add(path+".name", x.Name, nil, true)
		p.add(path+".descriptor", x.Descriptor, nil, true)
		for j, b := range x.Bullets {
			p.add(fmt.Sprintf("%s.bullets[%d].text", path, j), b.Text, b.EvidenceRefs, false)
		}
	}
	for i, x := range d.Skills {
		path := fmt.Sprintf("skills[%d]", i)
		p.add(path+".label", x.Label, nil, true)
		for j, text := range x.Keywords {
			p.add(fmt.Sprintf("%s.keywords[%d]", path, j), text, nil, true)
		}
	}
	for i, x := range d.Education {
		path := fmt.Sprintf("education[%d]", i)
		for _, f := range []struct{ name, value string }{{"heading", x.Heading}, {"subheading", x.Subheading}, {"location", x.Location}, {"date_text", x.DateText}} {
			p.add(path+"."+f.name, f.value, nil, true)
		}
		for j, text := range x.Details {
			p.add(fmt.Sprintf("%s.details[%d]", path, j), text, nil, true)
		}
	}
	return p.finish(d)
}

// CoverClaims requires current source-generated defaults for the credential
// line. An altered credential line becomes an editable claim needing evidence
// or an exact manual check. Do not accept these defaults from a client.
func CoverClaims(payload, currentDefaults []byte) (*DraftClaims, *Error) {
	d, e := materials.ParseCover(payload)
	if e != nil {
		return nil, failure("MATERIAL_FACT_DRAFT_INVALID")
	}
	defaults, e := materials.ParseCover(currentDefaults)
	if e != nil || defaults.JobID != d.JobID || defaults.ProfileRevision != d.ProfileRevision || defaults.EvidenceRevision != d.EvidenceRevision || defaults.JobFingerprint != d.JobFingerprint {
		return nil, failure("MATERIAL_FACT_DEFAULTS_STALE")
	}
	p := &DraftClaims{Claims: []FactClaim{}}
	p.candidate(d.Candidate)
	c := d.Content
	p.add("content.letter_date", c.Date, nil, true)
	for _, f := range []struct{ name, value string }{{"salutation", c.Salutation}, {"opening", c.Opening}, {"evidence_lead_in", c.EvidenceLeadIn}, {"closing", c.Closing}, {"sign_off", c.SignOff}, {"signature_name", c.SignatureName}} {
		p.add("content."+f.name, f.value, nil, false)
	}
	p.add("content.credential_line", c.CredentialLine, nil, c.CredentialLine == defaults.Content.CredentialLine)
	for i, x := range c.EvidenceItems {
		path := fmt.Sprintf("content.evidence_items[%d]", i)
		p.add(path+".label", x.Label, x.EvidenceRefs, false)
		p.add(path+".claim", x.Claim, x.EvidenceRefs, false)
	}
	return p.finish(d)
}
