package materialrender

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strings"

	"careerhub.local/backend/internal/materials"
)

// PDF URI actions carry URI bytes, not UTF-8 display strings. Escaping only
// non-ASCII bytes preserves existing % escapes, query delimiters and identity.
func pdfURI(target string) string {
	var out strings.Builder
	for i := 0; i < len(target); i++ {
		if target[i] >= 128 {
			fmt.Fprintf(&out, "%%%02X", target[i])
		} else {
			out.WriteByte(target[i])
		}
	}
	return out.String()
}

func pdfPara(text string, face int, size, leading, after float64) pdfBlock {
	return pdfBlock{p: pdfParagraph{spans: []pdfSpan{{text, face}}, size: size, leading: leading, after: after}}
}
func renderCoverPDF(ctx context.Context, d materials.CoverDraft) ([]byte, *Error) {
	p := newPDF(ctx, "Cover Letter - "+d.Company+" - "+d.RoleTitle, "Job application cover letter", 36)
	rows, e := linkRows(d.Candidate)
	if e != nil {
		return nil, e
	}
	p.draw(d.Candidate.Name, 1, 15.5, 36, 51, true)
	emailWidth := p.widthOf(d.Candidate.Email, 0, 10)
	if 36+p.widthOf(d.Candidate.Name, 1, 15.5)+8 > 576-emailWidth {
		return nil, fail("MATERIAL_PDF_LAYOUT_OVERFLOW")
	}
	p.draw(d.Candidate.Email, 0, 10, 576-emailWidth, 51, true)
	p.writer.SetLineWidth(1.5)
	p.writer.Line(36, 56, 576, 56)
	p.draw(d.Candidate.Location, 0, 10, 36, 68, true)
	phoneWidth := p.widthOf(d.Candidate.Phone, 0, 10)
	phoneX := 36 + 540*0.408333 - phoneWidth/2
	if d.Candidate.Phone != "" && 36+p.widthOf(d.Candidate.Location, 0, 10)+8 > phoneX {
		return nil, fail("MATERIAL_PDF_LAYOUT_OVERFLOW")
	}
	p.draw(d.Candidate.Phone, 0, 10, phoneX, 68, true)
	headerEnd := 36 + p.widthOf(d.Candidate.Location, 0, 10)
	if d.Candidate.Phone != "" {
		headerEnd = math.Max(headerEnd, phoneX+phoneWidth)
	}
	for i, row := range rows {
		width := 0.0
		for j, link := range row {
			if j > 0 {
				width += p.widthOf("|", 0, 10)
			}
			width += p.widthOf(link.Display, 0, 10)
		}
		x, y := 576-width, 68+float64(i)*13
		if i == 0 && headerEnd+8 > x {
			return nil, fail("MATERIAL_PDF_LAYOUT_OVERFLOW")
		}
		for j, link := range row {
			if j > 0 {
				p.writer.SetTextColor(0, 0, 0)
				p.draw("|", 0, 10, x, y, false)
				x += p.widthOf("|", 0, 10)
			}
			color, err := hex.DecodeString(link.Color)
			if err != nil || len(color) != 3 {
				return nil, fail("MATERIAL_PDF_STYLE_INVALID")
			}
			p.writer.SetTextColor(color[0], color[1], color[2])
			p.writer.SetStrokeColor(color[0], color[1], color[2])
			p.draw(link.Display, 0, 10, x, y, j == 0)
			w := p.widthOf(link.Display, 0, 10)
			p.writer.SetLineWidth(0.5)
			p.writer.Line(x, y+1.2, x+w, y+1.2)
			p.writer.AddExternalLink(pdfURI(link.Target), x, y-10, w, 12)
			x += w
		}
	}
	p.writer.SetTextColor(0, 0, 0)
	p.writer.SetStrokeColor(0, 0, 0)
	p.y = 36 + 34 + float64(max(0, len(rows)-1))*13 + 24
	date, e := displayDate(d.Content.Date)
	if e != nil {
		return nil, e
	}
	blocks := []pdfBlock{pdfPara(date, 0, 10.5, 15.75, 10), pdfPara(d.Content.Salutation, 0, 10.5, 15.75, 10), pdfPara(d.Content.Opening, 0, 10.5, 13, 6), pdfPara(materials.CoverEvidenceParagraph(d), 0, 10.5, 15.75, 10), pdfPara(d.Content.Closing, 0, 10.5, 15.75, 10), pdfPara(d.Content.SignOff, 0, 10.5, 15.75, 2), pdfPara(d.Content.SignatureName, 0, 10.5, 15.75, 2)}
	if d.Content.CredentialLine != "" {
		blocks = append(blocks, pdfPara(d.Content.CredentialLine, 0, 10.5, 15.75, 2))
	}
	p.layout(blocks)
	return p.bytes()
}

