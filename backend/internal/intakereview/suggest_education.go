package intakereview

import (
	"regexp"
	"strings"

	"careerhub.local/backend/internal/intakeread"
)

var (
	institutionPattern   = regexp.MustCompile(`(?i)\b(?:university|college|school|institute|academy)\b`)
	degreePattern        = regexp.MustCompile(`(?i)^(bachelor(?:'s)?(?:\s+of\s+[A-Za-z]+)?|master(?:'s)?(?:\s+of\s+[A-Za-z]+)?|associate(?:'s)?(?:\s+of\s+[A-Za-z]+)?|doctor(?:ate)?(?:\s+of\s+[A-Za-z]+)?|ph\.?d\.?|m\.?b\.?a\.?|b\.?sc\.?|m\.?sc\.?|b\.?s\.?|m\.?s\.?|b\.?a\.?|m\.?a\.?|diploma|certificate)(?:(?:\s+in\s+|,\s*)(.+))?$`)
	gpaPattern           = regexp.MustCompile(`(?i)\b(?:cumulative\s+)?GPA\s*:?\s*(\d(?:\.\d{1,2})?)(?:\s*/\s*\d(?:\.\d+)?)?`)
	educationDatePattern = regexp.MustCompile(`(?i)\b(?:(?:graduated|completed|expected)(?:\s+[A-Za-z]+)?\s+\d{4}|(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+\d{4}(?:\s*[-–—]\s*(?:(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+\d{4}|Present))?)\b`)
	honorPattern         = regexp.MustCompile(`(?i)\b(?:award|honor|dean(?:'|’)?s list|president(?:'|’)?s list|summa cum laude|magna cum laude|cum laude)\b`)
	honorPrefixPattern   = regexp.MustCompile(`(?i)^\s*(?:awards?|honors?)\s*:\s*`)
)

func withoutEducationDetails(value string) string {
	return strings.Trim(gpaPattern.ReplaceAllString(educationDatePattern.ReplaceAllString(value, ""), ""), " -|,;:\t")
}

func isEducationDegree(value string) bool {
	return degreePattern.MatchString(withoutEducationDetails(value))
}

func splitEducationChunk(blocks []intakeread.Block) [][]intakeread.Block {
	institutions, degrees := []int{}, []int{}
	for index, block := range blocks {
		if institutionPattern.MatchString(block.Text) && !strings.Contains(strings.ToLower(block.Text), "coursework") {
			institutions = append(institutions, index)
		}
		if isEducationDegree(block.Text) {
			degrees = append(degrees, index)
		}
	}
	if len(institutions) == 0 && len(degrees) == 0 {
		return nil
	}
	starts := institutions
	if len(degrees) > 0 && (len(institutions) == 0 || degrees[0] < institutions[0]) {
		starts = degrees
	}
	result := make([][]intakeread.Block, 0, len(starts))
	for index, start := range starts {
		end := len(blocks)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		result = append(result, append([]intakeread.Block(nil), blocks[start:end]...))
	}
	return result
}

func (s *suggestionState) educationGroups(span sectionSpan) [][]intakeread.Block {
	section := []intakeread.Block{}
	for _, block := range sortedBlocks(s.extraction.Blocks) {
		if inSpan(block, span) {
			section = append(section, block)
		}
	}
	result, chunk := [][]intakeread.Block{}, []intakeread.Block{}
	for _, block := range section {
		if strings.TrimSpace(block.Text) == "" {
			if len(chunk) > 0 {
				result = append(result, splitEducationChunk(chunk)...)
				chunk = nil
			}
			continue
		}
		chunk = append(chunk, block)
	}
	if len(chunk) > 0 {
		result = append(result, splitEducationChunk(chunk)...)
	}
	return result
}

