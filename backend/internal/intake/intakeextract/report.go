package intakeextract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"unicode"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/matching"
	"careerhub.local/backend/internal/skills/resolver"
	"careerhub.local/backend/internal/textcompat"
)

// Native recognition uses the reviewed registered-literal matcher rather
// than spaCy. A new version prevents treating its intentional corrections as
// an unchanged Python extraction when a writer is eventually activated.
const ExtractorVersion = "vault-intake-0.3"

var ErrInput = errors.New("invalid intake extraction input")
var ErrLimit = errors.New("intake extraction limit exceeded")

const maxCandidates = 4096
const maxReportBytes = 16 << 20

type Source struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	FileType string `json:"file_type"`
	SHA256   string `json:"sha256"`
}

type Block struct {
	ID       string  `json:"id"`
	Order    int     `json:"order"`
	Kind     string  `json:"kind"`
	Text     string  `json:"text"`
	Style    *string `json:"style"`
	IsBullet bool    `json:"is_bullet"`
}

// Start and End are nil for block-level references; matched text uses
// Unicode codepoint offsets, not UTF-8 bytes or JavaScript UTF-16 units.
type SourceRef struct {
	SourceID string `json:"source_id"`
	BlockID  string `json:"block_id"`
	Start    *int   `json:"start"`
	End      *int   `json:"end"`
}

type SectionHint struct {
	SectionType string    `json:"section_type"`
	SourceText  string    `json:"source_text"`
	SourceRef   SourceRef `json:"source_ref"`
	Extractor   string    `json:"extractor"`
}

type DateCandidate struct {
	SourceText string    `json:"source_text"`
	StartText  string    `json:"start_text"`
	EndText    *string   `json:"end_text"`
	Current    bool      `json:"current"`
	SourceRef  SourceRef `json:"source_ref"`
}

type BulletCandidate struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	SourceRef SourceRef `json:"source_ref"`
	Extractor string    `json:"extractor"`
}

type Report struct {
	SchemaVersion              int                `json:"schema_version"`
	Status                     string             `json:"status"`
	ExtractorVersion           string             `json:"extractor_version"`
	RegistryRevision           string             `json:"registry_revision"`
	Source                     Source             `json:"source"`
	Blocks                     []Block            `json:"blocks"`
	SectionHints               []SectionHint      `json:"section_hints"`
	TechnologyIDs              []string           `json:"technology_ids"`
	TechnologyMentions         []matching.Mention `json:"technology_mentions"`
	TechnologyResolutionIssues []matching.Issue   `json:"technology_resolution_issues"`
	DateCandidates             []DateCandidate    `json:"date_candidates"`
	BulletCandidates           []BulletCandidate  `json:"bullet_candidates"`
	UnresolvedFields           []string           `json:"unresolved_fields"`
}

var unresolvedFields = []string{
	"experience and project boundaries",
	"company and role assignment",
	"category selection",
	"bullet ownership and evidence review",
	"summary wording",
	"related-card approval",
}

var sections = map[string]string{
	"Summary": "summary", "Professional Summary": "summary", "Summary of Qualifications": "summary", "Profile": "summary", "Professional Profile": "summary",
	"Contact": "contact", "Contact Information": "contact",
	"Education":     "education",
	"Certification": "certifications", "Certifications": "certifications", "Licenses and Certifications": "certifications",
	"Coursework": "coursework", "Relevant Coursework": "coursework",
	"Projects": "projects", "Key Projects": "projects", "Selected Projects": "projects", "Project Experience": "projects", "Relevant Project Experience": "projects",
	"Experience": "experience", "Work Experience": "experience", "Relevant Work Experience": "experience", "Professional Experience": "experience", "Employment History": "experience",
	"Skills": "skills", "Technical Skills": "skills", "Core Skills": "skills",
	"Awards": "awards", "Honors and Awards": "awards",
	"Volunteer Experience": "volunteer", "Volunteering": "volunteer",
	"Involvement Experience": "involvement", "Leadership and Involvement": "involvement",
	"References": "references",
}

var sectionByKey = func() map[string]string {
	result := make(map[string]string, len(sections))
	for phrase, kind := range sections {
		result[textcompat.Normalize(phrase)] = kind
	}
	return result
}()

