package calendarinput

// The ISO parser preserves CPython 3.12.14 _datetimemodule.c's separator,
// component and terminal-byte decisions, including its permissive edge cases.
// Adapted to bounded Go string indexing and bool results; no Python object or
// timezone construction is retained. See LICENSE.PSF and README.md.

import (
	"time"
	"unicode/utf8"
)

func asciiDigit(b byte) bool { return b >= '0' && b <= '9' }

// CPython parses UTF-8 bytes and a terminating NUL. Reading beyond the Go
// string is rejected by digits; at() only supplies the terminal sentinel.
func at(text string, i int) byte {
	if i < 0 || i >= len(text) {
		return 0
	}
	return text[i]
}

func digits(text string, position, length int) (int, bool) {
	if position < 0 || length < 0 || position > len(text)-length {
		return 0, false
	}
	value := 0
	for _, b := range []byte(text[position : position+length]) {
		if !asciiDigit(b) {
			return 0, false
		}
		value = value*10 + int(b-'0')
	}
	return value, true
}

func validDate(year, month, day int) bool {
	if year < 1 || year > 9999 || month < 1 || month > 12 || day < 1 {
		return false
	}
	days := [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if month == 2 && year%4 == 0 && (year%100 != 0 || year%400 == 0) {
		return day <= 29
	}
	return day <= days[month-1]
}

func isoWeek(year, week, day int) (int, int, int, bool) {
	if year < 1 || year > 9999 || week < 1 || week > 53 || day < 1 || day > 7 {
		return 0, 0, 0, false
	}
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	monday := jan4.AddDate(0, 0, -(int(jan4.Weekday())+6)%7)
	date := monday.AddDate(0, 0, (week-1)*7+day-1)
	y, w := date.ISOWeek()
	if y != year || w != week || date.Year() < 1 || date.Year() > 9999 {
		return 0, 0, 0, false
	}
	return date.Year(), int(date.Month()), date.Day(), true
}

// length is the source parser's nominal date length. Its component parser can
// ignore remaining basic-date suffix bytes; ValidISODate preserves that quirk.
func isoDate(text string, length int) (int, int, int, bool) {
	year, ok := digits(text, 0, 4)
	if !ok {
		return 0, 0, 0, false
	}
	position, separated := 4, at(text, 4) == '-'
	if separated {
		position++
	}
	if at(text, position) == 'W' {
		position++
		week, ok := digits(text, position, 2)
		if !ok {
			return 0, 0, 0, false
		}
		position += 2
		day := 1
		if position < length {
			if separated {
				if at(text, position) != '-' {
					return 0, 0, 0, false
				}
				position++
			}
			day, ok = digits(text, position, 1)
			if !ok {
				return 0, 0, 0, false
			}
		}
		return isoWeek(year, week, day)
	}
	month, ok := digits(text, position, 2)
	if !ok {
		return 0, 0, 0, false
	}
	position += 2
	if separated {
		if at(text, position) != '-' {
			return 0, 0, 0, false
		}
		position++
	}
	day, ok := digits(text, position, 2)
	return year, month, day, ok && validDate(year, month, day)
}

// ValidISODate mirrors datetime.date.fromisoformat, without strip/cleanup.
// Scalar UTF-8 is the shared input precondition. Only byte lengths 7/8/10 enter
// the source C date parser; this is not equivalent to time.Parse("2006-01-02").
func ValidISODate(text string) bool {
	if !utf8.ValidString(text) || (len(text) != 7 && len(text) != 8 && len(text) != 10) {
		return false
	}
	_, _, _, ok := isoDate(text, len(text))
	return ok
}

func dateSeparator(text string) int {
	length := len(text)
	if length == 7 {
		return 7
	}
	if length < 7 {
		return -1
	}
	if text[4] == '-' {
		if text[5] != 'W' {
			return 10
		}
		if length < 8 {
			return -1
		}
		if length > 8 && text[8] == '-' {
			if length == 9 {
				return -1
			}
			if length > 10 && asciiDigit(text[10]) {
				return 8
			}
			return 10
		}
		return 8
	}
	if text[4] != 'W' {
		return 8
	}
	index := 7
	for index < length && asciiDigit(text[index]) {
		index++
	}
	if index < 9 {
		return index
	}
	if index%2 == 0 {
		return 7
	}
	return 8
}

// parseClock returns source rv: -1 invalid, 0 terminated, 1 residual bytes.
// The nominal end may be the timezone sign rather than len(text).
func parseClock(text string, start, end int) ([4]int, int) {
	var values [4]int
	position, separated := start, true
	for i := 0; i < 3; i++ {
		n, ok := digits(text, position, 2)
		if !ok {
			return values, -1
		}
		values[i] = n
		position += 2
		c := at(text, position)
		position++
		if i == 0 {
			separated = c == ':'
		}
		if position >= end {
			if c != 0 {
				return values, 1
			}
			return values, 0
		}
		if separated && c == ':' {
			continue
		}
		if c == '.' || c == ',' {
			break
		}
		if !separated {
			position--
		} else {
			return values, -1
		}
	}
	count := min(end-position, 6)
	if count < 1 {
		return values, -1
	} // no fractional digits is invalid
	n, ok := digits(text, position, count)
	if !ok {
		return values, -1
	}
	position += count
	for i := count; i < 6; i++ {
		n *= 10
	}
	values[3] = n
	for asciiDigit(at(text, position)) {
		position++
	}
	if at(text, position) != 0 {
		return values, 1
	}
	return values, 0
}

func isoTime(text string) bool {
	valid, _ := isoTimeInfo(text)
	return valid
}

// Awareness is determined only within the parsed time portion, never from a
// date hyphen or the arbitrary date/time separator.
func isoTimeInfo(text string) (valid, aware bool) {
	zone := 0
	for zone < len(text) && text[zone] != '+' && text[zone] != '-' && text[zone] != 'Z' {
		zone++
	}
	clock, rv := parseClock(text, 0, zone)
	if rv < 0 || clock[0] > 23 || clock[1] > 59 || clock[2] > 59 {
		return false, false
	}
	if zone == len(text) {
		return rv == 0, false
	}
	if text[zone] == 'Z' {
		return at(text, zone+1) == 0, true
	}
	offset, rv := parseClock(text, zone+1, len(text))
	if rv != 0 {
		return false, false
	}
	// CPython normalizes offset minutes/seconds, unlike clock components.
	seconds := offset[0]*3600 + offset[1]*60 + offset[2]
	return seconds < 86400, true
}

// ValidAwareISODateTime mirrors datetime.fromisoformat(text).tzinfo is not
// None for scalar UTF-8. It neither strips nor converts timezone/date values.
// This source-compatibility predicate is not a strict RFC3339 sanitizer.
// The document ledger caller separately enforces its 64-codepoint bound.
func ValidAwareISODateTime(text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	separator := dateSeparator(text)
	if separator < 0 || separator >= len(text) {
		return false
	}
	if _, _, _, ok := isoDate(text, separator); !ok {
		return false
	}
	_, size := utf8.DecodeRuneInString(text[separator:])
	valid, aware := isoTimeInfo(text[separator+size:])
	return valid && aware
}

func isoDateTime(text string) (int, int, bool) {
	separator := dateSeparator(text)
	if separator < 0 {
		return 0, 0, false
	}
	year, month, _, ok := isoDate(text, separator)
	if !ok {
		return 0, 0, false
	}
	if len(text) > separator {
		_, size := utf8.DecodeRuneInString(text[separator:])
		if !isoTime(text[separator+size:]) {
			return 0, 0, false
		}
	}
	return year, month, true
}
