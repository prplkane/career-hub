// Package discovery owns bounded local Job Feed projections and transaction-
// local review operations. An external owner must guard and commit writes.
// It never fetches a URL, reads a mailbox, submits an application or infers
// candidate experience.
package discovery

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

var ErrUnsupported = errors.New("discovery input exceeds the supported scalar bounds")

type FactInput struct {
	Provider, Title, Company, Location string
	SourceText                         string `json:"source_text"`
}
type Facts struct {
	Version         int          `json:"version"`
	Location        string       `json:"location"`
	WorkplaceType   string       `json:"workplace_type"`
	EmploymentType  string       `json:"employment_type"`
	EmploymentLabel string       `json:"employment_label"`
	Compensation    Compensation `json:"compensation"`
}
type Compensation struct {
	Status string              `json:"status"`
	Ranges []CompensationRange `json:"ranges"`
}
type CompensationRange struct {
	Minimum     json.Number    `json:"minimum"`
	Maximum     json.Number    `json:"maximum"`
	Currency    string         `json:"currency"`
	Period      string         `json:"period"`
	Location    string         `json:"location"`
	DisplayText string         `json:"display_text"`
	Evidence    []FactEvidence `json:"evidence"`
}
type FactEvidence struct {
	Line int    `json:"line"`
	Text string `json:"text"`
	Rule string `json:"rule"`
}

func validTexts(values ...string) bool {
	for _, value := range values {
		if len(value) > 2<<20 || !utf8.ValidString(value) {
			return false
		}
	}
	return true
}
func normalize(value string) string {
	return strings.Join(strings.FieldsFunc(value, textcompat.IsSpace), " ")
}
func factText(value string, maximum int) string {
	value = normalize(value)
	if utf8.RuneCountInString(value) > maximum {
		return ""
	}
	return value
}
func emptyCompensation() Compensation {
	return Compensation{Status: "not_stated", Ranges: []CompensationRange{}}
}

// All patterns here use ASCII literals/classes after whitespace normalization.
// Preserve Python's four extra ASCII-ignorecase matches and Unicode15 decimals,
// but keep an original-byte boundary map for exact captured wording/spans.
type matchText struct {
	raw, folded string
	boundaries  []int
}

func matching(raw string) matchText {
	var folded strings.Builder
	boundaries := []int{}
	for index, point := range raw {
		replacement := point
		switch {
		case point >= 'A' && point <= 'Z':
			replacement += 'a' - 'A'
		case point == 'İ' || point == 'ı':
			replacement = 'i'
		case point == 'ſ':
			replacement = 's'
		case point == 'K':
			replacement = 'k'
		default:
			if digit, found := textcompat.DecimalDigit(point); found {
				replacement = rune('0' + digit)
			}
		}
		text := string(replacement)
		for range len(text) {
			boundaries = append(boundaries, index)
		}
		folded.WriteString(text)
	}
	return matchText{raw, folded.String(), append(boundaries, len(raw))}
}
func (m matchText) text(indices []int, group int) string {
	start, end := indices[2*group], indices[2*group+1]
	if start < 0 {
		return ""
	}
	return m.raw[m.boundaries[start]:m.boundaries[end]]
}

var compensationPattern = regexp.MustCompile(`([$€£]|usd|cad|aud|eur|gbp)\s*(\d{1,3}(?:,\d{3})*(?:\.\d+)?)\s*([km]?)\s*(?:-|–|—|to)\s*([$€£]|usd|cad|aud|eur|gbp)?\s*(\d{1,3}(?:,\d{3})*(?:\.\d+)?)\s*([km]?)(\s*(?:/\s*|per\s+)(?:hr|hour|day|week|month|mo|yr|year))?`)
var workplacePattern = regexp.MustCompile(`\(\s*(remote|hybrid|on[ -]?site)\s*\)`)
var trailingMarkers = []*regexp.Regexp{
	regexp.MustCompile(`\s+easy apply$`), regexp.MustCompile(`\s+actively recruiting$`),
	regexp.MustCompile(`\s+be an early applicant$`), regexp.MustCompile(`\s+promoted$`),
	regexp.MustCompile(`\s+[0-9]+\s+school alum(?:ni)?$`),
}
var employmentPatterns = []struct {
	key, label string
	pattern    *regexp.Regexp
}{
	{"full_time", "Full-time", regexp.MustCompile(`^full[ -]?time$`)},
	{"part_time", "Part-time", regexp.MustCompile(`^part[ -]?time$`)},
	{"contract", "Contract", regexp.MustCompile(`^contract(?:or)?$`)},
	{"internship", "Internship", regexp.MustCompile(`^intern(?:ship)?$`)},
	{"temporary", "Temporary", regexp.MustCompile(`^(?:temporary|temp)$`)},
}

