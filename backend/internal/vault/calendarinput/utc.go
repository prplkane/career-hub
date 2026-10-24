package calendarinput

import (
	"time"
	"unicode/utf8"
)

// ISODateUTCSeconds converts the pinned date.fromisoformat contract to midnight
// UTC without changing ValidISODate's permissive basic-date suffix behavior.
func ISODateUTCSeconds(text string) (string, bool) {
	if !ValidISODate(text) {
		return "", false
	}
	year, month, day, ok := isoDate(text, len(text))
	if !ok {
		return "", false
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC).Format("2006-01-02T15:04:05Z"), true
}

// ISODateTimeUTCSeconds mirrors datetime.fromisoformat, interpreting a naive
// datetime as UTC, then emitting isoformat(timespec="seconds") in UTC. Callers
// own stripping, literal Z replacement, input bounds and error diagnostics.
// Fractional seconds are truncated only after timezone conversion. A resulting
// year outside 1..9999 is refused, never normalized into an unsupported year.
func ISODateTimeUTCSeconds(text string) (string, bool) {
	if !utf8.ValidString(text) {
		return "", false
	}
	separator := dateSeparator(text)
	if separator < 0 {
		return "", false
	}
	year, month, day, ok := isoDate(text, separator)
	if !ok {
		return "", false
	}
	var clock [4]int
	offsetMicros := int64(0)
	if len(text) > separator {
		_, width := utf8.DecodeRuneInString(text[separator:])
		part := text[separator+width:]
		valid, _ := isoTimeInfo(part)
		if !valid {
			return "", false
		}
		zone := 0
		for zone < len(part) && part[zone] != '+' && part[zone] != '-' && part[zone] != 'Z' {
			zone++
		}
		clock, _ = parseClock(part, 0, zone)
		if zone < len(part) && part[zone] != 'Z' {
			offset, _ := parseClock(part, zone+1, len(part))
			seconds := int64(offset[0]*3600 + offset[1]*60 + offset[2])
			// CPython selects its UTC singleton for a zero seconds component,
			// even when the textual offset also includes fractional seconds.
			if seconds != 0 {
				offsetMicros = seconds*1e6 + int64(offset[3])
			}
			if part[zone] == '-' {
				offsetMicros = -offsetMicros
			}
		}
	}
	instant := time.Date(year, time.Month(month), day, clock[0], clock[1], clock[2], clock[3]*1000, time.UTC).Add(-time.Duration(offsetMicros) * time.Microsecond)
	if instant.Year() < 1 || instant.Year() > 9999 {
		return "", false
	}
	return instant.Format("2006-01-02T15:04:05Z"), true
}
