package jobdigest

import (
	"encoding/json"
	"math"
	"math/big"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const locationPattern = `[A-Z][A-Za-z .'-]+,\s*(?:[A-Z]{2}(?:,\s*[A-Za-z ]+)?|[A-Z][A-Za-z ]+,\s*[A-Za-z ]+)`

func (a *analysis) labeled(name string) (Field, bool) {
	for _, l := range a.lines {
		if found, ok := a.matchKey(l.clean, ruleAliases["FIELD_"+strings.ToUpper(name)], "start"); ok {
			return field(textcompat.Strip(firstGroup(found)), "explicit", evidence(l.number, l.text, "labeled_"+name)), true
		}
	}
	return Field{}, false
}
func (a *analysis) title() Field {
	if text := textcompat.Strip(a.input.Title); text != "" {
		return field(text, "provided")
	}
	if value, ok := a.labeled("title"); ok {
		return value
	}
	for _, l := range a.lines[:min(len(a.lines), 12)] {
		length := utf8.RuneCountInString(l.clean)
		heading := a.heading(l.clean)
		isHeading := false
		for _, section := range []string{"responsibilities", "required", "preferred", "technology_stack"} {
			isHeading = isHeading || in("SECTION_"+section, heading)
		}
		if length >= 3 && length <= 120 && !isHeading && a.named(l.clean, "ROLE_WORDS") && !strings.HasSuffix(l.clean, ".") {
			return field(l.clean, "derived", evidence(l.number, l.text, "role_title_line"))
		}
	}
	return field("", "not_stated")
}
func (a *analysis) company(title Field) Field {
	if value := textcompat.Strip(a.input.Company); value != "" {
		return field(value, "provided")
	}
	if value, ok := a.labeled("company"); ok {
		return value
	}
	for _, l := range a.lines[:min(12, len(a.lines))] {
		if match, ok := a.match(analysisText(l.text), `\bJob\s+ID\s*:[^|]+\|\s*(.+)$`, true, ""); ok {
			return field(textcompat.Strip(firstGroup(match)), "explicit", evidence(l.number, l.text, "job_id_company"))
		}
	}
	titleLine := 0
	if len(title.Evidence) > 0 {
		titleLine = title.Evidence[0].Line
	}
	type candidate struct {
		line             line
		name             string
		suffix, repeated bool
	}
	candidates := []candidate{}
	counts := map[string]int{}
	for _, l := range a.lines[:min(10, len(a.lines))] {
		if l.number == titleLine {
			continue
		}
		value := l.clean
		if match, ok := a.match(value, `(.+?)(?:'s)?\s+logo`, true, "full"); ok {
			value = textcompat.Strip(firstGroup(match))
		}
		length := utf8.RuneCountInString(value)
		_, level := a.match(value, `(?:entry|junior|mid|senior|lead)\s+level`, true, "full")
		_, location := a.match(value, locationPattern, false, "full")
		if length < 2 || length > 100 || titleLine != 0 && l.number >= titleLine || in("GENERIC_COMPANY_LINES", textcompat.Fold(value)) || a.named(value, "ROLE_WORDS") || a.has(value, `\b(?:posted|apply by|match|coverage|department|job id)\b`, true) || level || location {
			continue
		}
		counts[textcompat.Fold(value)]++
		candidates = append(candidates, candidate{line: l, name: value, suffix: a.has(value, `\b(?:inc\.?|llc|ltd\.?|corp\.?|company)\b`, true)})
	}
	for i := range candidates {
		candidates[i].repeated = counts[textcompat.Fold(candidates[i].name)] > 1
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.suffix != right.suffix {
			return left.suffix
		}
		if left.repeated != right.repeated {
			return left.repeated
		}
		return left.line.number < right.line.number
	})
	if len(candidates) > 0 {
		c := candidates[0]
		return field(c.name, "derived", evidence(c.line.number, c.line.text, "near_title_company"))
	}
	for _, l := range a.lines {
		if match, ok := a.match(analysisText(l.text), `\b([A-Z][A-Za-z]+(?:\s+[A-Z][A-Za-z]+){0,3})'s\b`, false, ""); ok {
			return field(firstGroup(match), "needs_review", evidence(l.number, l.text, "possessive_brand"))
		}
	}
	parsed, err := url.Parse(a.input.SourceURL)
	if err != nil {
		a.err = ErrInput
		return field("", "not_stated")
	}
	// Python lowercases the complete hostname before selecting its first label.
	// Case-ignorable dots affect final-sigma context across that label boundary.
	host := strings.Split(textcompat.Lower(parsed.Hostname()), ".")[0]
	if host != "" && !slicesOf("apply", "app", "careers", "jobs", "www")(strings.ToLower(host)) {
		return field(textcompat.Title(strings.NewReplacer("-", " ", "_", " ").Replace(host)), "needs_review")
	}
	return field("", "not_stated")
}
func slicesOf(values ...string) func(string) bool {
	return func(value string) bool {
		for _, item := range values {
			if value == item {
				return true
			}
		}
		return false
	}
}

