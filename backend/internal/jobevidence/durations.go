package jobevidence

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/calendarinput"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

var monthSpellings = []string{"january", "jan", "february", "feb", "march", "mar", "april", "apr", "may", "june", "jun", "july", "jul", "august", "aug", "september", "sept", "sep", "october", "oct", "november", "nov", "december", "dec"}
var monthAbbreviations = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}

func validToday(today time.Time) bool { return today.Year() >= 1 && today.Year() <= 9999 }
func monthCount(startYear, startMonth, endYear, endMonth int) *float64 {
	count := float64(max((endYear-startYear)*12+endMonth-startMonth+1, 0))
	return &count
}

// equalDateWord is scoped to the source's ASCII month/status literals under
// Python IGNORECASE. Full casefold is not a regex replacement (e.g. dotted I).
func equalDateWord(input []rune, start int, word string) bool {
	if start+len(word) > len(input) {
		return false
	}
	for i := range word {
		point := input[start+i]
		if point >= 'A' && point <= 'Z' {
			point += 'a' - 'A'
		}
		switch point {
		case '\u0130', '\u0131':
			point = 'i'
		case '\u017f':
			point = 's'
		case '\u212a':
			point = 'k'
		}
		if point != rune(word[i]) {
			return false
		}
	}
	return true
}
func dateYear(input []rune, start int) (int, bool) {
	if start+4 > len(input) || !(input[start] == '1' && input[start+1] == '9' || input[start] == '2' && input[start+1] == '0') {
		return 0, false
	}
	a, first := textcompat.DecimalDigit(input[start+2])
	b, second := textcompat.DecimalDigit(input[start+3])
	if !first || !second {
		return 0, false
	}
	return int(input[start]-'0')*1000 + int(input[start+1]-'0')*100 + a*10 + b, true
}
func skipDateSpace(input []rune, position int) int {
	for position < len(input) && textcompat.IsSpace(input[position]) {
		position++
	}
	return position
}
func asciiLetter(point rune) bool {
	return point >= 'A' && point <= 'Z' || point >= 'a' && point <= 'z'
}

func datePart(input []rune, start, defaultMonth int) (end, year, month int, ok bool) {
	for _, word := range monthSpellings {
		if !equalDateWord(input, start, word) {
			continue
		}
		wordEnd := start + len(word)
		position := skipDateSpace(input, wordEnd)
		if position == wordEnd {
			continue
		}
		if year, found := dateYear(input, position); found {
			// The outer range regex is IGNORECASE; the source _date_part's
			// inner [A-Za-z]+ is not. Only the final ASCII letter suffix can
			// become its month capture after a special Unicode I/long-s.
			suffix := wordEnd
			for suffix > start && asciiLetter(input[suffix-1]) {
				suffix--
			}
			name := strings.ToLower(string(input[suffix:wordEnd]))
			month := defaultMonth
			if len(name) >= 3 {
				for i, candidate := range monthAbbreviations {
					if name[:3] == candidate {
						month = i + 1
						break
					}
				}
			}
			return position + 4, year, month, true
		}
	}
	if year, found := dateYear(input, start); found {
		return start + 4, year, defaultMonth, true
	}
	return 0, 0, 0, false
}