func educationInstitutionFields(blocks []intakeread.Block) (string, string) {
	source := ""
	for _, block := range blocks {
		if institutionPattern.MatchString(block.Text) && !strings.Contains(strings.ToLower(block.Text), "coursework") {
			source = block.Text
			break
		}
	}
	if source == "" {
		return "", ""
	}
	cleaned := gpaPattern.ReplaceAllString(educationDatePattern.ReplaceAllString(source, ""), "")
	parts := []string{}
	for _, part := range separatorPattern.Split(cleaned, -1) {
		if part = strings.Trim(part, " -|,;:\t"); part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) > 1 {
		institution := parts[0]
		for _, part := range parts {
			if institutionPattern.MatchString(part) {
				institution = part
				break
			}
		}
		location := ""
		for _, part := range parts {
			if part != institution {
				location = part
				break
			}
		}
		return institution, location
	}
	comma := []string{}
	for _, part := range strings.Split(cleaned, ",") {
		if part = strings.TrimSpace(part); part != "" {
			comma = append(comma, part)
		}
	}
	if len(comma) >= 3 {
		return strings.Join(comma[:len(comma)-2], ", "), strings.Join(comma[len(comma)-2:], ", ")
	}
	if len(comma) == 2 {
		return comma[0], comma[1]
	}
	return strings.Trim(cleaned, " -|,;:\t"), ""
}

func educationDegreeFields(blocks []intakeread.Block) (string, string) {
	for _, block := range blocks {
		cleaned := gpaPattern.ReplaceAllString(educationDatePattern.ReplaceAllString(block.Text, ""), "")
		for _, part := range separatorPattern.Split(cleaned, -1) {
			candidate := strings.Trim(part, " -|,;:\t")
			match := degreePattern.FindStringSubmatch(candidate)
			if match != nil {
				major := ""
				if len(match) > 2 {
					major = strings.Trim(match[2], " -|,;:\t")
				}
				return strings.TrimSpace(match[1]), major
			}
		}
	}
	return "", ""
}

func educationFields(blocks []intakeread.Block) map[string]any {
	institution, location := educationInstitutionFields(blocks)
	degree, major := educationDegreeFields(blocks)
	date, gpa := "", ""
	honors, seen := []string{}, map[string]bool{}
	for _, block := range blocks {
		if date == "" && !honorPattern.MatchString(block.Text) {
			date = educationDatePattern.FindString(block.Text)
		}
		if gpa == "" {
			if match := gpaPattern.FindStringSubmatch(block.Text); len(match) > 1 {
				gpa = match[1]
			}
		}
		if honorPattern.MatchString(block.Text) {
			value := strings.TrimLeft(honorPrefixPattern.ReplaceAllString(block.Text, ""), " -*•\t")
			if value != "" && !seen[value] {
				seen[value] = true
				honors = append(honors, value)
			}
		}
	}
	return map[string]any{"institution": institution, "degree": degree, "major": major, "date_text": strings.Join(strings.Fields(date), " "), "location": location, "gpa": gpa, "honors": anyStrings(honors)}
}

func (s *suggestionState) suggestEducation() cardStats {
	proposals, cards := map[string]map[string]bool{}, map[string]bool{}
	fresh := 0
	for _, span := range s.spans() {
		if span.typeName != "education" {
			continue
		}
		for _, owned := range s.educationGroups(span) {
			if len(owned) == 0 {
				continue
			}
			id := "education_" + owned[0].ID
			if s.entityByID[id] == nil && s.confirmedCategoryGroup(owned, "education") {
				continue
			}
			cards[id] = true
			if s.entityByID[id] == nil {
				entity := newEntity(id, "education", educationRule)
				for key, value := range educationFields(owned) {
					entity[key] = value
				}
				s.addEntity(entity, false)
				fresh++
			}
			for _, block := range owned {
				if decision := s.blocks[block.ID]; decision != nil && blockDisposition(decision) == "unassigned" {
					addProposal(proposals, block.ID, id)
				}
			}
		}
	}
	suggested, assigned := s.linkProposals(proposals)
	return cardStats{len(cards), fresh, suggested, assigned}
}
