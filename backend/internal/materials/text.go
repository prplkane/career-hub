package materials

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"careerhub.local/backend/internal/jobs/joburls"
	"careerhub.local/backend/internal/textcompat"
)

type ContactLink struct{ Display, Target, Color string }

var schemePrefix = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// ContactLinks provides renderer data, never network access. Unlike the source
// renderer's unrestricted hyperlink target, only plausible HTTP(S) links without
// credentials/controls are accepted. The original escaped path and case survive.
func ContactLinks(c Candidate) ([]ContactLink, *Error) {
	out := []ContactLink{}
	for _, item := range []struct{ value, color string }{{c.LinkedIn, "0563C1"}, {c.GitHub, "1155CC"}, {c.Website, "1155CC"}} {
		clean := textcompat.Strip(item.value)
		if clean == "" {
			continue
		}
		target := clean
		if !schemePrefix.MatchString(target) {
			target = "https://" + strings.TrimLeft(target, "/")
		}
		valid, e := joburls.ValidSource(target)
		if e != nil || !valid {
			return nil, failure("link", "MATERIAL_CONTACT_LINK_UNSAFE")
		}
		_, rest, _ := strings.Cut(target, "://")
		end := strings.IndexAny(rest, "/?#")
		if end < 0 {
			end = len(rest)
		}
		host := strings.TrimPrefix(rest[:end], "www.")
		path := rest[end:]
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
		path = strings.TrimRight(path, "/")
		display := host + path
		if len([]rune(display)) > 60 {
			display = strings.TrimRight(cutRunes(display, 57), "/") + "..."
		}
		out = append(out, ContactLink{display, target, item.color})
	}
	return out, nil
}
func joined(sep string, values ...string) string {
	out := []string{}
	for _, s := range values {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, sep)
}
func CoverEvidenceParagraph(d CoverDraft) string {
	p := []string{d.Content.EvidenceLeadIn}
	for _, i := range d.Content.EvidenceItems {
		p = append(p, i.Label+": "+i.Claim)
	}
	return strings.Join(p, " ")
}
func CoverPlainText(d CoverDraft) (string, *Error) {
	links, e := ContactLinks(d.Candidate)
	if e != nil {
		return "", e
	}
	labels := []string{}
	for _, l := range links {
		labels = append(labels, l.Display)
	}
	date, e2 := time.Parse("2006-01-02", d.Content.Date)
	if e2 != nil || date.Year() < 1 {
		return "", failure("date", "MATERIAL_LETTER_DATE_INVALID")
	}
	dateText := fmt.Sprintf("%s %d, %d", date.Month().String(), date.Day(), date.Year())
	p := []string{d.Candidate.Name + " | " + d.Candidate.Email, joined(" | ", d.Candidate.Location, d.Candidate.Phone, strings.Join(labels, "|")), "", dateText, d.Content.Salutation, "", d.Content.Opening, "", CoverEvidenceParagraph(d), "", d.Content.Closing, "", d.Content.SignOff, d.Content.SignatureName, d.Content.CredentialLine}
	text := textcompat.Strip(strings.Join(p, "\n")) + "\n"
	if len(text) > MaxResponseBytes {
		return "", failure("response", "MATERIAL_RESPONSE_LIMIT")
	}
	return text, nil
}
func ResumePlainText(d ResumeDraft) (string, *Error) {
	parts := append([]string{d.Candidate.Location, d.Candidate.Phone, d.Candidate.Email}, d.Candidate.Links()...)
	lines := []string{d.Candidate.Name, joined(" | ", parts...)}
	section := func(title string) {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, title)
	}
	if slices.Contains(d.Sections, "professional_summary") {
		section("PROFESSIONAL SUMMARY")
		if d.Summary != "" {
			lines = append(lines, d.Summary)
		}
	}
	if slices.Contains(d.Sections, "technical_skills") {
		section("TECHNICAL SKILLS")
		for _, s := range d.Skills {
			lines = append(lines, s.Label+": "+strings.Join(s.Keywords, ", "))
		}
	}
	if slices.Contains(d.Sections, "professional_experience") {
		section("PROFESSIONAL EXPERIENCE")
		for _, x := range d.Experiences {
			lines = append(lines, joined(" | ", x.Company, x.Dates), joined(" | ", x.Role, x.Location))
			for _, b := range x.Bullets {
				lines = append(lines, "- "+b.Text)
			}
		}
	}
	if slices.Contains(d.Sections, "projects") {
		section("PROJECTS")
		for _, p := range d.Projects {
			lines = append(lines, joined(" | ", p.Name, p.Descriptor))
			for _, b := range p.Bullets {
				lines = append(lines, "- "+b.Text)
			}
		}
	}
	for _, spec := range []struct{ key, kind, heading string }{{"education", "school", "EDUCATION"}, {"certifications", "certification", "CERTIFICATIONS"}} {
		if !slices.Contains(d.Sections, spec.key) {
			continue
		}
		started := false
		for _, x := range d.Education {
			if x.RecordType != spec.kind {
				continue
			}
			if !started {
				section(spec.heading)
				started = true
			}
			if spec.kind == "school" {
				lines = append(lines, joined(" | ", x.Heading, x.Location, x.DateText))
				if x.Subheading != "" {
					lines = append(lines, x.Subheading)
				}
				lines = append(lines, x.Details...)
			} else {
				lines = append(lines, joined(" | ", x.Heading, x.Subheading, x.DateText))
			}
		}
	}
	text := textcompat.Strip(strings.Join(lines, "\n")) + "\n"
	if len(text) > MaxResponseBytes {
		return "", failure("response", "MATERIAL_RESPONSE_LIMIT")
	}
	return text, nil
}
