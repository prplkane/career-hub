package intakereview

import (
	"math/big"
	"regexp"
	"strings"
	"unicode"

	"careerhub.local/backend/internal/intake/intakeread"
)

var (
	profileEmailPattern    = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
	profilePhonePattern    = regexp.MustCompile(`(?:\+?1[\s.-]?)?\(?[0-9]{3}\)?[\s.-][0-9]{3}[\s.-][0-9]{4}`)
	profileLinkPattern     = regexp.MustCompile(`(?i)(?:https?://|www\.)[^\s|,;]+|(?:linkedin\.com|github\.com)/[^\s|,;]+|(?:[A-Z0-9-]+\.)+[A-Z]{2,}(?:/[^\s|,;]+)?`)
	profileSuffixPattern   = regexp.MustCompile(`,\s*(?:[A-Za-z]\.?){2,}(?:\s.*)?$`)
	profileNameWordPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z'.-]*$`)
)

type profileFields struct {
	name, headline, location string
	emails, phones, links    []string
}

func (s *suggestionState) profileBlocks() []intakeread.Block {
	blocks := []intakeread.Block{}
	for _, block := range sortedBlocks(s.extraction.Blocks) {
		if strings.TrimSpace(block.Text) != "" {
			blocks = append(blocks, block)
		}
	}
	boundary := (*bigOrder)(nil)
	consider := func(value string) {
		candidate := bigOrderFrom(value)
		if boundary == nil || candidate.Cmp(boundary) < 0 {
			boundary = candidate
		}
	}
	orders := map[string]string{}
	for _, block := range blocks {
		orders[block.ID] = block.Order
	}
	for _, span := range s.spans() {
		consider(span.header.String())
	}
	for _, candidate := range s.extraction.DateCandidates {
		if value, ok := orders[candidate.BlockID]; ok {
			consider(value)
		}
	}
	if boundary == nil {
		boundary = bigOrderFrom("0")
		for _, block := range blocks {
			value := bigOrderFrom(block.Order)
			if value.Cmp(boundary) >= 0 {
				boundary = value.Add(value, bigOrderFrom("1"))
			}
		}
	}
	selected := map[string]intakeread.Block{}
	leading := 0
	for _, block := range blocks {
		if bigOrderFrom(block.Order).Cmp(boundary) < 0 && leading < 8 {
			selected[block.ID] = block
			leading++
		}
	}
	for _, span := range s.spans() {
		if span.typeName != "contact" {
			continue
		}
		for _, block := range blocks {
			value := order(block.Order)
			if value.Cmp(span.header) >= 0 && value.Cmp(span.end) < 0 {
				selected[block.ID] = block
			}
		}
	}
	result := make([]intakeread.Block, 0, len(selected))
	for _, block := range selected {
		result = append(result, block)
	}
	return sortedBlocks(result)
}

// bigOrder is a local alias that keeps profile-boundary calculations visibly
// separate from source text while retaining arbitrarily large persisted orders.
type bigOrder = big.Int

func bigOrderFrom(value string) *bigOrder { return order(value) }

func profileEmails(value string) []string {
	return profileEmailPattern.FindAllString(value, -1)
}

func isWordRune(value byte) bool {
	return value == '_' || value >= '0' && value <= '9' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func profilePhones(value string) []string {
	indices := profilePhonePattern.FindAllStringIndex(value, -1)
	result := make([]string, 0, len(indices))
	for _, index := range indices {
		if index[0] > 0 && isWordRune(value[index[0]-1]) || index[1] < len(value) && isWordRune(value[index[1]]) {
			continue
		}
		result = append(result, value[index[0]:index[1]])
	}
	return result
}

func profileLinks(value string) []string {
	indices := profileLinkPattern.FindAllStringIndex(value, -1)
	result := make([]string, 0, len(indices))
	for _, index := range indices {
		if index[0] > 0 && value[index[0]-1] == '@' {
			continue
		}
		result = append(result, strings.TrimSuffix(value[index[0]:index[1]], "."))
	}
	return result
}

func profileLinkKey(value string) string {
	value = strings.ToLower(value)
	for _, prefix := range []string{"https://", "http://", "www."} {
		value = strings.TrimPrefix(value, prefix)
	}
	return strings.TrimSuffix(value, "/")
}

func isContactLink(value string) bool {
	parts := strings.Split(profileLinkKey(value), "/")
	if len(parts) >= 3 && parts[0] == "linkedin.com" && parts[1] == "in" {
		return true
	}
	return len(parts) == 2 && parts[0] == "github.com" && parts[1] != ""
}

func phoneKey(value string) string {
	var result strings.Builder
	for _, char := range value {
		if unicode.IsDigit(char) {
			result.WriteRune(char)
		}
	}
	return result.String()
}

func dedupeProfile(values []string, key func(string) string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		normalized := key(value)
		if normalized != "" && !seen[normalized] {
			seen[normalized] = true
			result = append(result, value)
		}
	}
	return result
}

