package applicationwrite

import (
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/isotime"
	"careerhub.local/backend/internal/textcompat"
)

const maxTimestampBytes = 256

// Application history permits an omitted timezone and stores UTC seconds.
// Attended-session evidence uses the same kernel with a stricter timezone rule
// and microsecond comparisons; the two API policies must not be conflated.
func normalizeTimestamp(value any, now string) (string, *Error) {
	if value == nil {
		return now, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", invalid("occurred_at must be a non-empty ISO date or timestamp")
	}
	if !utf8.ValidString(text) {
		return "", failed("input", "APPLICATION_LEDGER_UNICODE_UNSUPPORTED")
	}
	if len(text) > maxTimestampBytes {
		return "", failed("input", "APPLICATION_LEDGER_INPUT_LIMIT")
	}
	text = strings.TrimFunc(text, textcompat.IsSpace)
	if text == "" {
		return "", invalid("occurred_at must be a non-empty ISO date or timestamp")
	}
	dateOnly := utf8.RuneCountInString(text) == 10
	if !dateOnly {
		text = strings.ReplaceAll(text, "Z", "+00:00")
	}
	stamp, _, err := isotime.Parse(text, dateOnly)
	if err == isotime.ErrRange {
		return "", failed("input", "APPLICATION_LEDGER_TIMESTAMP_RANGE")
	}
	if err != nil {
		return "", invalid("occurred_at must use ISO format, for example 2026-07-31")
	}
	return stamp.Format("2006-01-02T15:04:05Z"), nil
}