// DateRangeMonths matches the first legacy range, including Python's permissive
// substring/partial-year behavior. It does not parse durations from unrelated
// prose or invent dates. Explicit today makes replay and cache-month use stable.
func DateRangeMonths(ctx context.Context, text string, today time.Time) (*float64, *Error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	if !validToday(today) {
		return nil, &Error{Code: "EVIDENCE_MONTH_INVALID"}
	}
	if len(text) > MaxTextBytes {
		return nil, &Error{Code: "EVIDENCE_TEXT_LIMIT"}
	}
	if !utf8.ValidString(text) {
		return nil, &Error{Code: "EVIDENCE_TEXT_INVALID"}
	}
	input := []rune(text)
	for i := range input {
		if i%1024 == 0 {
			if err := stopped(ctx); err != nil {
				return nil, err
			}
		}
		position, startYear, startMonth, found := datePart(input, i, 1)
		if !found {
			continue
		}
		position = skipDateSpace(input, position)
		if position == len(input) {
			continue
		}
		if input[position] == '-' || input[position] == '–' || input[position] == '—' {
			position++
		} else if equalDateWord(input, position, "to") {
			position += 2
		} else {
			continue
		}
		position = skipDateSpace(input, position)
		_, endYear, endMonth, found := datePart(input, position, 12)
		if !found {
			for _, word := range []string{"present", "current", "now"} {
				if equalDateWord(input, position, word) {
					endYear, endMonth, found = today.Year(), int(today.Month()), true
					break
				}
			}
		}
		if found {
			if err := stopped(ctx); err != nil {
				return nil, err
			}
			return monthCount(startYear, startMonth, endYear, endMonth), nil
		}
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	return nil, nil
}

func decimalMonth(text string) (year, month int) {
	parts := strings.Split(text, "-") // Only called on normalized calendar output.
	for _, point := range parts[0] {
		digit, _ := textcompat.DecimalDigit(point)
		year = year*10 + digit
	}
	for _, point := range parts[1] {
		digit, _ := textcompat.DecimalDigit(point)
		month = month*10 + digit
	}
	return
}
func structuredMonths(value cataloginput.Value, today time.Time) (*float64, *Error) {
	if value.Kind != "mapping" {
		return nil, nil
	}
	// NormalizeRange only observes these flat fields; bound them before calling
	// the shared calendar adapter rather than recursively materializing YAML.
	if len(value.Entries) > 64 {
		return nil, &Error{Code: "EVIDENCE_INPUT_LIMIT"}
	}
	for _, entry := range value.Entries {
		if len(entry.Key.Text) > 16384 || len(entry.Value.Text) > 16384 {
			return nil, &Error{Code: "EVIDENCE_TEXT_LIMIT"}
		}
	}
	normalized, err := calendarinput.NormalizeRange(value)
	if err != nil {
		if err.Code == calendarinput.InputUnsupported {
			return nil, &Error{Code: "EVIDENCE_DATES_UNSUPPORTED"}
		}
		return nil, nil // Source DateNormalizationError allows the legacy fallback.
	}
	startYear, startMonth := decimalMonth(normalized.Start)
	if normalized.Ongoing {
		return monthCount(startYear, startMonth, today.Year(), int(today.Month())), nil
	}
	if normalized.End == nil {
		return nil, nil
	}
	endYear, endMonth := decimalMonth(*normalized.End)
	return monthCount(startYear, startMonth, endYear, endMonth), nil
}

func ExperienceDurations(ctx context.Context, experience cataloginput.Value, today time.Time) (map[string]float64, *Error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	if !validToday(today) {
		return nil, &Error{Code: "EVIDENCE_MONTH_INVALID"}
	}
	out := map[string]float64{}
	if experience.Kind != "sequence" {
		return out, nil
	}
	if len(experience.Items) > MaxSources {
		return nil, &Error{Code: "EVIDENCE_INPUT_LIMIT"}
	}
	bytes := 0
	for _, item := range experience.Items {
		if err := stopped(ctx); err != nil {
			return nil, err
		}
		if !active(item) {
			continue
		}
		value, _ := field(item, "date_range")
		if len(item.Entries) > MaxSources || len(value.Entries) > 64 {
			return nil, &Error{Code: "EVIDENCE_INPUT_LIMIT"}
		}
		for _, entry := range value.Entries {
			bytes += len(entry.Key.Text) + len(entry.Value.Text)
		}
		if bytes > 32<<20 {
			return nil, &Error{Code: "EVIDENCE_INPUT_LIMIT"}
		}
		months, err := structuredMonths(value, today)
		if err != nil {
			return nil, err
		}
		if months == nil {
			value, _ = field(item, "dates")
			if value.Kind != "string" {
				prepared := cataloginput.PrepareCatalog(value, false)
				if prepared.Kind != "sequence" || len(prepared.Items) != 0 {
					return nil, &Error{Code: "EVIDENCE_DATES_UNSUPPORTED"}
				}
				value.Text = ""
			}
			bytes += len(value.Text)
			if bytes > 32<<20 {
				return nil, &Error{Code: "EVIDENCE_INPUT_LIMIT"}
			}
			months, err = DateRangeMonths(ctx, value.Text, today)
			if err != nil {
				return nil, err
			}
		}
		id, _ := field(item, "id")
		if months != nil && id.Kind == "string" {
			if !utf8.ValidString(id.Text) || len(id.Text) > 1024 {
				return nil, &Error{Code: "EVIDENCE_ID_UNSUPPORTED"}
			}
			out[id.Text] = *months
		}
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