func amount(value, scale string) (float64, json.Number, error) {
	value = strings.ReplaceAll(value, ",", "")
	var digits strings.Builder
	for _, point := range value {
		if digit, found := textcompat.DecimalDigit(point); found {
			digits.WriteByte('0' + byte(digit))
		} else {
			digits.WriteRune(point)
		}
	}
	number, err := strconv.ParseFloat(digits.String(), 64)
	if err != nil {
		return 0, "", ErrUnsupported
	}
	switch textcompat.Fold(scale) {
	case "":
	case "k":
		number *= 1000
	case "m":
		number *= 1000000
	default:
		return 0, "", ErrUnsupported
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, "", ErrUnsupported
	}
	if math.Trunc(number) == number {
		integer, _ := new(big.Float).SetFloat64(number).Int(nil)
		return number, json.Number(integer.String()), nil
	}
	return number, json.Number(strconv.FormatFloat(number, 'g', -1, 64)), nil
}

func period(value string) string {
	value = strings.ReplaceAll(textcompat.Fold(value), " ", "")
	for _, unit := range []struct {
		name     string
		suffixes []string
	}{
		{"hour", []string{"/hr", "/hour", "perhr", "perhour"}}, {"day", []string{"/day", "perday"}},
		{"week", []string{"/week", "perweek"}}, {"month", []string{"/month", "/mo", "permonth", "permo"}},
		{"year", []string{"/yr", "/year", "peryr", "peryear"}},
	} {
		for _, suffix := range unit.suffixes {
			if strings.HasSuffix(value, suffix) {
				return unit.name
			}
		}
	}
	return "unspecified"
}

func compensation(value string) (Compensation, []int, error) {
	matched := matching(factText(value, 4000))
	indices := compensationPattern.FindStringSubmatchIndex(matched.folded)
	if indices == nil {
		return emptyCompensation(), nil, nil
	}
	firstCurrency, secondCurrency := matched.text(indices, 1), matched.text(indices, 4)
	if secondCurrency == "" {
		secondCurrency = firstCurrency
	}
	firstScale, secondScale := matched.text(indices, 3), matched.text(indices, 6)
	if textcompat.Fold(firstCurrency) != textcompat.Fold(secondCurrency) || firstScale != "" && secondScale != "" && textcompat.Fold(firstScale) != textcompat.Fold(secondScale) {
		return emptyCompensation(), nil, nil
	}
	if firstScale == "" {
		firstScale = secondScale
	}
	if secondScale == "" {
		secondScale = firstScale
	}
	minFloat, minimum, err := amount(matched.text(indices, 2), firstScale)
	if err != nil {
		return Compensation{}, nil, err
	}
	maxFloat, maximum, err := amount(matched.text(indices, 5), secondScale)
	if err != nil {
		return Compensation{}, nil, err
	}
	if maxFloat < minFloat {
		return emptyCompensation(), nil, nil
	}
	display := factText(matched.text(indices, 0), 500)
	entry := CompensationRange{minimum, maximum, textcompat.Upper(firstCurrency), period(matched.text(indices, 7)), "", display, []FactEvidence{{1, display, "email_compensation_range"}}}
	return Compensation{"explicit", []CompensationRange{entry}}, []int{matched.boundaries[indices[0]], matched.boundaries[indices[1]]}, nil
}

