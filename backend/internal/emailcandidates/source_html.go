package emailcandidates

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"careerhub.local/backend/internal/joburls"
	"careerhub.local/backend/internal/textcompat"
)

const handshakeTrackingSender = "handshake@notifications.joinhandshake.com"

var (
	handshakeSubmittedSubject = regexp.MustCompile(`(?i)^application\s+sent\s+to\s+(.+?)\s*(?:—|–|-)\s*here(?:'|’)?s\s+what(?:'|’)?s\s+next$`)
	handshakeSimilarHeading   = regexp.MustCompile(`(?i)\bapply\s+to\s+similar\s+jobs\b`)
	handshakeJobDetail        = regexp.MustCompile(`(?i)(?:\$|\bfull[- ]time\b|\bpart[- ]time\b|\binternship\b|\bcontract\b|\bremote\b|\bonsite\b|\bon-site\b|\bhybrid\b|[•·])`)
	handshakeWorkModeSuffix   = regexp.MustCompile(`(?i)\s*\((?:onsite|on-site|remote|hybrid)\)\s*$`)
	handshakeDetailSplit      = regexp.MustCompile(`\s*[•·]\s*`)
	linkedinRejectionSubject  = regexp.MustCompile(`(?i)^your\s+application\s+to\s+(.+?)\s+at\s+(.+?)$`)
	linkedinRejectionText     = regexp.MustCompile(`(?i)\bunfortunately,?\s+we\s+will\s+not\s+be\s+moving\s+forward\s+with\s+your\s+application\b`)
	linkedinAppliedMarker     = regexp.MustCompile(`(?i)\bapplied\s+on\s+[a-z]{3,9}\s+[0-9]{1,2}(?:,\s+[0-9]{4})?\b`)
	linkedinTrailingMarkers   = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\s+applied\s+on\s+[a-z]{3,9}\s+[0-9]{1,2}(?:,\s+[0-9]{4})?$`),
		regexp.MustCompile(`(?i)\s+easy apply$`),
		regexp.MustCompile(`(?i)\s+actively recruiting$`),
		regexp.MustCompile(`(?i)\s+be an early applicant$`),
		regexp.MustCompile(`(?i)\s+promoted$`),
		regexp.MustCompile(`(?i)\s+[0-9]+\s+school alumni$`),
		regexp.MustCompile(`(?i)\s*\((?:remote|hybrid|on[ -]?site)\)$`),
	}
)

type ApplicationOutcome struct {
	Ordinal           int    `json:"ordinal"`
	EventType         string `json:"event_type,omitempty"`
	Title             string `json:"title"`
	Company           string `json:"company"`
	Location          string `json:"location"`
	SourceURL         string `json:"source_url"`
	PostingIdentity   string `json:"posting_identity"`
	SourceProvider    string `json:"source_provider"`
	ApplicationStatus string `json:"application_status"`
	EvidenceCode      string `json:"evidence_code"`
}

// ExtractHTMLSource applies Python's source-specific branches before the
// generic HTML path. The sender must already be validated by the MIME/header
// layer. This does not infer an application outcome or access a URL.
func ExtractHTMLSource(text string, linkBudget, relationBudget int, senderAddress, subject string) (HTMLResult, error) {
	parsed, err := parseHTMLLinks(text, linkBudget, relationBudget)
	if err != nil {
		return HTMLResult{}, err
	}
	if textcompat.Fold(senderAddress) == handshakeTrackingSender {
		consumed := make(map[int]bool)
		if handshakeSubmissionCompany(subject) != "" {
			parsed.result.Candidates, consumed = extractHandshakeEmbedded(&parsed)
		}
		parsed.result.IgnoredLinks += len(parsed.valid) - len(consumed)
		return parsed.result, nil
	}
	linkedin, consumed := extractLinkedInGroups(&parsed)
	if len(linkedin) > 0 {
		parsed.result.Candidates = linkedin
		parsed.result.IgnoredLinks += len(parsed.valid) - len(consumed)
		return parsed.result, nil
	}
	extractGenericHTMLCandidates(&parsed)
	return parsed.result, nil
}

func handshakeSubmissionCompany(subject string) string {
	match := handshakeSubmittedSubject.FindStringSubmatch(textcompat.Strip(subject))
	if match == nil {
		return ""
	}
	company := strings.Trim(normalizeVisible(match[1]), " -|,;")
	if length := utf8.RuneCountInString(company); length < 1 || length > 300 {
		return ""
	}
	for _, r := range company {
		if unicode.IsLetter(r) {
			return company
		}
	}
	return ""
}

func handshakeLocation(detailLabels []string) string {
	for _, detail := range detailLabels {
		var parts []string
		for _, part := range handshakeDetailSplit.Split(detail, -1) {
			if normalizeVisible(part) != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			continue
		}
		location := strings.Trim(handshakeWorkModeSuffix.ReplaceAllString(parts[len(parts)-1], ""), " -|,;")
		location = normalizeVisible(location)
		if length := utf8.RuneCountInString(location); length < 1 || length > 300 {
			continue
		}
		switch textcompat.Fold(location) {
		case "full-time", "full time", "part-time", "part time", "contract":
			continue
		}
		for _, r := range location {
			if unicode.IsLetter(r) {
				return location
			}
		}
	}
	return ""
}

func longestFirst(values []string) string {
	longest := ""
	for _, value := range values {
		if utf8.RuneCountInString(value) > utf8.RuneCountInString(longest) {
			longest = value
		}
	}
	return longest
}

func extractHandshakeEmbedded(parsed *parsedHTMLLinks) ([]Candidate, map[int]bool) {
	consumed := make(map[int]bool)
	if !handshakeSimilarHeading.MatchString(parsed.result.VisibleText) {
		return nil, consumed
	}
	groups := make(map[string][]int)
	var order []string
	for id := range parsed.parser.anchors {
		url, ok := parsed.valid[id]
		if !ok {
			continue
		}
		if _, exists := groups[url]; !exists {
			order = append(order, url)
		}
		groups[url] = append(groups[url], id)
	}
	var candidates []Candidate
	for _, url := range order {
		ids := groups[url]
		seenLabels := make(map[string]bool)
		var labels, detailLabels, identityLabels []string
		for _, id := range ids {
			label := parsed.labels[id]
			key := textcompat.Fold(label)
			if label == "" || seenLabels[key] {
				continue
			}
			seenLabels[key] = true
			labels = append(labels, label)
			if handshakeJobDetail.MatchString(label) {
				detailLabels = append(detailLabels, label)
			} else if plausibleTitleLabel(label) {
				identityLabels = append(identityLabels, label)
			}
		}
		if len(identityLabels) < 2 || textcompat.Fold(identityLabels[0]) == textcompat.Fold(identityLabels[1]) {
			continue
		}
		company, title := identityLabels[0], identityLabels[1]
		var sourceTexts []string
		for _, id := range ids {
			if value := parsed.parser.selectBlock(parsed.parser.anchors[id], parsed.counts); value != "" {
				sourceTexts = append(sourceTexts, value)
			}
		}
		sourceText := longestFirst(append(sourceTexts, labels...))
		if sourceText == "" {
			sourceText = title
		}
		candidate := buildCandidate(url, sourceText)
		if candidate.Title == "" && plausibleTitleLabel(title) {
			candidate.Title = title
			candidate.ContentScope = contentScope(sourceText, candidate.Title, candidate.Company, candidate.Location, url)
		}
		candidate.Title, candidate.Company = title, company
		candidate.Location = handshakeLocation(detailLabels)
		candidate.SourceProvider = "handshake"
		candidate.EmailSectionKind = "embedded_recommendation"
		candidate.SourceRecommendation = Recommendation{Kind: "recommended_for_you", EvidenceText: "Apply to similar jobs"}
		if posting, err := joburls.Resolve(url); err == nil {
			candidate.PostingIdentity = posting.Identity
		}
		candidates = append(candidates, candidate)
		for _, id := range ids {
			consumed[id] = true
		}
	}
	return candidates, consumed
}

func linkedinContextKey(label string, ids []int) string {
	var key strings.Builder
	key.WriteString(textcompat.Fold(label))
	for _, id := range ids {
		key.WriteByte('|')
		key.WriteString(strconv.Itoa(id))
	}
	return key.String()
}

func afterRunes(value string, count int) string {
	for count > 0 && value != "" {
		_, width := utf8.DecodeRuneInString(value)
		value = value[width:]
		count--
	}
	return value
}

func startsLinkedInTitle(text, title string) bool {
	foldedText, foldedTitle := textcompat.Fold(text), textcompat.Fold(title)
	return strings.HasPrefix(foldedText, foldedTitle) &&
		strings.Contains(afterRunes(text, utf8.RuneCountInString(title)), "·")
}

func linkedinCardFields(title string, values []string) (string, string) {
	title = normalizeVisible(title)
	if title == "" {
		return "", ""
	}
	var remainders []string
	for _, value := range values {
		value = normalizeVisible(value)
		if !strings.HasPrefix(textcompat.Fold(value), textcompat.Fold(title)) {
			continue
		}
		remainder := strings.Trim(afterRunes(value, utf8.RuneCountInString(title)), " -|·")
		if strings.Contains(remainder, "·") {
			remainders = append(remainders, remainder)
		}
	}
	if len(remainders) == 0 {
		return "", ""
	}
	company, location, _ := strings.Cut(longestFirst(remainders), "·")
	company = strings.Trim(normalizeVisible(company), " -|,;")
	location = strings.Trim(normalizeVisible(location), " -|,;")
	if utf8.RuneCountInString(company) > 300 || utf8.RuneCountInString(location) > 600 {
		return "", ""
	}
	for pass := 0; pass < 16; pass++ {
		previous := location
		for _, marker := range linkedinTrailingMarkers {
			location = strings.Trim(marker.ReplaceAllString(location, ""), " -|,;")
		}
		if location == previous {
			break
		}
		if pass == 15 {
			for _, marker := range linkedinTrailingMarkers {
				if marker.MatchString(location) {
					return "", ""
				}
			}
		}
	}
	if company == "" || utf8.RuneCountInString(location) > 300 {
		return "", ""
	}
	return company, location
}

func extractLinkedInGroups(parsed *parsedHTMLLinks) ([]Candidate, map[int]bool) {
	groups := make(map[string][]int)
	canonical := make(map[string]string)
	var order []string
	for id := range parsed.parser.anchors {
		url, ok := parsed.valid[id]
		if !ok {
			continue
		}
		posting, err := joburls.Resolve(url)
		if err != nil || !strings.HasPrefix(posting.Identity, "linkedin:") {
			continue
		}
		postingID := strings.TrimPrefix(posting.Identity, "linkedin:")
		if _, exists := groups[postingID]; !exists {
			order = append(order, postingID)
			canonical[postingID] = posting.URL
		}
		groups[postingID] = append(groups[postingID], id)
	}
	var candidates []Candidate
	consumed := make(map[int]bool)
	for _, postingID := range order {
		ids := groups[postingID]
		var groupLabels, contextTexts, descendantTexts []string
		seenLabels := make(map[string]bool)
		seenContexts := make(map[string]bool)
		seenDescendants := make(map[string]bool)
		for _, id := range ids {
			consumed[id] = true
			label := parsed.labels[id]
			if key := textcompat.Fold(label); !seenLabels[key] {
				seenLabels[key] = true
				groupLabels = append(groupLabels, label)
			}
			contextKey := linkedinContextKey(label, parsed.parser.anchors[id].blockIDs)
			if !seenContexts[contextKey] {
				seenContexts[contextKey] = true
				contextTexts = append(contextTexts, parsed.parser.selectBlock(parsed.parser.anchors[id], parsed.counts))
			}
		}
		for _, id := range ids {
			for _, blockID := range parsed.parser.anchors[id].descendantBlockIDs {
				value := normalizeVisible(strings.Join(parsed.parser.blocks[blockID].chunks, ""))
				key := textcompat.Fold(value)
				if value != "" && !seenDescendants[key] {
					seenDescendants[key] = true
					descendantTexts = append(descendantTexts, value)
				}
			}
		}
		contextsAndLabels := append(append([]string(nil), contextTexts...), groupLabels...)
		var titleLabels, structuredTitles []string
		for _, label := range groupLabels {
			if plausibleTitleLabel(label) {
				titleLabels = append(titleLabels, label)
			}
		}
		for _, label := range descendantTexts {
			if !plausibleTitleLabel(label) {
				continue
			}
			for _, value := range contextsAndLabels {
				if startsLinkedInTitle(value, label) {
					structuredTitles = append(structuredTitles, label)
					break
				}
			}
		}
		var supported []string
		for _, label := range append(titleLabels, structuredTitles...) {
			for _, value := range contextsAndLabels {
				if startsLinkedInTitle(value, label) {
					supported = append(supported, label)
					break
				}
			}
		}
		titlePool := supported
		if len(titlePool) == 0 {
			titlePool = titleLabels
		}
		title := ""
		for _, value := range titlePool {
			if title == "" || utf8.RuneCountInString(value) < utf8.RuneCountInString(title) ||
				(utf8.RuneCountInString(value) == utf8.RuneCountInString(title) && textcompat.Fold(value) < textcompat.Fold(title)) {
				title = value
			}
		}
		sourceText := longestFirst(contextsAndLabels)
		if sourceText == "" {
			sourceText = title
		}
		candidate := buildCandidate(canonical[postingID], sourceText)
		if candidate.Title == "" && plausibleTitleLabel(title) {
			candidate.Title = title
			candidate.ContentScope = contentScope(sourceText, candidate.Title, candidate.Company, candidate.Location, candidate.SourceURL)
		}
		candidate.PostingIdentity = "linkedin:" + postingID
		candidate.SourceProvider = "linkedin"
		company, location := linkedinCardFields(candidate.Title, contextsAndLabels)
		if candidate.Company == "" {
			candidate.Company = company
		}
		if candidate.Location == "" {
			candidate.Location = location
		}
		candidates = append(candidates, candidate)
	}
	return candidates, consumed
}

// SeparateApplicationOutcomes mirrors Python's explicit after-part outcome
// pass. visibleText must combine all selected MIME bodies. No match never
// creates or updates an application record.
func SeparateApplicationOutcomes(subject, senderAddress, visibleText string, candidates []Candidate) ([]Candidate, []ApplicationOutcome) {
	remaining := append([]Candidate(nil), candidates...)
	var outcomes []ApplicationOutcome
	if textcompat.Fold(senderAddress) == handshakeTrackingSender {
		filtered := make([]Candidate, 0, len(remaining))
		for _, candidate := range remaining {
			if candidate.EmailSectionKind == "embedded_recommendation" {
				filtered = append(filtered, candidate)
			}
		}
		remaining = filtered
	}
	if at := strings.LastIndex(senderAddress, "@"); at >= 0 && textcompat.Fold(strings.TrimRight(senderAddress[at+1:], ".")) == "linkedin.com" {
		match := linkedinRejectionSubject.FindStringSubmatch(textcompat.Strip(subject))
		if match != nil && linkedinRejectionText.MatchString(visibleText) {
			title, company := textcompat.Fold(normalizeVisible(match[1])), textcompat.Fold(normalizeVisible(match[2]))
			matching := -1
			for i, candidate := range remaining {
				if candidate.SourceProvider != "linkedin" || !strings.HasPrefix(candidate.PostingIdentity, "linkedin:") ||
					textcompat.Fold(normalizeVisible(candidate.Title)) != title || textcompat.Fold(normalizeVisible(candidate.Company)) != company ||
					!linkedinAppliedMarker.MatchString(candidate.SourceText) {
					continue
				}
				if matching >= 0 {
					matching = -1
					break
				}
				matching = i
			}
			if matching >= 0 {
				candidate := remaining[matching]
				outcomes = append(outcomes, ApplicationOutcome{
					Ordinal: 1, Title: candidate.Title, Company: candidate.Company, Location: candidate.Location,
					SourceURL: candidate.SourceURL, PostingIdentity: candidate.PostingIdentity,
					SourceProvider: "linkedin", ApplicationStatus: "rejected", EvidenceCode: "linkedin_application_not_moving_forward",
				})
				remaining = append(remaining[:matching], remaining[matching+1:]...)
			}
		}
	}
	if textcompat.Fold(senderAddress) == handshakeTrackingSender {
		if company := handshakeSubmissionCompany(subject); company != "" {
			bodyPattern := regexp.MustCompile(`(?i)\byour\s+application\s+was\s+sent\s+to\s+` + regexp.QuoteMeta(company) + `\b`)
			if bodyPattern.MatchString(visibleText) {
				outcomes = append(outcomes, ApplicationOutcome{
					Ordinal: 1, EventType: "application_submitted", Company: company, SourceProvider: "handshake",
					ApplicationStatus: "applied", EvidenceCode: "handshake_application_sent",
				})
			}
		}
	}
	return remaining, outcomes
}
