package materialrender

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/materials"
)

func displayDate(value string) (string, *Error) {
	d, e := time.Parse("2006-01-02", value)
	if e != nil || d.Year() < 1 {
		return "", fail("MATERIAL_LETTER_DATE_INVALID")
	}
	return fmt.Sprintf("%s %d, %d", d.Month().String(), d.Day(), d.Year()), nil
}

func linkRows(c materials.Candidate) ([][]materials.ContactLink, *Error) {
	links, problem := materials.ContactLinks(c)
	if problem != nil {
		return nil, fail(problem.Code)
	}
	rows := [][]materials.ContactLink{}
	row := []materials.ContactLink{}
	width := 0
	for _, link := range links {
		n := utf8.RuneCountInString(link.Display)
		if len(row) != 0 {
			n++
		}
		if len(row) != 0 && (len(row) >= 2 || width+n > 60) {
			rows = append(rows, row)
			row = nil
			width = 0
			n--
		}
		row = append(row, link)
		width += n
	}
	if len(row) != 0 {
		rows = append(rows, row)
	}
	return rows, nil
}

func coverProjection(d materials.CoverDraft, format string) (string, *Error) {
	if format == "txt" {
		s, e := materials.CoverPlainText(d)
		if e != nil {
			return "", fail(e.Code)
		}
		return s, nil
	}
	rows, e := linkRows(d.Candidate)
	if e != nil {
		return "", e
	}
	lines := []string{d.Candidate.Name + "\t" + d.Candidate.Email, d.Candidate.Location + "\t" + d.Candidate.Phone}
	for i, row := range rows {
		labels := []string{}
		for _, l := range row {
			labels = append(labels, l.Display)
		}
		if i == 0 {
			lines[1] += "\t" + strings.Join(labels, "|")
		} else {
			lines = append(lines, "\t"+strings.Join(labels, "|"))
		}
	}
	date, e := displayDate(d.Content.Date)
	if e != nil {
		return "", e
	}
	lines = append(lines, "", date, d.Content.Salutation, d.Content.Opening,
		materials.CoverEvidenceParagraph(d), d.Content.Closing, d.Content.SignOff, d.Content.SignatureName)
	if d.Content.CredentialLine != "" {
		lines = append(lines, d.Content.CredentialLine)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// This follows source renderer order, not the editor's sections slice order.
// Automatic DOCX numbering is decoration; unlike TXT's explicit "- ", it is
// not part of w:t and must not be manufactured by the final-text extractor.
func resumeProjection(d materials.ResumeDraft, format string) (string, *Error) {
	if format == "txt" {
		s, e := materials.ResumePlainText(d)
		if e != nil {
			return "", fail(e.Code)
		}
		return s, nil
	}
	parts := append([]string{d.Candidate.Location, d.Candidate.Phone, d.Candidate.Email}, d.Candidate.Links()...)
	lines := []string{d.Candidate.Name, joinPresent(" | ", parts...)}
	if slices.Contains(d.Sections, "professional_summary") {
		lines = append(lines, "PROFESSIONAL SUMMARY", d.Summary)
	}
	if slices.Contains(d.Sections, "technical_skills") {
		lines = append(lines, "TECHNICAL SKILLS")
		for _, group := range d.Skills {
			lines = append(lines, group.Label+": "+strings.Join(group.Keywords, ", "))
		}
	}
	twoSides := func(left, right string) string {
		if right != "" {
			if format == "pdf" {
				return left + " | " + right
			}
			return left + "\t" + right
		}
		return left
	}
	if slices.Contains(d.Sections, "professional_experience") {
		lines = append(lines, "PROFESSIONAL EXPERIENCE")
		for _, item := range d.Experiences {
			lines = append(lines, twoSides(item.Company, item.Dates), twoSides(item.Role, item.Location))
			for _, bullet := range item.Bullets {
				text := bullet.Text
				if format == "pdf" {
					text = "• " + text
				}
				lines = append(lines, text)
			}
		}
	}
	if slices.Contains(d.Sections, "projects") {
		lines = append(lines, "PROJECTS")
		for _, item := range d.Projects {
			lines = append(lines, twoSides(item.Name, item.Descriptor))
			for _, bullet := range item.Bullets {
				text := bullet.Text
				if format == "pdf" {
					text = "• " + text
				}
				lines = append(lines, text)
			}
		}
	}
	for _, spec := range []struct{ key, kind, title string }{{"education", "school", "EDUCATION"}, {"certifications", "certification", "CERTIFICATIONS"}} {
		if !slices.Contains(d.Sections, spec.key) {
			continue
		}
		started := false
		for _, item := range d.Education {
			if item.RecordType != spec.kind {
				continue
			}
			if !started {
				lines = append(lines, spec.title)
				started = true
			}
			if spec.kind == "school" {
				lines = append(lines, twoSides(item.Heading, joinPresent(" | ", item.Location, item.DateText)))
				if item.Subheading != "" {
					lines = append(lines, item.Subheading)
				}
				lines = append(lines, item.Details...)
			} else {
				lines = append(lines, twoSides(joinPresent(" | ", item.Heading, item.Subheading), item.DateText))
			}
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}
