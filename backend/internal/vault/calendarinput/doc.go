// Package calendarinput implements the Vault calendar validators. Inputs are
// bounded, already-decoded cataloginput.Values, not editor coercions; the
// package reads no files, selects no Data Home, guesses no legacy ranges and
// formats no dates.
//
// NormalizeMonth(Value) (string, *Error) and NormalizeRange(Value) (Range,
// *Error) validate month strings and start/end/ongoing ranges; Range has
// Start, End (*string, owned) and Ongoing with snake-case JSON names. No
// scalar is coerced: a YAML date, number, bytes or boolean is not a calendar
// string. A missing, null or empty end is absent, a whitespace-only end is
// an error, and ongoing must be a bool. Validation order is unknown keys,
// start, ongoing, end, ongoing/end conflict, end-before-start. Whitespace
// collapses with textcompat.IsSpace and decimal digits use the Unicode 15
// ranges.
//
// ValidISODate and ValidAwareISODateTime implement the permissive CPython
// 3.12 ISO parser (see LICENSE.PSF), not RFC 3339: basic and extended
// calendar and week dates, years 1 to 9999, one arbitrary separator, partial
// clock components, comma or point fractions and offset grammar, including
// its known lenient edges. They are compatibility predicates, not
// sanitizers. ValidAwareISODateTime requires a timezone in the time portion;
// date-only and naive timestamps fail, and accepted strings are returned
// unchanged.
//
// Error carries only Code and Field (start, end, ongoing or empty); Format
// takes a trusted schema label, never user text. Codes:
// CALENDAR_STRING_REQUIRED, CALENDAR_VALUE_REQUIRED, CALENDAR_DATE_INVALID,
// CALENDAR_FORMAT_INVALID, CALENDAR_RANGE_OBJECT_REQUIRED,
// CALENDAR_RANGE_UNKNOWN_FIELDS, CALENDAR_RANGE_ONGOING_BOOLEAN_REQUIRED,
// CALENDAR_RANGE_END_WITH_ONGOING, CALENDAR_RANGE_END_BEFORE_START and
// CALENDAR_INPUT_UNSUPPORTED for non-string keys or unrepresentable input.
// Scans are bounded by input size; no narrower string limit is added.
package calendarinput