func (a *analysis) location() Field {
	if value, ok := a.labeled("location"); ok {
		return value
	}
	for _, l := range a.lines[:min(12, len(a.lines))] {
		_, first := a.match(l.clean, `[A-Z][A-Za-z .'-]+\s*-\s*[A-Z]{2}\s*\([^)]+\)`, false, "full")
		_, second := a.match(l.clean, locationPattern, false, "full")
		if first || second {
			return field(l.clean, "derived", evidence(l.number, l.text, "location_line"))
		}
	}
	for _, l := range a.lines {
		if match, ok := a.match(analysisText(l.text), `\b(?:based|located)\s+in\s+(.+?)(?:[.;]|\s+and\s+(?:be|work)\b|$)`, true, ""); ok {
			return field(strings.Trim(firstGroup(match), " ,"), "derived", evidence(l.number, l.text, "based_in_phrase"))
		}
	}
	locations, proof := []string{}, []Evidence{}
	for _, l := range a.lines {
		if _, ok := a.match(l.clean, `[A-Z]{2}\s*\([^)]+\)`, false, "full"); ok {
			locations = append(locations, l.clean)
			proof = append(proof, evidence(l.number, l.text, "listed_location"))
		}
	}
	if len(locations) > 0 {
		return field(strings.Join(locations, "; "), "derived", proof...)
	}
	byLocation := map[string]CompensationRange{}
	for _, item := range a.result.Compensation.Ranges {
		if value := textcompat.Strip(item.Location); value != "" {
			k := textcompat.Fold(value)
			if _, exists := byLocation[k]; !exists {
				byLocation[k] = item
			}
		}
	}
	if len(byLocation) == 1 {
		for _, item := range byLocation {
			proof := append([]Evidence{}, item.Evidence...)
			for i := range proof {
				proof[i].Rule = "sole_compensation_location"
			}
			return field(item.Location, "derived", proof...)
		}
	}
	return field("", "not_stated")
}
func (a *analysis) remote() Field {
	for _, l := range a.lines {
		for _, entry := range [][2]string{{"remote", `\b(?:fully\s+)?remote\b`}, {"hybrid", `\bhybrid\b`}, {"onsite", `\b(?:on[- ]?site|in[- ]?office)\b`}} {
			if a.has(analysisText(l.text), entry[1], true) {
				return field(entry[0], "derived", evidence(l.number, l.text, "workplace_phrase"))
			}
		}
	}
	return field("", "not_stated")
}
func (a *analysis) seniority(title Field) Field {
	for _, entry := range [][2]string{{"lead", `\b(?:lead|principal|staff)\b`}, {"senior", `\b(?:senior|sr\.?)\b`}, {"entry", `\b(?:entry|junior|jr\.?|new grad|graduate)\b`}, {"internship", `\b(?:intern|internship)\b`}} {
		if a.has(title.Value, entry[1], true) {
			proof := append([]Evidence{}, title.Evidence...)
			for i := range proof {
				proof[i].Rule = "seniority_from_title"
			}
			return field(entry[0], "derived", proof...)
		}
	}
	return field("", "not_stated")
}
func (a *analysis) fields() {
	title := a.title()
	a.result.Fields["title"] = title
	a.result.Fields["company"] = a.company(title)
	a.result.Compensation = a.compensation()
	a.result.Fields["location"] = a.location()
	a.result.Fields["remote_policy"] = a.remote()
	a.result.Fields["seniority"] = a.seniority(title)
}

