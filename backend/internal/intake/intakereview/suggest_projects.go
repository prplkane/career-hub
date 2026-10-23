package intakereview

import (
	"math/big"
	"sort"
	"strings"

	"careerhub.local/backend/internal/intake/intakeread"
)

func (s *suggestionState) projectGroups(span sectionSpan) [][]intakeread.Block {
	section := []intakeread.Block{}
	for _, block := range sortedBlocks(s.extraction.Blocks) {
		if inSpan(block, span) {
			section = append(section, block)
		}
	}
	starts := []int{}
	var previous *intakeread.Block
	for index := range section {
		block := section[index]
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		var following *intakeread.Block
		for next := index + 1; next < len(section); next++ {
			if strings.TrimSpace(section[next].Text) != "" {
				copy := section[next]
				following = &copy
				break
			}
		}
		startsEntry := previous == nil || previous.IsBullet
		hasBody := block.Kind == "table_cell" || following != nil && following.IsBullet
		if !block.IsBullet && startsEntry && hasBody {
			starts = append(starts, index)
		}
		copy := block
		previous = &copy
	}
	groups := make([][]intakeread.Block, 0, len(starts))
	for position, start := range starts {
		end := len(section)
		if position+1 < len(starts) {
			end = starts[position+1]
		}
		group := []intakeread.Block{}
		for _, block := range section[start:end] {
			if strings.TrimSpace(block.Text) != "" {
				group = append(group, block)
			}
		}
		groups = append(groups, group)
	}
	return groups
}

func (s *suggestionState) projectSpans() ([]sectionSpan, string) {
	spans := []sectionSpan{}
	for _, span := range s.spans() {
		if span.typeName == "projects" {
			spans = append(spans, span)
		}
	}
	if len(spans) > 0 || len(s.extraction.SectionHints) > 0 {
		return spans, projectRule
	}
	end := big.NewInt(-1)
	for _, block := range s.extraction.Blocks {
		if value := order(block.Order); value.Cmp(end) > 0 {
			end = value
		}
	}
	end.Add(end, big.NewInt(1))
	return []sectionSpan{{"projects", big.NewInt(-1), big.NewInt(0), end}}, unsectionedProjectRule
}

func (s *suggestionState) suggestProjects() cardStats {
	mentions := map[string]map[string]bool{}
	for _, mention := range s.extraction.TechnologyMentions {
		if mentions[mention.BlockID] == nil {
			mentions[mention.BlockID] = map[string]bool{}
		}
		mentions[mention.BlockID][mention.TechnologyID] = true
	}
	dates := map[string]string{}
	for _, candidate := range s.extraction.DateCandidates {
		dates[candidate.BlockID] = candidate.SourceText
	}
	proposals, cards := map[string]map[string]bool{}, map[string]bool{}
	fresh := 0
	spans, rule := s.projectSpans()
	for _, span := range spans {
		for _, owned := range s.projectGroups(span) {
			if len(owned) == 0 {
				continue
			}
			id := "project_" + owned[0].ID
			if s.entityByID[id] == nil && s.confirmedCategoryGroup(owned, "project") {
				continue
			}
			cards[id] = true
			date := ""
			for _, block := range owned {
				if value := dates[block.ID]; value != "" {
					date = value
					break
				}
			}
			rawTitle, title := owned[0].Text, owned[0].Text
			location := educationDatePattern.FindStringIndex(rawTitle)
			if date == "" && location != nil && strings.TrimSpace(rawTitle[location[1]:]) == "" && (strings.HasSuffix(rawTitle[:location[0]], "\t") || strings.HasSuffix(strings.TrimSpace(rawTitle[:location[0]]), "|")) {
				date, title = rawTitle[location[0]:location[1]], rawTitle[:location[0]]
			} else if date != "" {
				title = strings.ReplaceAll(rawTitle, date, " ")
			}
			title = strings.Trim(title, " -|,;:\t")
			entity := s.entityByID[id]
			if entity == nil {
				entity = newEntity(id, "project", rule)
				entity["title"], entity["date_text"] = title, date
				items := []string{}
				for _, block := range owned[1:] {
					value := strings.TrimSpace(block.Text)
					if value != "" && value != date {
						items = append(items, value)
					}
				}
				entity["items"] = anyStrings(items)
				technologies, seen := []string{}, map[string]bool{}
				for _, block := range owned {
					for value := range mentions[block.ID] {
						seen[value] = true
					}
				}
				for value := range seen {
					technologies = append(technologies, value)
				}
				sort.Strings(technologies)
				entity["technologies"] = anyStrings(technologies)
				s.addEntity(entity, false)
				fresh++
			} else {
				currentTitle, _ := entity["title"].(string)
				currentRule, _ := entity["suggestion_rule"].(string)
				if currentTitle == strings.TrimSpace(owned[0].Text) && (currentRule == legacyProjectRule || title != currentTitle) {
					entity["title"] = title
					currentDate, _ := entity["date_text"].(string)
					if currentDate == "" {
						entity["date_text"] = date
					}
					items := []string{}
					for _, item := range stringSlice(entity["items"]) {
						if strings.TrimSpace(item) != date {
							items = append(items, item)
						}
					}
					entity["items"], entity["suggestion_rule"] = anyStrings(items), projectRule
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