func renderResumePDF(ctx context.Context, d materials.ResumeDraft) ([]byte, *Error) {
	// SimpleDocTemplate adds six points of frame padding inside the 36pt margin.
	p := newPDF(ctx, "Resume - "+d.Company+" - "+d.RoleTitle, "Tailored job application resume", 42)
	parts := append([]string{d.Candidate.Location, d.Candidate.Phone, d.Candidate.Email}, d.Candidate.Links()...)
	name := pdfPara(d.Candidate.Name, 1, 15, 16, 0)
	name.p.center = true
	contact := pdfPara(joinPresent(" | ", parts...), 0, 9.25, 11, 2)
	contact.p.center = true
	blocks := []pdfBlock{name, contact}
	heading := func(title string) {
		b := pdfPara(title, 1, 10.5, 11.5, 0)
		b.p.before = 5
		b.keepNext = true
		blocks = append(blocks, b, pdfBlock{rule: true})
	}
	two := func(left, right string) string {
		if right != "" {
			return left + " | " + right
		}
		return left
	}
	group := 0
	addBullets := func(bullets []materials.ResumeBullet) {
		for _, v := range bullets {
			b := pdfPara(v.Text, 0, 9.5, 11, 1)
			b.p.bullet = true
			b.group = group
			blocks = append(blocks, b)
		}
	}
	if slices.Contains(d.Sections, "professional_summary") {
		heading("PROFESSIONAL SUMMARY")
		blocks = append(blocks, pdfPara(d.Summary, 0, 9.75, 11.5, 1))
	}
	if slices.Contains(d.Sections, "technical_skills") {
		heading("TECHNICAL SKILLS")
		for _, v := range d.Skills {
			b := pdfPara("", 0, 9.5, 11, 0)
			// The source does not register a ReportLab font-family mapping:
			// its <b> label actually uses the regular embedded face. Preserve
			// that visible template, rather than silently redesigning it.
			b.p.spans = []pdfSpan{{v.Label + ": " + strings.Join(v.Keywords, ", "), 0}}
			blocks = append(blocks, b)
		}
	}
	if slices.Contains(d.Sections, "professional_experience") {
		heading("PROFESSIONAL EXPERIENCE")
		for _, v := range d.Experiences {
			group++
			a := pdfPara(two(v.Company, v.Dates), 1, 9.75, 11.5, 0)
			a.group = group
			b := pdfPara(two(v.Role, v.Location), 0, 9.75, 11.5, 0)
			b.group = group
			blocks = append(blocks, a, b)
			addBullets(v.Bullets)
		}
	}
	if slices.Contains(d.Sections, "projects") {
		heading("PROJECTS")
		for _, v := range d.Projects {
			group++
			a := pdfPara(two(v.Name, v.Descriptor), 1, 9.75, 11.5, 0)
			a.group = group
			blocks = append(blocks, a)
			addBullets(v.Bullets)
		}
	}
	for _, spec := range []struct{ key, kind, title string }{{"education", "school", "EDUCATION"}, {"certifications", "certification", "CERTIFICATIONS"}} {
		if !slices.Contains(d.Sections, spec.key) {
			continue
		}
		started := false
		for _, v := range d.Education {
			if v.RecordType != spec.kind {
				continue
			}
			if !started {
				heading(spec.title)
				started = true
			}
			group++
			if spec.kind == "school" {
				a := pdfPara(two(v.Heading, joinPresent(" | ", v.Location, v.DateText)), 1, 9.75, 11.5, 0)
				a.group = group
				blocks = append(blocks, a)
				if v.Subheading != "" {
					b := pdfPara(v.Subheading, 0, 9.75, 11.5, 0)
					b.group = group
					blocks = append(blocks, b)
				}
				for _, v := range v.Details {
					b := pdfPara(v, 0, 9.5, 11, 0)
					b.group = group
					blocks = append(blocks, b)
				}
			} else {
				blocks = append(blocks, pdfPara(two(joinPresent(" | ", v.Heading, v.Subheading), v.DateText), 1, 9.75, 11.5, 0))
			}
		}
	}
	p.layout(blocks)
	return p.bytes()
}