// Build is deliberately downstream of format parsing. The format adapter
// must provide ordered, exact source blocks, and the owning writer must pass
// a verified source identity and effective registry revision.
func Build(ctx context.Context, source Source, revision string, blocks []Block, terms []resolver.Term) (*Report, error) {
	if ctx == nil || source.ID == "" || source.Path == "" || len(source.ID) > 512 ||
		len(source.Path) > 4096 || len(source.SHA256) != 64 || len(revision) > 512 ||
		(source.FileType != "txt" && source.FileType != "docx") ||
		!utf8.ValidString(source.ID+source.Path+source.SHA256+revision) {
		return nil, ErrInput
	}
	for _, point := range source.SHA256 {
		if (point < '0' || point > '9') && (point < 'a' || point > 'f') {
			return nil, ErrInput
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(blocks) > matching.MaxBlocks {
		return nil, ErrLimit
	}
	m, err := matching.New(ctx, terms)
	if err != nil {
		return nil, err
	}
	input := make([]matching.Block, len(blocks))
	for i, block := range blocks {
		if block.Order != i || block.Kind != "paragraph" && block.Kind != "table_cell" && block.Kind != "blank" ||
			!utf8.ValidString(block.Text) {
			return nil, ErrInput
		}
		input[i] = matching.Block{ID: block.ID, Text: block.Text}
	}
	matched, err := m.FindBlocks(ctx, source.ID, input)
	if err != nil {
		return nil, err
	}
	result := &Report{
		SchemaVersion: 2, Status: "extracted", ExtractorVersion: ExtractorVersion,
		RegistryRevision: revision, Source: source,
		Blocks: append([]Block{}, blocks...), SectionHints: []SectionHint{},
		TechnologyIDs: []string{}, TechnologyMentions: []matching.Mention{},
		TechnologyResolutionIssues: []matching.Issue{}, DateCandidates: []DateCandidate{},
		BulletCandidates: []BulletCandidate{}, UnresolvedFields: append([]string{}, unresolvedFields...),
	}
	ids := make(map[string]struct{})
	for i, block := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if block.Text == "" {
			continue
		}
		if kind, ok := sectionByKey[textcompat.Normalize(block.Text)]; ok {
			result.SectionHints = append(result.SectionHints, SectionHint{kind, block.Text, SourceRef{SourceID: source.ID, BlockID: block.ID}, "registered_section_v1"})
		}
		result.TechnologyMentions = append(result.TechnologyMentions, matched[i].Mentions...)
		result.TechnologyResolutionIssues = append(result.TechnologyResolutionIssues, matched[i].Issues...)
		for _, mention := range matched[i].Mentions {
			ids[mention.TechnologyID] = struct{}{}
		}
		dates, dateErr := findDates(source.ID, block, maxCandidates-len(result.DateCandidates))
		if dateErr != nil {
			return nil, dateErr
		}
		result.DateCandidates = append(result.DateCandidates, dates...)
		if block.IsBullet {
			if cleaned := cleanBullet(block.Text); cleaned != "" {
				if len(result.BulletCandidates) >= maxCandidates {
					return nil, ErrLimit
				}
				result.BulletCandidates = append(result.BulletCandidates, BulletCandidate{
					ID:   fmt.Sprintf("%s_bullet_%03d", source.ID, len(result.BulletCandidates)+1),
					Text: cleaned, SourceRef: SourceRef{SourceID: source.ID, BlockID: block.ID}, Extractor: "document_bullet",
				})
			}
		}
	}
	for id := range ids {
		result.TechnologyIDs = append(result.TechnologyIDs, id)
	}
	sort.Strings(result.TechnologyIDs)
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, ErrInput
	}
	if len(encoded) > maxReportBytes {
		return nil, ErrLimit
	}
	return result, nil
}

var month = `(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:t(?:ember)?)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)`
var datePart = `(?:` + month + `\s+)?(?:19|20)\d{2}`
var dateRange = regexp.MustCompile(`(?i)(?P<start>` + datePart + `)\s*(?:-|–|—|to)\s*(?P<end>` + datePart + `|Present|Current|Now)`)
var completedDate = regexp.MustCompile(`(?i)\b(?:Completed|Graduated)\s+(?P<start>` + datePart + `)\b`)

func findDates(sourceID string, block Block, limit int) ([]DateCandidate, error) {
	result := []DateCandidate{}
	for _, pattern := range []*regexp.Regexp{dateRange, completedDate} {
		maxMatches := limit - len(result) + 1
		if pattern == completedDate {
			// Go's regexp \b is ASCII-only; Python's is Unicode-aware.
			// Bound rejected-prefix work without truncating a later valid date.
			maxMatches = maxCandidates*8 + 1
		}
		matches := pattern.FindAllStringSubmatchIndex(block.Text, maxMatches)
		if len(matches) == maxMatches && pattern == completedDate {
			return nil, ErrLimit
		}
		for _, span := range matches {
			startByte, endByte := span[0], span[1]
			if pattern == completedDate {
				if startByte > 0 {
					previous, _ := utf8.DecodeLastRuneInString(block.Text[:startByte])
					if previous == '_' || unicode.IsLetter(previous) || unicode.IsNumber(previous) {
						continue
					}
				}
				if endByte < len(block.Text) {
					next, _ := utf8.DecodeRuneInString(block.Text[endByte:])
					if next == '_' || unicode.IsLetter(next) || unicode.IsNumber(next) {
						continue
					}
				}
			}
			if len(result) >= limit {
				return nil, ErrLimit
			}
			start, end := utf8.RuneCountInString(block.Text[:startByte]), utf8.RuneCountInString(block.Text[:endByte])
			startGroup := pattern.SubexpIndex("start") * 2
			startText := block.Text[span[startGroup]:span[startGroup+1]]
			var endText *string
			if group := pattern.SubexpIndex("end"); group >= 0 && span[group*2] >= 0 {
				value := block.Text[span[group*2]:span[group*2+1]]
				endText = &value
			}
			current := endText != nil && (textcompat.Normalize(*endText) == "present" || textcompat.Normalize(*endText) == "current" || textcompat.Normalize(*endText) == "now")
			result = append(result, DateCandidate{block.Text[startByte:endByte], startText, endText, current, SourceRef{sourceID, block.ID, &start, &end}})
		}
	}
	return result, nil
}
