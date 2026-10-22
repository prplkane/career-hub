// Package emailcandidates ports offline candidate extraction from already
// bounded email text. It does not fetch URLs or create saved jobs.
package emailcandidates

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

type Recommendation struct {
	Kind         string `json:"kind"`
	EvidenceText string `json:"evidence_text"`
}

type Candidate struct {
	Ordinal              int            `json:"ordinal"`
	Title                string         `json:"title"`
	Company              string         `json:"company"`
	Location             string         `json:"location"`
	SourceURL            string         `json:"source_url"`
	PostingIdentity      string         `json:"posting_identity"`
	SourceProvider       string         `json:"source_provider"`
	EmailSectionKind     string         `json:"email_section_kind"`
	SourceText           string         `json:"source_text"`
	ContentScope         string         `json:"content_scope"`
	NeedsContent         bool           `json:"needs_content"`
	SourceRecommendation Recommendation `json:"source_recommendation"`
}

var (
	fieldNames = []string{
		"title", "role", "company", "location", "summary", "description",
		"qualifications", "requirements", "responsibilities",
	}
	recommendationPhrases = []struct{ kind, phrase string }{
		{"great_match", "great match"},
		{"top_match", "top match"},
		{"strong_match", "strong match"},
		{"recommended_for_you", "recommended for you"},
	}
	ignoredLabels = set(
		"account", "email preferences", "help", "login", "manage email preferences",
		"manage preferences", "manage your job alerts", "preferences", "privacy",
		"privacy policy", "sign in", "social", "unsubscribe",
		"update your career interests", "view in browser", "view more jobs",
	)
	ignoredPaths = set(
		"account", "email-preferences", "help", "login", "manage-preferences",
		"preferences", "privacy", "privacy-policy", "signin", "support", "unsubscribe",
	)
	socialHosts = []string{"facebook.com", "instagram.com", "twitter.com", "x.com"}
	jobSignals  = []string{
		"career", "job", "opening", "opportunity", "position", "posting", "requisition", "role",
	}
	descriptionMarkers = []string{
		"description:", "qualifications:", "requirements:", "responsibilities:",
		"what you will do", "what you'll do", "what you bring",
	}
	fieldNamePattern              = regexp.MustCompile(`(?i)\b(?:title|role|company|location|summary|description|qualifications|requirements|responsibilities)\s*:\s*`)
	recommendationReplacePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bgreat\s+match\b`),
		regexp.MustCompile(`(?i)\btop\s+match\b`),
		regexp.MustCompile(`(?i)\bstrong\s+match\b`),
		regexp.MustCompile(`(?i)\brecommended\s+for\s+you\b`),
	}
)

func set(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func normalizeVisible(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		if (r <= 0x08) || (r >= 0x0b && r <= 0x0c) || (r >= 0x0e && r <= 0x1f) || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	return strings.Join(strings.FieldsFunc(cleaned, textcompat.IsSpace), " ")
}

func plainBlocks(value string) []string {
	var blocks []string
	start, search := 0, 0
	for search < len(value) {
		relativeLF := strings.IndexByte(value[search:], '\n')
		if relativeLF < 0 {
			break
		}
		lf := search + relativeLF
		separatorStart := lf
		if lf > 0 && value[lf-1] == '\r' {
			separatorStart--
		}
		cursor := lf + 1
		secondLF := false
		for cursor < len(value) {
			r, size := utf8.DecodeRuneInString(value[cursor:])
			if r == '\n' {
				cursor += size
				secondLF = true
				break
			}
			if !textcompat.IsSpace(r) {
				break
			}
			cursor += size
		}
		if !secondLF {
			search = lf + 1
			continue
		}
		if block := value[start:separatorStart]; normalizeVisible(block) != "" {
			blocks = append(blocks, block)
		}
		for cursor < len(value) {
			r, size := utf8.DecodeRuneInString(value[cursor:])
			if !textcompat.IsSpace(r) {
				break
			}
			cursor += size
		}
		start, search = cursor, cursor
	}
	if block := value[start:]; normalizeVisible(block) != "" {
		blocks = append(blocks, block)
	}
	return blocks
}

func plainURLs(value string) []string {
	var found []string
	for i := 0; i < len(value); i++ {
		prefixLen := 0
		if len(value)-i >= 7 && strings.EqualFold(value[i:i+7], "http://") {
			prefixLen = 7
		} else if len(value)-i >= 8 && strings.EqualFold(value[i:i+8], "https://") {
			prefixLen = 8
		}
		if prefixLen == 0 {
			continue
		}
		end := i + prefixLen
		for end < len(value) {
			r, size := utf8.DecodeRuneInString(value[end:])
			if textcompat.IsSpace(r) || r == '<' || r == '>' || r == '"' || r == '\'' {
				break
			}
			end += size
		}
		if end > i+prefixLen {
			found = append(found, value[i:end])
		}
		i = end - 1
	}
	return found
}

func absoluteHTTPURL(value string) (string, *url.URL) {
	candidate := textcompat.Strip(value)
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		!(strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) {
		return "", nil
	}
	for _, r := range candidate {
		if textcompat.IsSpace(r) {
			return "", nil
		}
	}
	return candidate, parsed
}

func hostMatches(host, expected string) bool {
	return host == expected || strings.HasSuffix(host, "."+expected)
}

func shouldIgnoreLink(parsed *url.URL, label string) bool {
	if normalized := textcompat.Fold(normalizeVisible(label)); normalized == "" || ignoredLabels[normalized] {
		return true
	}
	host := strings.TrimRight(textcompat.Fold(parsed.Hostname()), ".")
	path := parsed.EscapedPath()
	if hostMatches(host, "linkedin.com") {
		switch strings.TrimRight(textcompat.Fold(path), "/") {
		case "/comm/jobs/alerts", "/comm/jobs/search-results", "/comm/widgets/promo":
			return true
		}
	}
	for _, social := range socialHosts {
		if hostMatches(host, social) {
			return true
		}
	}
	for _, segment := range strings.Split(path, "/") {
		if textcompat.Strip(segment) != "" && ignoredPaths[textcompat.Fold(segment)] {
			return true
		}
	}
	return false
}

func urlHasJobSignal(parsed *url.URL) bool {
	searchable := textcompat.Fold(parsed.EscapedPath() + " " + parsed.RawQuery)
	for _, signal := range jobSignals {
		if strings.Contains(searchable, signal) {
			return true
		}
	}
	return false
}

func matchFieldAt(value string, start int, names []string) (int, bool) {
	for _, name := range names {
		if len(value)-start < len(name) || !strings.EqualFold(value[start:start+len(name)], name) {
			continue
		}
		cursor := start + len(name)
		for cursor < len(value) && value[cursor] == ' ' {
			cursor++
		}
		if cursor >= len(value) || value[cursor] != ':' {
			continue
		}
		cursor++
		for cursor < len(value) && value[cursor] == ' ' {
			cursor++
		}
		return cursor, true
	}
	return 0, false
}

func recommendationAt(value string, start int) (int, string, bool) {
	for _, item := range recommendationPhrases {
		end := start + len(item.phrase)
		if end > len(value) || !strings.EqualFold(value[start:end], item.phrase) {
			continue
		}
		if end < len(value) {
			r, _ := utf8.DecodeRuneInString(value[end:])
			if textcompat.IsWord(r) {
				continue
			}
		}
		return end, item.kind, true
	}
	return 0, "", false
}

func labeledField(sourceText string, names ...string) string {
	start := -1
	for index := 0; index < len(sourceText); index++ {
		if index != 0 && sourceText[index-1] != ' ' {
			continue
		}
		if valueStart, ok := matchFieldAt(sourceText, index, names); ok {
			start = valueStart
			break
		}
	}
	if start < 0 || start >= len(sourceText) {
		return ""
	}
	end := len(sourceText)
	for index := start; index < len(sourceText); index++ {
		if sourceText[index] != ' ' {
			continue
		}
		if _, ok := matchFieldAt(sourceText, index+1, fieldNames); ok {
			end = index
			break
		}
		if _, _, ok := recommendationAt(sourceText, index+1); ok {
			end = index
			break
		}
	}
	value := strings.Trim(normalizeVisible(sourceText[start:end]), " -|,;")
	if utf8.RuneCountInString(value) > 300 {
		return ""
	}
	return value
}

func sourceRecommendation(sourceText string) Recommendation {
	for index := 0; index < len(sourceText); index++ {
		if index != 0 {
			previous, _ := utf8.DecodeLastRuneInString(sourceText[:index])
			if textcompat.IsWord(previous) {
				continue
			}
		}
		if end, kind, ok := recommendationAt(sourceText, index); ok {
			return Recommendation{Kind: kind, EvidenceText: sourceText[index:end]}
		}
	}
	return Recommendation{Kind: "none"}
}

func wordCount(value string) int {
	count, inWord := 0, false
	for _, r := range value {
		if textcompat.IsWord(r) {
			if !inWord {
				count++
			}
			inWord = true
		} else {
			inWord = false
		}
	}
	return count
}

func replaceOnceInsensitive(value, target string) string {
	if target == "" {
		return value
	}
	if index := strings.Index(value, target); index >= 0 {
		return value[:index] + " " + value[index+len(target):]
	}
	pattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(target))
	if match := pattern.FindStringIndex(value); match != nil {
		return value[:match[0]] + " " + value[match[1]:]
	}
	return value
}

func contentScope(sourceText, title, company, location, sourceURL string) string {
	folded := textcompat.Fold(sourceText)
	for _, marker := range descriptionMarkers {
		if strings.Contains(folded, marker) && wordCount(sourceText) >= 35 {
			return "description_candidate"
		}
	}
	residual := sourceText
	for _, value := range []string{sourceURL, title, company, location} {
		residual = replaceOnceInsensitive(residual, value)
	}
	residual = fieldNamePattern.ReplaceAllString(residual, " ")
	for _, pattern := range recommendationReplacePatterns {
		residual = pattern.ReplaceAllString(residual, " ")
	}
	if wordCount(residual) >= 5 {
		return "snippet"
	}
	return "link_only"
}

func buildCandidate(sourceURL, sourceText string) Candidate {
	title := labeledField(sourceText, "title", "role")
	company := labeledField(sourceText, "company")
	location := labeledField(sourceText, "location")
	return Candidate{
		Title: title, Company: company, Location: location,
		SourceURL: sourceURL, SourceProvider: "generic", EmailSectionKind: "job_alert",
		SourceText: sourceText, ContentScope: contentScope(sourceText, title, company, location, sourceURL),
		NeedsContent: true, SourceRecommendation: sourceRecommendation(sourceText),
	}
}

// ExtractPlain mirrors _extract_plain_candidates after the MIME text-size gate.
// The returned candidates are not full JDs and have no application status.
func ExtractPlain(text string) ([]Candidate, int) {
	var candidates []Candidate
	ignored := 0
	seen := make(map[string]bool)
	for _, block := range plainBlocks(text) {
		sourceText := normalizeVisible(block)
		labeledTitle := labeledField(sourceText, "title", "role")
		for _, observed := range plainURLs(block) {
			observed = strings.TrimRight(observed, ".,;:!?)]}")
			sourceURL, parsed := absoluteHTTPURL(observed)
			if sourceURL == "" {
				ignored++
				continue
			}
			label := labeledTitle
			if label == "" {
				label = sourceURL
			}
			if shouldIgnoreLink(parsed, label) || (labeledTitle == "" && !urlHasJobSignal(parsed)) {
				ignored++
				continue
			}
			if seen[sourceURL] {
				continue
			}
			seen[sourceURL] = true
			candidates = append(candidates, buildCandidate(sourceURL, sourceText))
		}
	}
	return candidates, ignored
}
