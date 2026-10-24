package calendarinput

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

// Fields contains the visible text and its optional derived calendar range.
// A nil DateRange means there is no derived metadata, not "keep the old range".
type Fields struct {
	Dates     string `json:"dates"`
	DateRange *Range `json:"date_range"`
}

// FieldError adds the closed editor field label to a calendar failure. Neither
// the printable error nor Cause retains the submitted dates or unknown keys.
type FieldError struct {
	Field string
	Cause *Error
}

func (*FieldError) Error() string     { return "calendar fields could not be validated" }
func (e *FieldError) Message() string { return e.Cause.Format(e.Field) }

// Value returns a new ordered, owned value for canonical Vault serialization.
func (r Range) Value() cataloginput.Value {
	value := cataloginput.Value{Kind: "mapping"}
	add := func(key string, child cataloginput.Value) {
		value.Entries = append(value.Entries, cataloginput.Entry{
			Key: cataloginput.Value{Kind: "string", Text: key}, Value: child,
		})
	}
	add("start", cataloginput.Value{Kind: "string", Text: r.Start})
	end := cataloginput.Value{Kind: "null"}
	if r.End != nil {
		end = cataloginput.Value{Kind: "string", Text: *r.End}
	}
	add("end", end)
	add("ongoing", cataloginput.Value{Kind: "bool", Boolean: r.Ongoing})
	return value
}

func absent(value cataloginput.Value) bool {
	return value.Kind == "" || value.Kind == "null" || (value.Kind == "string" && value.Text == "")
}

// NormalizeRangeFields preserves the source's dates-before-range error order.
// Values come from a bounded decoder. A zero Value denotes an absent field.
// Unrecognized legacy wording remains intact; structured input controls the
// visible label when supplied. The caller must replace, not merge, DateRange.
func NormalizeRangeFields(dates, rawRange cataloginput.Value, required bool) (Fields, *FieldError) {
	text := ""
	if !absent(dates) {
		if dates.Kind != "string" {
			return Fields{}, &FieldError{"dates", failure(StringRequired, "")}
		}
		if !utf8.ValidString(dates.Text) {
			return Fields{}, &FieldError{"dates", failure(InputUnsupported, "")}
		}
		text = strings.Join(strings.FieldsFunc(dates.Text, textcompat.IsSpace), " ")
	}
	if !absent(rawRange) {
		normalized, err := NormalizeRange(rawRange)
		if err != nil {
			return Fields{}, &FieldError{"date_range", err}
		}
		display, displayError := formatRange(normalized)
		if displayError != nil {
			return Fields{}, displayError
		}
		return Fields{display, &normalized}, nil
	}
	if text != "" {
		return Fields{text, parseLegacyRange(text)}, nil
	}
	if required {
		return Fields{}, &FieldError{"dates", failure(ValueRequired, "")}
	}
	return Fields{}, nil
}

var rangeSeparator = pattern(`(?i)\s+(?:-|–|—|to)\s+|\s*(?:–|—)\s*`)
var standalonePattern = pattern(`^(0?[1-9]|1[0-2]|[A-Za-z]+)\.?$`)

func standaloneMonth(text string) int {
	match := standalonePattern.FindStringSubmatch(textcompat.Strip(text))
	if match == nil {
		return 0
	}
	if number := decimalNumber(match[1]); number >= 1 && number <= 12 {
		return number
	}
	return namedMonth(match[1])
}

// parseLegacyRange is best effort, not a second validation of derived data.
// In particular anchored months can cross 1900/2099 or even 1/9999; full Vault
// validation decides whether that derived range is admissible.
func parseLegacyRange(text string) *Range {
	parts := rangeSeparator.Split(text, 2)
	startText := textcompat.Strip(parts[0])
	start, _ := NormalizeMonth(cataloginput.Value{Kind: "string", Text: startText})
	if len(parts) == 1 {
		if start == "" {
			return nil
		}
		return &Range{Start: start}
	}
	endText := textcompat.Strip(parts[1])
	switch textcompat.Fold(endText) {
	case "present", "current", "now", "ongoing":
		if start == "" {
			return nil
		}
		return &Range{Start: start, Ongoing: true}
	}
	end, _ := NormalizeMonth(cataloginput.Value{Kind: "string", Text: endText})
	if start == "" && end != "" {
		if month := standaloneMonth(startText); month != 0 {
			year, endMonth := monthParts(end)
			if month > endMonth {
				year--
			}
			start = fmt.Sprintf("%04d-%02d", year, month)
		}
	} else if end == "" && start != "" {
		if month := standaloneMonth(endText); month != 0 {
			year, startMonth := monthParts(start)
			if month < startMonth {
				year++
			}
			end = fmt.Sprintf("%04d-%02d", year, month)
		}
	}
	if start == "" || end == "" || end < start {
		return nil
	}
	return &Range{Start: start, End: &end}
}

func monthParts(text string) (int, int) {
	parts := strings.Split(text, "-")
	return decimalNumber(parts[0]), decimalNumber(parts[1])
}

func formatMonth(text string) (string, *Error) {
	normalized, err := NormalizeMonth(cataloginput.Value{Kind: "string", Text: text})
	if err != nil {
		return "", err
	}
	year, month := monthParts(normalized)
	name := monthNames[month-1][:3]
	return fmt.Sprintf("%s%s %04d", strings.ToUpper(name[:1]), name[1:], year), nil
}

func formatRange(value Range) (string, *FieldError) {
	// Source format_date_range revalidates the range and each displayed month.
	// Do not remove this apparently redundant normalization: an ISO date outside
	// 1900..2099 can normalize to a month which the next call rejects.
	normalized, err := NormalizeRange(value.Value())
	if err != nil {
		return "", &FieldError{"date_range", err}
	}
	start, err := formatMonth(normalized.Start)
	if err != nil {
		return "", &FieldError{"date", err}
	}
	if normalized.Ongoing {
		return start + " - Present", nil
	}
	if normalized.End != nil {
		end, err := formatMonth(*normalized.End)
		if err != nil {
			return "", &FieldError{"date", err}
		}
		return start + " - " + end, nil
	}
	return start, nil
}
