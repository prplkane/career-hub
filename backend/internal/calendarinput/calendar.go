// Package calendarinput implements the existing Vault calendar validators.
// Inputs are bounded, already-decoded cataloginput.Values, not editor coercions.
package calendarinput

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

const (
	StringRequired              = "CALENDAR_STRING_REQUIRED"
	ValueRequired               = "CALENDAR_VALUE_REQUIRED"
	DateInvalid                 = "CALENDAR_DATE_INVALID"
	FormatInvalid               = "CALENDAR_FORMAT_INVALID"
	RangeObjectRequired         = "CALENDAR_RANGE_OBJECT_REQUIRED"
	RangeUnknownFields          = "CALENDAR_RANGE_UNKNOWN_FIELDS"
	RangeOngoingBooleanRequired = "CALENDAR_RANGE_ONGOING_BOOLEAN_REQUIRED"
	RangeEndWithOngoing         = "CALENDAR_RANGE_END_WITH_ONGOING"
	RangeEndBeforeStart         = "CALENDAR_RANGE_END_BEFORE_START"
	InputUnsupported            = "CALENDAR_INPUT_UNSUPPORTED"
)

// Error never retains source values or unknown keys. Field is empty, start,
// end, or ongoing. Unknown raw shapes are explicitly unsupported, not a false
// claim of DateNormalizationError parity for Python TypeError/ValueError.
type Error struct{ Code, Field string }

func (*Error) Error() string { return "calendar input could not be validated" }

// Format uses a trusted schema label supplied by the caller, not user content.
// Unknown-key prose is deliberately redacted; all other supported messages
// preserve the source's wording and field choice.
func (e *Error) Format(label string) string {
	if e.Field == "start" || e.Field == "end" || e.Field == "ongoing" {
		label += "." + e.Field
	}
	var suffix string
	switch e.Code {
	case StringRequired:
		suffix = " must be a string"
	case ValueRequired:
		suffix = " is required"
	case DateInvalid:
		suffix = " must contain a valid calendar date"
	case FormatInvalid:
		suffix = " must be a supported date, timestamp, month/year, or Month YYYY"
	case RangeObjectRequired:
		suffix = " must be an object"
	case RangeUnknownFields:
		suffix = " has unknown fields"
	case RangeOngoingBooleanRequired:
		suffix = " must be a boolean"
	case RangeEndWithOngoing:
		suffix = " must be empty when ongoing is true"
	case RangeEndBeforeStart:
		suffix = " must not be earlier than " + strings.TrimSuffix(label, ".end") + ".start"
	default:
		return "calendar input could not be validated"
	}
	return label + suffix
}

type Range struct {
	Start   string  `json:"start"`
	End     *string `json:"end"`
	Ongoing bool    `json:"ongoing"`
}

func failure(code, field string) *Error { return &Error{Code: code, Field: field} }

func pattern(source string) *regexp.Regexp {
	// All Python whitespace has already been collapsed to ASCII space. Nd is
	// pinned separately instead of following the compiler's newer Unicode data.
	source = strings.ReplaceAll(source, `\d`, decimalPattern)
	source = strings.ReplaceAll(source, `\s`, " ")
	return regexp.MustCompile(source)
}

var (
	monthPattern     = pattern(`^((?:19|20)\d{2})-(0[1-9]|1[0-2])$`)
	datePattern      = pattern(`^((?:19|20)\d{2})-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$`)
	yearPattern      = pattern(`^((?:19|20)\d{2})$`)
	namePattern      = pattern(`^([A-Za-z]+)\.?(?:\s+|[/-]\s*|,\s*)((?:19|20)\d{2})$`)
	monthYearPattern = pattern(`^(0?[1-9]|1[0-2])[./-]((?:19|20)\d{2})$`)
	dottedPattern    = pattern(`^(0?[1-9]|[12]\d|3[01])\.(0?[1-9]|1[0-2])\.((?:19|20)\d{2})(?:[T\s].*)?$`)
	slashPattern     = pattern(`^(0?[1-9]|1[0-2])/(0?[1-9]|[12]\d|3[01])/((?:19|20)\d{2})(?:[T\s].*)?$`)
	yearFirstPattern = pattern(`^((?:19|20)\d{2})[./](0?[1-9]|1[0-2])[./](0?[1-9]|[12]\d|3[01])(?:[T\s].*)?$`)
	monthDayPattern  = pattern(`^([A-Za-z]+)\.?\s+(\d{1,2}),?\s+((?:19|20)\d{2})$`)
	dayMonthPattern  = pattern(`^(\d{1,2})\s+([A-Za-z]+)\.?[,]?\s+((?:19|20)\d{2})$`)
)

var monthNames = [...]string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}

