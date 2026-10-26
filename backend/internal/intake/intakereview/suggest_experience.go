package intakereview

import (
	"regexp"
	"sort"
	"strings"

	"careerhub.local/backend/internal/intake/intakeread"
)

var (
	rolePattern      = regexp.MustCompile(`[a-z]+(?:-[a-z]+)?`)
	separatorPattern = regexp.MustCompile(`(?:\||\t|�|–|—)|\s{2,}`)
)

var roleTerms = map[string]bool{
	"analyst": true, "architect": true, "assistant": true, "consultant": true,
	"co-founder": true, "cofounder": true, "coordinator": true, "cto": true,
	"developer": true, "director": true, "engineer": true, "founder": true,
	"intern": true, "lead": true, "manager": true, "mentor": true,
	"officer": true, "president": true, "specialist": true, "tutor": true, "volunteer": true,
}

func isRole(value string) bool {
	for _, word := range rolePattern.FindAllString(strings.ToLower(value), -1) {
		if roleTerms[word] {
			return true
		}
	}
	return false
}

func isLocation(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "remote") || strings.Contains(lower, "relocation") {
		return true
	}
	index := strings.LastIndex(value, ",")
	if index < 0 {
		return false
	}
	region := strings.TrimSpace(value[index+1:])
	if len(region) == 2 {
		return true
	}
	switch strings.ToLower(region) {
	case "us", "usa", "united states", "uk", "united kingdom", "canada":
		return true
	}
	return false
}

func cleanHeaderParts(blocks []intakeread.Block, date string) []string {
	parts := []string{}
	for _, block := range blocks {
		value := block.Text
		if date != "" {
			value = strings.ReplaceAll(value, date, " ")
		}
		for _, part := range separatorPattern.Split(value, -1) {
			if cleaned := strings.Trim(part, " -|,;:\t"); cleaned != "" {
				parts = append(parts, cleaned)
			}
		}
	}
	return parts
}

func roleFirstCommaFields(value string) (map[string]string, bool) {
	parts := []string{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) < 2 || !isRole(parts[0]) {
		return nil, false
	}
	result := map[string]string{"title": parts[0], "location": ""}
	if len(parts) >= 4 {
		result["organization"], result["location"] = strings.Join(parts[1:len(parts)-2], ", "), strings.Join(parts[len(parts)-2:], ", ")
	} else if len(parts) == 3 && strings.EqualFold(parts[2], "remote") {
		result["organization"], result["location"] = parts[1], parts[2]
	} else {
		result["organization"] = strings.Join(parts[1:], ", ")
	}
	return result, true
}

func experienceFields(blocks []intakeread.Block, date string) map[string]string {
	parts := cleanHeaderParts(blocks, date)
	if len(parts) == 1 && strings.Contains(parts[0], "/") {
		slashes := []string{}
		for _, part := range strings.Split(parts[0], "/") {
			if part = strings.TrimSpace(part); part != "" {
				slashes = append(slashes, part)
			}
		}
		role := false
		if len(slashes) >= 2 {
			for _, part := range slashes[1:] {
				role = role || isRole(part)
			}
		}
		if role {
			return map[string]string{"organization": slashes[0], "title": strings.Join(slashes[1:], " / "), "date_text": date, "location": ""}
		}
	}
	if len(parts) == 1 {
		if fields, ok := roleFirstCommaFields(parts[0]); ok {
			fields["date_text"] = date
			return fields
		}
	}
	title := ""
	for _, part := range parts {
		if isRole(part) {
			title = part
			break
		}
	}
	location := ""
	for _, part := range parts {
		if part != title && isLocation(part) {
			location = part
			break
		}
	}
	organization := ""
	for _, part := range parts {
		if part != title && part != location {
			organization = part
			break
		}
	}
	if title == "" && len(parts) > 0 {
		title = parts[0]
		if organization == title {
			organization = ""
			if len(parts) > 1 {
				organization = parts[1]
			}
		}
	}
	return map[string]string{"organization": organization, "title": title, "date_text": date, "location": location}
}

func entryBlocks(blocks []intakeread.Block, anchor int, dates map[string]bool) ([]intakeread.Block, []intakeread.Block) {
	start, preceding := anchor, 0
	for start > 0 && preceding < 2 {
		previous := blocks[start-1]
		if previous.IsBullet || dates[previous.ID] {
			break
		}
		start--
		preceding++
	}
	header := append([]intakeread.Block(nil), blocks[start:anchor+1]...)
	owned := append([]intakeread.Block(nil), header...)
	trailing := blocks[anchor+1:]
	if len(trailing) >= 2 && !trailing[0].IsBullet && !dates[trailing[0].ID] && trailing[1].IsBullet {
		owned = append(owned, trailing[0])
		trailing = trailing[1:]
	}
	for _, block := range trailing {
		if !block.IsBullet {
			break
		}
		owned = append(owned, block)
	}
	return header, owned
}

func (s *suggestionState) suggestExperience() cardStats {
	blocks := sortedBlocks(s.extraction.Blocks)
	dateByBlock, dateIDs := map[string]string{}, map[string]bool{}
	for _, candidate := range s.extraction.DateCandidates {
		dateByBlock[candidate.BlockID], dateIDs[candidate.BlockID] = candidate.SourceText, true
	}
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
		if span.typeName != "experience" {
			continue
		}
		section := []intakeread.Block{}
		for _, block := range blocks {
			if block.Text != "" && inSpan(block, span) {
				section = append(section, block)
			}
		}
		for index, anchor := range section {
			date, found := dateByBlock[anchor.ID]
			if !found || anchor.IsBullet {
				continue
			}
			header, owned := entryBlocks(section, index, dateIDs)
			id := "experience_" + header[0].ID
			if s.entityByID[id] == nil && s.confirmedCategoryGroup(owned, "experience") {
				continue
			}
			cards[id] = true
			fields := experienceFields(header, date)
			entity := s.entityByID[id]
			if entity == nil {
				entity = newEntity(id, "experience", experienceRule)
				for key, value := range fields {
					entity[key] = value
				}
				technologies := []string{}
				seen := map[string]bool{}
				for _, block := range owned {
					for technology := range mentions[block.ID] {
						seen[technology] = true
					}
				}
				for technology := range seen {
					technologies = append(technologies, technology)
				}
				sort.Strings(technologies)
				entity["technologies"] = anyStrings(technologies)
				s.addEntity(entity, false)
				fresh++
			} else if entity["suggestion_rule"] == legacyExperienceRule {
				organization, _ := entity["organization"].(string)
				title, _ := entity["title"].(string)
				combined := strings.Trim(strings.Join([]string{fields["organization"], fields["title"]}, " / "), " /")
				if organization == "" && fields["organization"] != "" && fields["title"] != "" && title == combined {
					entity["organization"], entity["title"], entity["suggestion_rule"] = fields["organization"], fields["title"], experienceRule
				}
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
