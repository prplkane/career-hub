// Package isotime preserves the selected Python ISO calendar/week/clock grammar.
// This is a bounded parsing kernel, not an API policy: callers decide whether
// timezone-less values are permitted and how to classify invalid input.
package isotime

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid = errors.New("invalid ISO timestamp")
	ErrRange   = errors.New("ISO timestamp outside supported range")
	ErrLimit   = errors.New("ISO timestamp outside representation bounds")
)

// Parse receives a trimmed value with any source-supported Z token expansion
// already applied. dateOnly retains application_history's ten-character branch.
// It preserves microseconds and whether an explicit timezone was supplied.
func Parse(text string, dateOnly bool) (time.Time, bool, error) {
	if len(text) > 256 || !utf8.ValidString(text) {
		return time.Time{}, false, ErrLimit
	}
	date, end, ok := isoDate(text)
	if !ok || dateOnly && end != len(text) {
		return time.Time{}, false, ErrInvalid
	}
	if end == len(text) {
		return date, false, nil
	}
	_, width := utf8.DecodeRuneInString(text[end:])
	clock, zone := text[end+width:], ""
	if index := strings.IndexAny(clock, "+-"); index >= 0 {
		clock, zone = clock[:index], clock[index:]
	}
	h, m, s, micro, ok := isoClock(clock)
	if !ok || h > 23 || m > 59 || s > 59 {
		return time.Time{}, false, ErrInvalid
	}
	stamp := date.Add(time.Duration(h*3600+m*60+s)*time.Second + time.Duration(micro)*time.Microsecond)
	if zone != "" {
		zh, zm, zs, zmicro, ok := isoClock(zone[1:])
		seconds := zh*3600 + zm*60 + zs
		if !ok || seconds >= 86400 {
			return time.Time{}, false, ErrInvalid
		}
		// CPython's fractional zero-second offset resolves to UTC; overflowing
		// minute/second components below a total of 24 hours remain accepted.
		if seconds != 0 {
			delta := time.Duration(seconds)*time.Second + time.Duration(zmicro)*time.Microsecond
			if zone[0] == '-' {
				delta = -delta
			}
			stamp = stamp.Add(-delta)
		}
	}
	if stamp.Year() < 1 || stamp.Year() > 9999 {
		return time.Time{}, false, ErrRange
	}
	return stamp, zone != "", nil
}

func digits(text string, start, count int) (int, bool) {
	if start < 0 || start+count > len(text) {
		return 0, false
	}
	value := 0
	for _, b := range []byte(text[start : start+count]) {
		if b < '0' || b > '9' {
			return 0, false
		}
		value = value*10 + int(b-'0')
	}
	return value, true
}
func isoDate(text string) (time.Time, int, bool) {
	year, ok := digits(text, 0, 4)
	if !ok || year < 1 {
		return time.Time{}, 0, false
	}
	at := 4
	extended := at < len(text) && text[at] == '-'
	if extended {
		at++
	}
	if at < len(text) && text[at] == 'W' {
		week, ok := digits(text, at+1, 2)
		if !ok || week < 1 || week > 53 {
			return time.Time{}, 0, false
		}
		at += 3
		day := 1
		if extended && at < len(text) && text[at] == '-' {
			at++
			day, ok = digits(text, at, 1)
			at++
			if !ok {
				return time.Time{}, 0, false
			}
		} else if !extended && at < len(text) && text[at] >= '0' && text[at] <= '9' {
			day = int(text[at] - '0')
			at++
		}
		if day < 1 || day > 7 {
			return time.Time{}, 0, false
		}
		jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
		date := jan4.AddDate(0, 0, -(int(jan4.Weekday())+6)%7+(week-1)*7+day-1)
		isoYear, isoWeek := date.ISOWeek()
		if isoYear != year || isoWeek != week || date.Year() < 1 || date.Year() > 9999 {
			return time.Time{}, 0, false
		}
		return date, at, true
	}
	month, ok := digits(text, at, 2)
	if !ok {
		return time.Time{}, 0, false
	}
	at += 2
	if extended {
		if at >= len(text) || text[at] != '-' {
			return time.Time{}, 0, false
		}
		at++
	}
	day, ok := digits(text, at, 2)
	if !ok {
		return time.Time{}, 0, false
	}
	at += 2
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if date.Year() != year || int(date.Month()) != month || date.Day() != day {
		return time.Time{}, 0, false
	}
	return date, at, true
}
func isoClock(text string) (hour, minute, second, micro int, valid bool) {
	hour, ok := digits(text, 0, 2)
	if !ok {
		return 0, 0, 0, 0, false
	}
	at := 2
	extended := at < len(text) && text[at] == ':'
	parts := []*int{&minute, &second}
	for _, part := range parts {
		if at >= len(text) {
			break
		}
		if extended {
			if text[at] != ':' {
				break
			}
			at++
		} else if text[at] < '0' || text[at] > '9' {
			break
		}
		*part, ok = digits(text, at, 2)
		if !ok {
			return 0, 0, 0, 0, false
		}
		at += 2
	}
	if at < len(text) && (text[at] == '.' || text[at] == ',') {
		at++
		start := at
		for at < len(text) && text[at] >= '0' && text[at] <= '9' {
			if at-start < 6 {
				micro = micro*10 + int(text[at]-'0')
			}
			at++
		}
		if at == start {
			return 0, 0, 0, 0, false
		}
		for n := at - start; n < 6; n++ {
			micro *= 10
		}
	}
	return hour, minute, second, micro, at == len(text)
}