func namedMonth(name string) int {
	name = strings.ToLower(name) // Pattern admits ASCII letters only.
	for i, candidate := range monthNames {
		if name == candidate || name == candidate[:3] {
			return i + 1
		}
	}
	return 0
}

// NormalizeMonth preserves the source's branch order and raw Unicode year in
// the direct month/year/named-month shortcuts. It never mutates its input.
func NormalizeMonth(value cataloginput.Value) (string, *Error) { return normalizeMonth(value, "") }

func normalizeMonth(value cataloginput.Value, field string) (string, *Error) {
	if value.Kind != "string" {
		return "", failure(StringRequired, field)
	}
	if !utf8.ValidString(value.Text) {
		return "", failure(InputUnsupported, field)
	}
	text := strings.Join(strings.FieldsFunc(value.Text, textcompat.IsSpace), " ")
	if text == "" {
		return "", failure(ValueRequired, field)
	}
	if monthPattern.MatchString(text) {
		return text, nil
	}
	if m := datePattern.FindStringSubmatch(text); m != nil {
		return calendarMonth(m[1], m[2], m[3], field)
	}
	if m := yearPattern.FindStringSubmatch(text); m != nil {
		return m[1] + "-01", nil
	}
	if m := namePattern.FindStringSubmatch(text); m != nil {
		if month := namedMonth(m[1]); month != 0 {
			return fmt.Sprintf("%s-%02d", m[2], month), nil
		}
	}
	if m := monthYearPattern.FindStringSubmatch(text); m != nil {
		return calendarMonth(m[2], m[1], "1", field)
	}
	if m := dottedPattern.FindStringSubmatch(text); m != nil {
		return calendarMonth(m[3], m[2], m[1], field)
	}
	if m := slashPattern.FindStringSubmatch(text); m != nil {
		return calendarMonth(m[3], m[1], m[2], field)
	}
	if m := yearFirstPattern.FindStringSubmatch(text); m != nil {
		return calendarMonth(m[1], m[2], m[3], field)
	}
	if m := monthDayPattern.FindStringSubmatch(text); m != nil {
		if month := namedMonth(m[1]); month != 0 {
			return calendarMonth(m[3], fmt.Sprint(month), m[2], field)
		}
	}
	if m := dayMonthPattern.FindStringSubmatch(text); m != nil {
		if month := namedMonth(m[2]); month != 0 {
			return calendarMonth(m[3], fmt.Sprint(month), m[1], field)
		}
	}
	year, month, ok := isoDateTime(strings.ReplaceAll(text, "Z", "+00:00"))
	if !ok {
		return "", failure(FormatInvalid, field)
	}
	return fmt.Sprintf("%04d-%02d", year, month), nil
}

func calendarMonth(yearText, monthText, dayText, field string) (string, *Error) {
	year, month, day := decimalNumber(yearText), decimalNumber(monthText), decimalNumber(dayText)
	if !validDate(year, month, day) {
		return "", failure(DateInvalid, field)
	}
	return fmt.Sprintf("%04d-%02d", year, month), nil
}

func NormalizeRange(value cataloginput.Value) (Range, *Error) {
	if value.Kind != "mapping" {
		return Range{}, failure(RangeObjectRequired, "")
	}
	values := map[string]cataloginput.Value{}
	unknown := false
	for _, entry := range value.Entries {
		if entry.Key.Kind != "string" || !utf8.ValidString(entry.Key.Text) {
			return Range{}, failure(InputUnsupported, "")
		}
		key := entry.Key.Text
		if key != "start" && key != "end" && key != "ongoing" {
			unknown = true
		}
		if _, exists := values[key]; exists {
			return Range{}, failure(InputUnsupported, "")
		}
		values[key] = entry.Value
	}
	if unknown {
		return Range{}, failure(RangeUnknownFields, "")
	}
	start, err := normalizeMonth(values["start"], "start")
	if err != nil {
		return Range{}, err
	}
	ongoing := false
	if value, exists := values["ongoing"]; exists {
		if value.Kind != "bool" {
			return Range{}, failure(RangeOngoingBooleanRequired, "ongoing")
		}
		ongoing = value.Boolean
	}
	var end *string
	if value, exists := values["end"]; exists && value.Kind != "null" && !(value.Kind == "string" && value.Text == "") {
		text, err := normalizeMonth(value, "end")
		if err != nil {
			return Range{}, err
		}
		end = &text
	}
	if ongoing && end != nil {
		return Range{}, failure(RangeEndWithOngoing, "end")
	}
	if !ongoing && end != nil && *end < start {
		return Range{}, failure(RangeEndBeforeStart, "end")
	}
	return Range{Start: start, End: end, Ongoing: ongoing}, nil
}
