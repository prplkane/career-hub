package applicationanalysis

import (
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

func genericEmail(value string) bool {
	local, _, _ := strings.Cut(textcompat.Fold(value), "@")
	local = strings.Map(func(point rune) rune {
		if strings.ContainsRune("._+-", point) {
			return ' '
		}
		return point
	}, local)
	for _, token := range strings.FieldsFunc(local, textcompat.IsSpace) {
		if inSet("c._GENERIC_MAILBOX_LOCAL_PARTS", token) {
			return true
		}
	}
	return false
}

func channels(text matchingText, generic bool) ([]Channel, error) {
	result := make([]Channel, 0)
	seen := make(map[string]bool)
	add := func(kind, value, key string) error {
		identity := kind + ":" + key
		if seen[identity] {
			return nil
		}
		if len(result) == maxChannelsPerLine {
			return ErrLimit
		}
		seen[identity] = true
		result = append(result, Channel{kind, value})
		return nil
	}
	for _, match := range text.all("c._EMAIL_ADDRESS") {
		value := match.value()
		if genericEmail(value) == generic {
			if err := add("email", value, textcompat.Fold(value)); err != nil {
				return nil, err
			}
		}
	}
	if generic {
		return result, nil
	}
	for _, match := range text.all("c._PHONE_NUMBER") {
		value := textcompat.Strip(match.value())
		digits := make([]rune, 0, 11)
		for _, point := range value {
			if _, ok := textcompat.DecimalDigit(point); ok {
				digits = append(digits, point)
			}
		}
		if len(digits) == 10 || len(digits) == 11 {
			if err := add("phone", value, string(digits)); err != nil {
				return nil, err
			}
		}
	}
	for _, match := range text.all("c._PROFESSIONAL_PROFILE") {
		value := match.value()
		if err := add("professional_profile", value, textcompat.Fold(strings.TrimRight(value, "/"))); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (engine *matcher) personName(value string) string {
	segment := engine.text(textcompat.Strip(value)).split("c._NAME_BOUNDARY", true)[0]
	if match, ok := engine.text(segment).match("c._HONORIFIC", "start"); ok {
		segment = segment[match.end():]
	}
	segment = strings.Trim(segment, " .-–—")
	match, ok := engine.text(segment).match("c._PERSON_NAME", "full")
	if !ok {
		return ""
	}
	name := match.value()
	normalized := textcompat.Fold(name)
	if inSet("c._NON_PERSON_NAMES", normalized) {
		return ""
	}
	for _, token := range strings.FieldsFunc(strings.ReplaceAll(normalized, ".", ""), textcompat.IsSpace) {
		if inSet("c._NON_PERSON_TOKENS", token) {
			return ""
		}
	}
	return name
}
func (engine *matcher) lastPersonSegment(value string) string {
	parts := engine.text(value).split("x.last_person_split", false)
	for index := len(parts) - 1; index >= 0; index-- {
		if candidate := engine.personName(parts[index]); candidate != "" {
			return candidate
		}
	}
	return ""
}
func statedCompany(text matchingText, excluded string) string {
	match, ok := text.match("x.company", "")
	if !ok {
		return ""
	}
	company := strings.Trim(match.group("company"), " .,-")
	if textcompat.Fold(company) == textcompat.Fold(excluded) {
		return ""
	}
	return company
}
func statedReportingRole(text matchingText, name string) string {
	index := strings.Index(text.source, name)
	if index < 0 {
		return ""
	}
	match, ok := text.engine.text(text.source[index+len(name):]).match("x.reporting_role", "start")
	if !ok {
		return ""
	}
	role := text.engine.text(match.group("role")).split("x.role_company_split", true)[0]
	role = strings.Trim(collapsed(role), " ,")
	if role == "" || len(strings.FieldsFunc(role, textcompat.IsSpace)) > 10 || text.engine.text(role).has("c._EMAIL_ADDRESS") {
		return ""
	}
	return role
}

// CandidateID exposes the existing ordered identity algorithm for stored-card
// validation. It does not verify that the candidate is present in a source JD.
// Bounds apply to every consumed field, not to ignored display metadata.
func CandidateID(item ContactCandidate) (string, error) {
	if len(item.Channels) > maxChannelsPerLine {
		return "", ErrLimit
	}
	size := 0
	check := func(value string) error {
		if !utf8.ValidString(value) {
			return ErrInput
		}
		size += len(value)
		if size > MaxSourceBytes {
			return ErrLimit
		}
		return nil
	}
	for _, value := range []string{item.ContentSHA256, item.ContextKind, item.Confidence, item.Name, item.Role, item.Company, item.SourceText} {
		if err := check(value); err != nil {
			return "", err
		}
	}
	for _, channel := range item.Channels {
		if err := check(channel.Kind); err != nil {
			return "", err
		}
		if err := check(channel.Value); err != nil {
			return "", err
		}
	}
	return candidateID(item), nil
}

func candidateID(item ContactCandidate) string {
	channelKeys := make([]string, 0, len(item.Channels))
	for _, channel := range item.Channels {
		channelKeys = append(channelKeys, channel.Kind+":"+textcompat.Fold(collapsed(channel.Value)))
	}
	return digest(strings.Join([]string{item.ContentSHA256, item.ContextKind, item.Confidence,
		textcompat.Fold(collapsed(item.Name)), textcompat.Fold(collapsed(item.Role)), textcompat.Fold(collapsed(item.Company)),
		strings.Join(channelKeys, "\n"), textcompat.Fold(collapsed(item.SourceText))}, "\n"))
}

// emit returns false once the caller has enough unique candidates. This avoids
// building an unbounded intermediate list while preserving the source ordering.
func lineCandidates(text matchingText, evidence, contentHash string, lineNumber int, inSection bool, emit func(ContactCandidate) bool) error {
	direct, err := channels(text, false)
	if err != nil {
		return err
	}
	named := make(map[string]bool)
	add := func(kind, confidence, name, role, company, rule string, links []Channel) bool {
		exact := runePrefix(evidence, maxEvidenceChars)
		item := ContactCandidate{ContentSHA256: contentHash, Name: name, Role: role, Company: company, Channels: links,
			ContextKind: kind, Confidence: confidence, SourceText: exact, LineStart: lineNumber, LineEnd: lineNumber,
			Rule: rule, ExtractorVersion: 1, EvidenceTruncated: len(exact) < len(evidence)}
		item.CandidateID = candidateID(item)
		return emit(item)
	}
	for _, match := range text.all("c._RECRUITING_ROLE") {
		role := collapsed(match.value())
		before, after := text.source[:match.start()], text.engine.text(text.source[match.end():])
		name, roleCompany := "", ""
		if companyFirst, ok := after.match("x.company_first", "start"); ok {
			roleCompany = strings.Trim(companyFirst.group("company"), " .,-")
			name = text.engine.personName(companyFirst.group("tail"))
		} else if roleFirst, ok := after.match("x.role_first", "start"); ok {
			name = text.engine.personName(roleFirst.group("tail"))
		}
		if name == "" {
			name = text.engine.lastPersonSegment(before)
		}
		if name == "" {
			continue
		}
		company := roleCompany
		if company == "" {
			company = statedCompany(text, name)
		}
		kind := "recruiter"
		if strings.Contains(textcompat.Fold(role), "hiring manager") {
			kind = "hiring_manager"
		}
		if strings.Contains(textcompat.Fold(role), "application contact") {
			kind = "application_recipient"
		}
		named[textcompat.Fold(name)] = true
		if !add(kind, "explicit", name, role, company, "stated_recruiting_role", direct) {
			return nil
		}
	}
	for _, key := range []string{"c._ACTION_TAILS.0", "c._ACTION_TAILS.1", "c._ACTION_TAILS.2"} {
		for _, match := range text.all(key) {
			name := text.engine.personName(match.group("tail"))
			if name == "" || named[textcompat.Fold(name)] {
				continue
			}
			named[textcompat.Fold(name)] = true
			if !add("application_recipient", "explicit", name, "", statedCompany(text, name), "named_application_action", direct) {
				return nil
			}
		}
	}
	if len(direct) > 0 && len(named) == 0 {
		position := len(text.source)
		for _, key := range []string{"c._EMAIL_ADDRESS", "c._PHONE_NUMBER", "c._PROFESSIONAL_PROFILE"} {
			for _, match := range text.all(key) {
				if key == "c._EMAIL_ADDRESS" && genericEmail(match.value()) {
					continue
				}
				position = min(position, match.start())
			}
		}
		if position < len(text.source) {
			prefix := strings.TrimRightFunc(text.source[:position], textcompat.IsSpace)
			name := text.engine.lastPersonSegment(prefix)
			tied := inSection || text.has("c._APPLICATION_ACTION") || text.engine.text(prefix).has("x.visibly_tied")
			if name != "" && tied {
				named[textcompat.Fold(name)] = true
				if !add("application_recipient", "explicit", name, "", statedCompany(text, name), "direct_channel_tied_to_name", direct) {
					return nil
				}
			}
		}
	}
	for _, key := range []string{"c._REPORTING_TAILS.0", "c._REPORTING_TAILS.1"} {
		for _, match := range text.all(key) {
			name := text.engine.personName(match.group("tail"))
			if name == "" || named[textcompat.Fold(name)] {
				continue
			}
			named[textcompat.Fold(name)] = true
			confidence := "review"
			if len(direct) > 0 {
				confidence = "explicit"
			}
			if !add("possible_team_contact", confidence, name, statedReportingRole(text, name), statedCompany(text, name), "named_reporting_or_hiring_team_reference", direct) {
				return nil
			}
		}
	}
	if inSection || text.has("c._APPLICATION_ACTION") {
		generic, err := channels(text, true)
		if err != nil {
			return err
		}
		for _, channel := range generic {
			if !add("application_channel", "explicit", "", "", "", "generic_application_mailbox", []Channel{channel}) {
				return nil
			}
		}
	}
	return nil
}

func (source preparedSource) contacts() (ContactResult, error) {
	result := ContactResult{Scan: source.scan, ContentSHA256: source.contentHash, Candidates: make([]ContactCandidate, 0)}
	seen := make(map[string]bool)
	remaining, signature, noise := 0, 0, false
	emit := func(item ContactCandidate) bool {
		if seen[item.CandidateID] {
			return true
		}
		seen[item.CandidateID] = true
		result.Candidates = append(result.Candidates, item)
		return len(result.Candidates) < maxItems
	}
	for index, line := range source.lines {
		stripped := textcompat.Strip(line)
		if stripped == "" {
			continue
		}
		heading := headingKey(stripped)
		if inSet("i.APPLICATION_INSTRUCTION_HEADINGS", heading) || inSet("c._CONTACT_HEADINGS", heading) {
			remaining = 20
			noise = false
			signature = 0
			continue
		}
		if inSet("c._NOISE_HEADINGS", heading) {
			remaining = 0
			noise = true
			signature = 0
			continue
		}
		if inSet("c._SIGNATURE_MARKERS", heading) {
			remaining = 0
			signature = 5
			continue
		}
		if source.engine.looksLikeHeading(stripped, true) {
			remaining = 0
			noise = false
			signature = 0
		}
		text := source.engine.candidateText(stripped)
		inSection := remaining > 0
		if remaining > 0 {
			remaining--
		}
		if signature > 0 {
			signature--
			continue
		}
		if text.source == "" || noise || containsPhrase("c._NOISE_PHRASES", textcompat.Fold(text.source)) {
			continue
		}
		if err := lineCandidates(text, stripped, source.contentHash, index+1, inSection, emit); err != nil {
			return ContactResult{}, err
		}
		if len(result.Candidates) == maxItems {
			break
		}
	}
	result.CandidateCount = len(result.Candidates)
	result.Truncated = result.Truncated || result.CandidateCount >= maxItems
	return result, nil
}