func stripMarkers(value string) string {
	value = strings.Trim(value, " -|,;·•")
	for range 16 {
		before := value
		for index, pattern := range trailingMarkers {
			matched := matching(value)
			// This source pattern explicitly uses ASCII [0-9], not Unicode \d.
			if index == 4 {
				matched = matchingASCIIOnlyDigits(value)
			}
			if span := pattern.FindStringIndex(matched.folded); span != nil {
				value = value[:matched.boundaries[span[0]]] + value[matched.boundaries[span[1]]:]
			}
			value = strings.Trim(value, " -|,;·•")
		}
		if value == before {
			break
		}
	}
	return value
}

func matchingASCIIOnlyDigits(value string) matchText {
	// Keep scalar offsets while masking only non-ASCII decimal digits, which
	// the source's explicit ASCII class must not recognize.
	m := matching(value)
	bytes := []byte(m.folded)
	for i := range bytes {
		if bytes[i] >= '0' && bytes[i] <= '9' && m.raw[m.boundaries[i]] >= 0x80 {
			bytes[i] = '#'
		}
	}
	m.folded = string(bytes)
	return m
}

func workplace(value string) (string, []int) {
	matched := matching(value)
	all := workplacePattern.FindAllStringSubmatchIndex(matched.folded, -1)
	if len(all) == 0 {
		return "", nil
	}
	last := all[len(all)-1]
	mode := strings.ReplaceAll(textcompat.Fold(matched.text(last, 1)), "-", " ")
	if strings.ReplaceAll(mode, " ", "") == "onsite" {
		mode = "onsite"
	}
	return mode, []int{matched.boundaries[last[0]], matched.boundaries[last[1]]}
}

func location(value string) (string, string, Compensation, error) {
	raw := stripMarkers(factText(value, 500))
	pay, span, err := compensation(raw)
	if err != nil {
		return "", "", Compensation{}, err
	}
	if span != nil {
		raw = stripMarkers(raw[:span[0]] + " " + raw[span[1]:])
	}
	mode, span := workplace(raw)
	if span != nil {
		raw = stripMarkers(raw[:span[0]] + " " + raw[span[1]:])
	}
	return factText(raw, 300), mode, pay, nil
}

func employment(value string) string {
	for _, part := range strings.Split(value, "•") {
		part = factText(part, 500)
		if part == "" {
			continue
		}
		for _, item := range employmentPatterns {
			if item.pattern.MatchString(matching(part).folded) {
				return item.key
			}
		}
	}
	return ""
}
func employmentLabel(key string) string {
	for _, item := range employmentPatterns {
		if key == item.key {
			return item.label
		}
	}
	return ""
}

func locationCandidate(provider, title, place, source string) (string, error) {
	if place != "" {
		return place, nil
	}
	corpus := source
	if corpus == "" {
		corpus = title
	}
	if provider == "linkedin" || strings.Contains(corpus, "·") {
		chosen := ""
		for _, value := range []string{source, title} {
			if strings.Contains(value, "·") && utf8.RuneCountInString(value) > utf8.RuneCountInString(chosen) {
				chosen = value
			}
		}
		if chosen != "" {
			return chosen[strings.LastIndex(chosen, "·")+len("·"):], nil
		}
	}
	if provider == "handshake" || strings.Contains(corpus, "•") {
		parts := []string{}
		for _, part := range strings.Split(corpus, "•") {
			if part = factText(part, 500); part != "" {
				parts = append(parts, part)
			}
		}
		for i := len(parts) - 1; i >= 1; i-- {
			if employment(parts[i]) != "" {
				continue
			}
			candidate, _, _, err := location(parts[i])
			if err != nil {
				return "", err
			}
			for _, point := range candidate {
				if textcompat.IsAlpha(point) {
					return parts[i], nil
				}
			}
		}
	}
	return "", nil
}

func ProjectEmailFacts(input FactInput) (Facts, error) {
	if !validTexts(input.Provider, input.Title, input.Company, input.Location, input.SourceText) {
		return Facts{}, ErrUnsupported
	}
	provider := textcompat.Fold(factText(input.Provider, 100))
	title, place, source := factText(input.Title, 500), factText(input.Location, 500), factText(input.SourceText, 4000)
	corpus := source
	if corpus == "" {
		parts := []string{}
		for _, part := range []string{title, factText(input.Company, 500), place} {
			if part != "" {
				parts = append(parts, part)
			}
		}
		corpus = strings.Join(parts, " · ")
	}
	candidate, err := locationCandidate(provider, title, place, source)
	if err != nil {
		return Facts{}, err
	}
	projected, mode, locationPay, err := location(candidate)
	if err != nil {
		return Facts{}, err
	}
	if mode == "" {
		mode, _ = workplace(factText(corpus, 4000))
	}
	pay, _, err := compensation(corpus)
	if err != nil {
		return Facts{}, err
	}
	if pay.Status != "explicit" {
		pay = locationPay
	}
	jobType := employment(corpus)
	return Facts{1, projected, mode, jobType, employmentLabel(jobType), pay}, nil
}

