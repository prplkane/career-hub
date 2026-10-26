package materialrender

import (
	"context"
	"slices"
	"strings"

	"careerhub.local/backend/internal/materials"
)

func bodyParagraph(text string, after, line int) paragraph {
	return paragraph{runs: []run{{text: text, size: 21}}, after: after, line: line}
}

func renderCoverDOCX(ctx context.Context, draft materials.CoverDraft) ([]byte, *Error) {
	d := document{normalSize: 21, title: "Cover Letter - " + draft.Company + " - " + draft.RoleTitle,
		subject: "Job application cover letter", description: "Generated locally from a reviewed Career Hub draft."}
	d.add(paragraph{runs: []run{{text: draft.Candidate.Name, size: 31, bold: 1}, {text: "\t" + draft.Candidate.Email, size: 20}}, after: 20, line: 240, tabs: []tab{{10771, "right"}}, border: 12})
	rows, e := linkRows(draft.Candidate)
	if e != nil {
		return nil, e
	}
	second := paragraph{runs: []run{{text: draft.Candidate.Location, size: 20}, {text: "\t" + draft.Candidate.Phone, size: 20}}, after: 20, line: 240, tabs: []tab{{4410, "center"}, {10800, "right"}}}
	addLinks := func(p *paragraph, links []materials.ContactLink) {
		p.runs = append(p.runs, run{text: "\t", size: 20})
		for i, link := range links {
			if i > 0 {
				p.runs = append(p.runs, run{text: "|", size: 20})
			}
			p.runs = append(p.runs, run{text: link.Display, size: 20, link: &link})
		}
	}
	if len(rows) > 0 {
		addLinks(&second, rows[0])
	}
	d.add(second)
	for index, row := range rows {
		if index == 0 {
			continue
		}
		p := paragraph{after: 20, line: 240, tabs: []tab{{10800, "right"}}}
		addLinks(&p, row)
		d.add(p)
	}
	d.add(paragraph{after: 200, line: 240})
	date, e := displayDate(draft.Content.Date)
	if e != nil {
		return nil, e
	}
	d.add(bodyParagraph(date, 200, 360))
	d.add(bodyParagraph(draft.Content.Salutation, 200, 360))
	d.add(bodyParagraph(draft.Content.Opening, 200, 297))
	d.add(bodyParagraph(materials.CoverEvidenceParagraph(draft), 200, 360))
	d.add(bodyParagraph(draft.Content.Closing, 200, 360))
	d.add(bodyParagraph(draft.Content.SignOff, 40, 360))
	d.add(bodyParagraph(draft.Content.SignatureName, 20, 360))
	if draft.Content.CredentialLine != "" {
		d.add(bodyParagraph(draft.Content.CredentialLine, 0, 360))
	}
	return d.bytes(ctx)
}

func resumeParagraph(text string, size, after int) paragraph {
	return paragraph{runs: []run{{text: text, size: size}}, resume: true, after: after, line: 240}
}
func (d *document) heading(text string) {
	p := resumeParagraph(text, 21, 40)
	p.before = 100
	p.keep = true
	p.border = 6
	p.runs[0].bold = 1
	d.add(p)
}
func (d *document) twoSides(left, right string, bold, italic bool, after int) {
	b, i := -1, -1
	if bold {
		b = 1
	}
	if italic {
		i = 1
	}
	p := paragraph{resume: true, keep: true, line: 240, after: after, tabs: []tab{{10771, "right"}}, runs: []run{{text: left, size: 20, bold: b, italic: i}}}
	if right != "" {
		p.runs = append(p.runs, run{text: "\t" + right, size: 20, bold: b, italic: i})
	}
	d.add(p)
}
func (d *document) bullets(bullets []materials.ResumeBullet) {
	for _, bullet := range bullets {
		p := resumeParagraph(bullet.Text, 19, 20)
		p.style = "CareerHubResumeBullet"
		p.bullet = true
		d.add(p)
	}
}

func renderResumeDOCX(ctx context.Context, draft materials.ResumeDraft) ([]byte, *Error) {
	d := document{normalSize: 20, title: "Resume - " + draft.Company + " - " + draft.RoleTitle,
		subject: "Tailored job application resume", description: "Generated locally from a reviewed Career Hub Resume Plan."}
	p := resumeParagraph(draft.Candidate.Name, 30, 0)
	p.align = "center"
	p.runs[0].bold = 1
	d.add(p)
	parts := append([]string{draft.Candidate.Location, draft.Candidate.Phone, draft.Candidate.Email}, draft.Candidate.Links()...)
	p = resumeParagraph(joinPresent(" | ", parts...), 18, 40)
	p.align = "center"
	d.add(p)
	if slices.Contains(draft.Sections, "professional_summary") {
		d.heading("PROFESSIONAL SUMMARY")
		d.add(resumeParagraph(draft.Summary, 19, 20))
	}
	if slices.Contains(draft.Sections, "technical_skills") {
		d.heading("TECHNICAL SKILLS")
		for _, group := range draft.Skills {
			p := resumeParagraph("", 20, 0)
			p.runs = []run{{text: group.Label + ": ", size: 20, bold: 1}, {text: strings.Join(group.Keywords, ", "), size: 20}}
			d.add(p)
		}
	}
	if slices.Contains(draft.Sections, "professional_experience") {
		d.heading("PROFESSIONAL EXPERIENCE")
		for _, record := range draft.Experiences {
			d.twoSides(record.Company, record.Dates, true, false, 0)
			d.twoSides(record.Role, record.Location, false, true, 20)
			d.bullets(record.Bullets)
		}
	}
	if slices.Contains(draft.Sections, "projects") {
		d.heading("PROJECTS")
		for _, record := range draft.Projects {
			d.twoSides(record.Name, record.Descriptor, true, false, 20)
			d.bullets(record.Bullets)
		}
	}
	for _, spec := range []struct{ key, kind, title string }{{"education", "school", "EDUCATION"}, {"certifications", "certification", "CERTIFICATIONS"}} {
		if !slices.Contains(draft.Sections, spec.key) {
			continue
		}
		started := false
		for _, record := range draft.Education {
			if record.RecordType != spec.kind {
				continue
			}
			if !started {
				d.heading(spec.title)
				started = true
			}
			if spec.kind == "school" {
				d.twoSides(record.Heading, joinPresent(" | ", record.Location, record.DateText), true, false, 0)
				if record.Subheading != "" {
					p := resumeParagraph(record.Subheading, 20, 0)
					p.runs[0].italic = 1
					d.add(p)
				}
				for _, detail := range record.Details {
					d.add(resumeParagraph(detail, 19, 0))
				}
			} else {
				d.twoSides(joinPresent(" | ", record.Heading, record.Subheading), record.DateText, true, false, 0)
			}
		}
	}
	return d.bytes(ctx)
}
