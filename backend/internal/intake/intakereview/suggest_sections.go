package intakereview

import (
	"regexp"
	"sort"
	"strings"

	"careerhub.local/backend/internal/intake/intakeread"
)

var (
	skillSeparatorPattern    = regexp.MustCompile(`\s*(?:,|;|\|)\s*`)
	certificationDatePattern = regexp.MustCompile(`(?i)^(?:(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+)?(?:19|20)\d{2}$`)
	credentialPattern        = regexp.MustCompile(`(?i)\b(?:certificate|certification|credential|license)\b`)
)

func (s *suggestionState) suggestSummary() cardStats {
	blocks := sortedBlocks(s.extraction.Blocks)
	proposals, cards := map[string]map[string]bool{}, map[string]bool{}
	fresh := 0
	for _, span := range s.spans() {
		if span.typeName != "summary" {
			continue
		}
		owned := []intakeread.Block{}
		for _, block := range blocks {
			if strings.TrimSpace(block.Text) != "" && inSpan(block, span) {
				owned = append(owned, block)
			}
		}
		if len(owned) == 0 {
			continue
		}
		id := "summary_" + owned[0].ID
		if s.entityByID[id] == nil && s.confirmedCategoryGroup(owned, "summary") {
			continue
		}
		cards[id] = true
		if s.entityByID[id] == nil {
			entity := newEntity(id, "summary", summaryRule)
			content := make([]string, len(owned))
			for index, block := range owned {
				content[index] = strings.TrimSpace(block.Text)
			}
			entity["name"], entity["content"] = "Professional Summary", strings.Join(content, "\n\n")
			s.addEntity(entity, false)
			fresh++
		}
		for _, block := range owned {
			if decision := s.blocks[block.ID]; decision != nil && blockDisposition(decision) == "unassigned" {
				addProposal(proposals, block.ID, id)
			}
		}
	}
	suggested, assigned := s.linkProposals(proposals)
	return cardStats{len(cards), fresh, suggested, assigned}
}

func skillGroupFields(value string) (string, []string) {
	label, raw, found := strings.Cut(value, ":")
	if !found || strings.TrimSpace(label) == "" || strings.TrimSpace(raw) == "" {
		label, raw = "Skills", value
	}
	items, seen := []string{}, map[string]bool{}
	for _, item := range skillSeparatorPattern.Split(raw, -1) {
		item = strings.Trim(item, " -*•\t")
		if item != "" && !seen[item] {
			seen[item] = true
			items = append(items, item)
		}
	}
	return strings.TrimSpace(label), items
}

func (s *suggestionState) suggestSkills() cardStats {
	blocks := sortedBlocks(s.extraction.Blocks)
	mentions := map[string]map[string]bool{}
	for _, mention := range s.extraction.TechnologyMentions {
		if mentions[mention.BlockID] == nil {
			mentions[mention.BlockID] = map[string]bool{}
		}
		mentions[mention.BlockID][mention.TechnologyID] = true
	}
	proposals, cards := map[string]map[string]bool{}, map[string]bool{}
	fresh := 0
	for _, span := range s.spans() {
		if span.typeName != "skills" {
			continue
		}
		for _, block := range blocks {
			if strings.TrimSpace(block.Text) == "" || !inSpan(block, span) {
				continue
			}
			id := "skill_" + block.ID
			if s.entityByID[id] == nil && s.confirmedCategoryGroup([]intakeread.Block{block}, "skill") {
				continue
			}
			cards[id] = true
			if s.entityByID[id] == nil {
				entity := newEntity(id, "skill", skillRule)
				name, items := skillGroupFields(block.Text)
				entity["name"], entity["items"] = name, anyStrings(items)
				technologies := []string{}
				for value := range mentions[block.ID] {
					technologies = append(technologies, value)
				}
				sort.Strings(technologies)
				entity["technologies"] = anyStrings(technologies)
				s.addEntity(entity, false)
				fresh++
			}
			if decision := s.blocks[block.ID]; decision != nil && blockDisposition(decision) == "unassigned" {
				addProposal(proposals, block.ID, id)
			}
		}
	}
	suggested, assigned := s.linkProposals(proposals)
	return cardStats{len(cards), fresh, suggested, assigned}
}

func certificationFields(text string) map[string]string {
	parts := []string{}
	for _, part := range separatorPattern.Split(text, -1) {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	date := ""
	if len(parts) > 0 && certificationDatePattern.MatchString(parts[len(parts)-1]) {
		date, parts = parts[len(parts)-1], parts[:len(parts)-1]
	}
	body := strings.TrimSpace(strings.Join(parts, " | "))
	name, credential := body, body
	if comma := strings.SplitN(body, ",", 2); len(comma) == 2 && credentialPattern.MatchString(comma[1]) {
		name, credential = strings.TrimSpace(comma[0]), strings.TrimSpace(comma[1])
	}
	return map[string]string{"name": name, "credential": credential, "date_text": date}
}

func (s *suggestionState) suggestCertifications() cardStats {
	blocks := sortedBlocks(s.extraction.Blocks)
	mentions := map[string]map[string]bool{}
	for _, mention := range s.extraction.TechnologyMentions {
		if mentions[mention.BlockID] == nil {
			mentions[mention.BlockID] = map[string]bool{}
		}
		mentions[mention.BlockID][mention.TechnologyID] = true
	}
	proposals, cards := map[string]map[string]bool{}, map[string]bool{}
	fresh := 0
	for _, span := range s.spans() {
		if span.typeName != "certifications" {
			continue
		}
		for _, block := range blocks {
			if strings.TrimSpace(block.Text) == "" || !inSpan(block, span) {
				continue
			}
			id := "certification_" + block.ID
			if s.entityByID[id] == nil && s.confirmedCategoryGroup([]intakeread.Block{block}, "certification") {
				continue
			}
			cards[id] = true
			if s.entityByID[id] == nil {
				entity := newEntity(id, "certification", certificationRule)
				for key, value := range certificationFields(block.Text) {
					entity[key] = value
				}
				technologies := []string{}
				for value := range mentions[block.ID] {
					technologies = append(technologies, value)
				}
				sort.Strings(technologies)
				entity["technologies"] = anyStrings(technologies)
				s.addEntity(entity, false)
				fresh++
			}
			if decision := s.blocks[block.ID]; decision != nil && blockDisposition(decision) == "unassigned" {
				addProposal(proposals, block.ID, id)
			}
		}
	}
	suggested, assigned := s.linkProposals(proposals)
	return cardStats{len(cards), fresh, suggested, assigned}
}

func (s *suggestionState) classifyStructuralHeaders() int {
	categories := map[string]string{"contact": "profile", "summary": "summary", "experience": "experience", "education": "education", "projects": "project", "skills": "skill", "certifications": "certification"}
	structural := map[string]bool{}
	for _, hint := range s.extraction.SectionHints {
		block := s.blocks[hint.BlockID]
		if block == nil {
			continue
		}
		if blockDisposition(block) == "unassigned" {
			block["disposition"], block["owner_id"], block["suggested_owner_ids"] = "structural", nil, []any{}
		}
		if block["category"] == nil {
			if category := categories[hint.SectionType]; category != "" {
				block["category"] = category
			}
		}
		if blockDisposition(block) == "structural" {
			structural[hint.BlockID] = true
		}
	}
	return len(structural)
}