func amount(value string) (json.Number, error) {
	if len(value) > 1024 {
		return "", ErrLimit
	}
	var cleaned strings.Builder
	for _, point := range value {
		if strings.ContainsRune("$€£,", point) || textcompat.IsSpace(point) {
			continue
		}
		if digit, ok := textcompat.DecimalDigit(point); ok {
			cleaned.WriteByte(byte('0' + digit))
		} else {
			cleaned.WriteRune(point)
		}
	}
	text := cleaned.String()
	multiple := float64(1)
	if strings.HasSuffix(strings.ToLower(text), "k") {
		multiple = 1000
		text = text[:len(text)-1]
	}
	number, err := strconv.ParseFloat(text, 64)
	number *= multiple
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return "", ErrInput
	}
	if math.Trunc(number) == number {
		integer, _ := new(big.Float).SetFloat64(number).Int(nil)
		return json.Number(integer.String()), nil
	}
	return json.Number(strconv.FormatFloat(number, 'g', -1, 64)), nil
}
func (a *analysis) period(value string) string {
	for _, entry := range [][2]string{{"year", `\b(?:annually|annual|yearly|per\s+year)\b|/(?:yr|year)\b`}, {"month", `\b(?:monthly|per\s+month)\b|/(?:mo|month)\b`}, {"week", `\b(?:weekly|per\s+week)\b|/(?:wk|week)\b`}, {"hour", `\b(?:hourly|per\s+hour)\b|/(?:hr|hour)\b`}} {
		if a.has(value, entry[1], true) {
			return entry[0]
		}
	}
	return "unspecified"
}
func (a *analysis) compensation() Compensation {
	result := Compensation{"not_stated", []CompensationRange{}}
	seen := map[[5]string]bool{}
	for _, l := range a.lines {
		if !a.check() {
			break
		}
		cleaned := a.clean(l.raw)
		for _, match := range a.allKey(cleaned, ruleAliases["COMPENSATION_RANGE_PATTERN"]) {
			if !a.named(match.Value(), "COMPENSATION_SIGNAL_PATTERN") && !a.named(cleaned[:match.Start()], "COMPENSATION_CONTEXT_PATTERN") {
				continue
			}
			currency := match.Group("currency")
			if currency != "" {
				currency = textcompat.Upper(currency)
			} else {
				for _, point := range match.Value() {
					if strings.ContainsRune("$€£", point) {
						currency = string(point)
						break
					}
				}
			}
			location := strings.Trim(cleaned[:match.Start()], " :-|\t")
			if a.named(location, "COMPENSATION_CONTEXT_PATTERN") {
				location = ""
			}
			minimum, err := amount(match.Group("minimum"))
			if err != nil {
				a.err = err
				return result
			}
			maximum, err := amount(match.Group("maximum"))
			if err != nil {
				a.err = err
				return result
			}
			item := CompensationRange{minimum, maximum, currency, a.period(cleaned), location, prefix(cleaned, 500), []Evidence{evidence(l.number, l.raw, "compensation_range")}}
			k := [5]string{minimum.String(), maximum.String(), currency, item.Period, textcompat.Fold(location)}
			if !seen[k] {
				if !a.item() {
					return result
				}
				seen[k] = true
				result.Ranges = append(result.Ranges, item)
			}
		}
	}
	if len(result.Ranges) > 0 {
		result.Status = "explicit"
	}
	return result
}