// Only the small consumed fields are projected. Unknown metadata keys are never
// copied into the public card. Nonscalar consumed text is an explicit typed
// migration refusal, not fmt.Sprint's incompatible arbitrary-object rendering.
func storedText(raw any, maximum int) (string, error) {
	var text string
	switch value := raw.(type) {
	case nil:
		return "", nil
	case string:
		text = value
	case bool:
		if value {
			text = "True"
		}
	case json.Number:
		if strings.ContainsAny(string(value), ".eE") {
			number, err := strconv.ParseFloat(string(value), 64)
			if err != nil {
				return "", ErrUnsupported
			}
			return storedText(number, maximum)
		}
		integer, ok := new(big.Int).SetString(string(value), 10)
		if !ok {
			return "", ErrUnsupported
		}
		if integer.Sign() != 0 {
			text = integer.String()
		}
	case float64:
		if math.IsInf(value, 0) || math.IsNaN(value) {
			return "", ErrUnsupported
		}
		if value != 0 {
			text = strconv.FormatFloat(value, 'g', -1, 64)
			if magnitude := math.Abs(value); magnitude >= 1e-4 && magnitude < 1e16 {
				text = strconv.FormatFloat(value, 'f', -1, 64)
			}
			if !strings.ContainsAny(text, ".eE") {
				text += ".0"
			}
		}
	case []any:
		if len(value) != 0 {
			return "", ErrUnsupported
		}
	case map[string]any:
		if len(value) != 0 {
			return "", ErrUnsupported
		}
	default:
		return "", ErrUnsupported
	}
	if !validTexts(text) {
		return "", ErrUnsupported
	}
	return factText(text, maximum), nil
}

func NormalizeStoredFacts(stored map[string]any) (Facts, error) {
	place, err := storedText(stored["location"], 300)
	if err != nil {
		return Facts{}, err
	}
	mode, err := storedText(stored["workplace_type"], 30)
	if err != nil {
		return Facts{}, err
	}
	mode = textcompat.Fold(mode)
	if mode != "remote" && mode != "hybrid" && mode != "onsite" {
		mode = ""
	}
	jobType, err := storedText(stored["employment_type"], 30)
	if err != nil {
		return Facts{}, err
	}
	jobType = textcompat.Fold(jobType)
	if employmentLabel(jobType) == "" {
		jobType = ""
	}
	pay := emptyCompensation()
	if raw, ok := stored["compensation"].(map[string]any); ok {
		if ranges, ok := raw["ranges"].([]any); ok && len(ranges) > 0 {
			if first, ok := ranges[0].(map[string]any); ok {
				display, err := storedText(first["display_text"], 4000)
				if err != nil {
					return Facts{}, err
				}
				pay, _, err = compensation(display)
				if err != nil {
					return Facts{}, err
				}
			}
		}
	}
	return Facts{1, place, mode, jobType, employmentLabel(jobType), pay}, nil
}

func MergeFacts(stored map[string]any, projected Facts) (Facts, error) {
	safe, err := NormalizeStoredFacts(stored)
	if err != nil {
		return Facts{}, err
	}
	if projected.Location == "" {
		projected.Location = safe.Location
	}
	if projected.WorkplaceType == "" {
		projected.WorkplaceType = safe.WorkplaceType
	}
	if projected.EmploymentType == "" {
		projected.EmploymentType = safe.EmploymentType
	}
	projected.EmploymentLabel = employmentLabel(projected.EmploymentType)
	if projected.Compensation.Status != "explicit" {
		projected.Compensation = safe.Compensation
	}
	projected.Version = 1
	return projected, nil
}