func withoutProfileContacts(value string) string {
	value = profileEmailPattern.ReplaceAllString(value, " ")
	for _, phone := range profilePhones(value) {
		value = strings.ReplaceAll(value, phone, " ")
	}
	for _, link := range profileLinks(value) {
		value = strings.ReplaceAll(value, link, " ")
	}
	return strings.Trim(strings.Join(strings.Fields(value), " "), " |,;-\t")
}

func profileNameCandidate(value string) string {
	value = strings.TrimSpace(profileSuffixPattern.ReplaceAllString(value, ""))
	lower := strings.ToLower(value)
	if value == "" || len(value) > 60 || isLocation(value) {
		return ""
	}
	for _, term := range []string{"contact", "email", "phone", "linkedin", "github"} {
		if strings.Contains(lower, term) {
			return ""
		}
	}
	words := strings.Fields(value)
	if isRole(value) && len(words) > 2 {
		return ""
	}
	if len(words) < 2 || len(words) > 4 {
		return ""
	}
	for _, word := range words {
		if !profileNameWordPattern.MatchString(word) {
			return ""
		}
	}
	return value
}

func extractProfileFields(blocks []intakeread.Block) profileFields {
	fields := profileFields{}
	cleaned := make([]string, 0, len(blocks))
	for _, block := range blocks {
		fields.emails = append(fields.emails, profileEmails(block.Text)...)
		fields.phones = append(fields.phones, profilePhones(block.Text)...)
		for _, link := range profileLinks(block.Text) {
			if isContactLink(link) {
				fields.links = append(fields.links, link)
			}
		}
		cleaned = append(cleaned, withoutProfileContacts(block.Text))
	}
	fields.emails = dedupeProfile(fields.emails, strings.ToLower)
	fields.phones = dedupeProfile(fields.phones, phoneKey)
	fields.links = dedupeProfile(fields.links, profileLinkKey)
	for _, line := range cleaned {
		if fields.name == "" {
			fields.name = profileNameCandidate(line)
		}
	}
	for _, line := range cleaned {
		if fields.location == "" && isLocation(line) {
			fields.location = line
		}
	}
	for _, line := range cleaned {
		if fields.headline == "" && line != "" && line != fields.name && line != fields.location && profileNameCandidate(line) != fields.name && isRole(line) && len(line) <= 100 {
			fields.headline = line
		}
	}
	return fields
}

func uniqueProfileValues(values []string, key func(string) string) map[string]bool {
	counts := map[string]int{}
	for _, value := range values {
		counts[key(value)]++
	}
	result := map[string]bool{}
	for value, count := range counts {
		if value != "" && count == 1 {
			result[value] = true
		}
	}
	return result
}

func (s *suggestionState) profileAutoLinkIDs(blocks []intakeread.Block, fields profileFields) map[string]bool {
	allEmails, allPhones, allLinks := []string{}, []string{}, []string{}
	for _, block := range s.extraction.Blocks {
		allEmails = append(allEmails, profileEmails(block.Text)...)
		allPhones = append(allPhones, profilePhones(block.Text)...)
		for _, link := range profileLinks(block.Text) {
			if isContactLink(link) {
				allLinks = append(allLinks, link)
			}
		}
	}
	uniqueEmails := uniqueProfileValues(allEmails, strings.ToLower)
	uniquePhones := uniqueProfileValues(allPhones, phoneKey)
	uniqueLinks := uniqueProfileValues(allLinks, profileLinkKey)
	textValues := map[string]bool{fields.name: fields.name != "", fields.headline: fields.headline != "", fields.location: fields.location != ""}
	eligible := map[string]bool{}
	for _, block := range blocks {
		rawLinks := profileLinks(block.Text)
		ambiguousLink := false
		for _, link := range rawLinks {
			if !isContactLink(link) {
				ambiguousLink = true
				break
			}
		}
		if ambiguousLink {
			continue
		}
		blockEmails, blockPhones, blockLinks := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, value := range profileEmails(block.Text) {
			blockEmails[strings.ToLower(value)] = true
		}
		for _, value := range profilePhones(block.Text) {
			blockPhones[phoneKey(value)] = true
		}
		for _, value := range rawLinks {
			blockLinks[profileLinkKey(value)] = true
		}
		if hasNonUnique(blockEmails, uniqueEmails) || hasNonUnique(blockPhones, uniquePhones) || hasNonUnique(blockLinks, uniqueLinks) {
			continue
		}
		cleaned := withoutProfileContacts(block.Text)
		if textValues[cleaned] || intersects(blockEmails, uniqueEmails) || intersects(blockPhones, uniquePhones) || intersects(blockLinks, uniqueLinks) {
			eligible[block.ID] = true
		}
	}
	return eligible
}

func hasNonUnique(values, unique map[string]bool) bool {
	for value := range values {
		if !unique[value] {
			return true
		}
	}
	return false
}

func intersects(left, right map[string]bool) bool {
	for value := range left {
		if right[value] {
			return true
		}
	}
	return false
}

func cloneEntityIndex(values map[string]map[string]any) map[string]map[string]any {
	result := make(map[string]map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func (s *suggestionState) suggestProfile() cardStats {
	blocks := s.profileBlocks()
	fields := extractProfileFields(blocks)
	hasContent := fields.name != "" || fields.headline != "" || fields.location != "" || len(fields.emails) > 0 || len(fields.phones) > 0 || len(fields.links) > 0
	hasContactSection := false
	for _, hint := range s.extraction.SectionHints {
		hasContactSection = hasContactSection || hint.SectionType == "contact"
	}
	if !hasContent || !(hasContactSection || len(fields.emails) > 0 || len(fields.phones) > 0 || len(fields.links) > 0) {
		return cardStats{}
	}
	existing := cloneEntityIndex(s.entityByID)
	const cardID = "profile_contact"
	fresh := 0
	if entity := s.entityByID[cardID]; entity == nil {
		entity = newEntity(cardID, "profile", profileRule)
		entity["name"], entity["headline"], entity["location"] = fields.name, fields.headline, fields.location
		entity["emails"], entity["phones"], entity["links"] = anyStrings(fields.emails), anyStrings(fields.phones), anyStrings(fields.links)
		s.addEntity(entity, true)
		fresh = 1
	} else {
		entity["suggestion_rule"] = profileRule
	}
	eligible := s.profileAutoLinkIDs(blocks, fields)
	for _, block := range blocks {
		decision := s.blocks[block.ID]
		if decision == nil || blockDisposition(decision) != "unassigned" || eligible[block.ID] {
			continue
		}
		owners := []string{}
		for _, owner := range stringSlice(decision["suggested_owner_ids"]) {
			if owner != cardID {
				owners = append(owners, owner)
			}
		}
		decision["suggested_owner_ids"] = anyStrings(owners)
	}
	proposals := map[string]map[string]bool{}
	for blockID := range eligible {
		if decision := s.blocks[blockID]; decision != nil && blockDisposition(decision) == "unassigned" {
			addProposal(proposals, blockID, cardID)
		}
	}
	suggested, assigned := s.linkProposalsUsing(proposals, existing)
	return cardStats{cards: 1, fresh: fresh, suggested: suggested, assigned: assigned}
}

func (s *suggestionState) linkProposalsUsing(proposals map[string]map[string]bool, entities map[string]map[string]any) (int, int) {
	original := s.entityByID
	s.entityByID = entities
	suggested, assigned := s.linkProposals(proposals)
	s.entityByID = original
	return suggested, assigned
}
