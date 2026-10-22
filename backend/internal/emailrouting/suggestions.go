package emailrouting

import (
	"strconv"
	"strings"
	"time"

	"careerhub.local/backend/internal/textcompat"
)

const (
	recurrenceMinMessages     = 4
	recurrenceMinDistinctDays = 3
	recurrenceMinSpanDays     = 14
)

// UnknownSenderSignals contains only aggregate header/receipt facts. Message
// subjects and bodies are not accepted or returned by this classifier.
type UnknownSenderSignals struct {
	SenderAddress                 string `json:"sender_address"`
	MessageCount                  int    `json:"message_count"`
	DistinctMessageDays           int    `json:"distinct_message_days"`
	FirstMessageDate              string `json:"first_message_date"`
	LastMessageDate               string `json:"last_message_date"`
	SubjectJobSignalCount         int    `json:"subject_job_signal_count"`
	SubjectApplicationSignalCount int    `json:"subject_application_signal_count"`
	SubjectServiceSignalCount     int    `json:"subject_service_signal_count"`
}

type SenderSuggestion struct {
	Recommendation    string   `json:"recommendation"`
	SuggestionReasons []string `json:"suggestion_reasons"`
	ObservedSpanDays  int      `json:"observed_span_days"`
}

// SuggestUnknownSender ports the Python _pending_senders recommendation only.
// The future connection reader must supply the exact unknown-sender receipt
// aggregation, exclude explicitly disabled senders and preserve source order.
func SuggestUnknownSender(signals UnknownSenderSignals) SenderSuggestion {
	span := observedSpanDays(signals.FirstMessageDate, signals.LastMessageDate)
	result := SenderSuggestion{Recommendation: "none", SuggestionReasons: []string{}, ObservedSpanDays: span}
	if signals.SubjectServiceSignalCount > 0 {
		return result
	}
	knownApplication := signals.SenderAddress == "handshake@notifications.joinhandshake.com"
	application := signals.SubjectApplicationSignalCount > 0
	jobSender := senderHasJobSignal(signals.SenderAddress)
	jobSubject := signals.SubjectJobSignalCount > 0
	recurring := signals.MessageCount >= recurrenceMinMessages &&
		signals.DistinctMessageDays >= recurrenceMinDistinctDays && span >= recurrenceMinSpanDays
	if knownApplication {
		result.SuggestionReasons = append(result.SuggestionReasons, "known_tracking_sender")
	}
	if application {
		result.SuggestionReasons = append(result.SuggestionReasons, "subject_application_keyword")
	}
	if jobSender && !knownApplication && !application {
		result.SuggestionReasons = append(result.SuggestionReasons, "sender_keyword")
	}
	if jobSubject && !knownApplication && !application {
		result.SuggestionReasons = append(result.SuggestionReasons, "subject_keyword")
	}
	if recurring {
		result.SuggestionReasons = append(result.SuggestionReasons, "recurring_pattern")
	}
	switch {
	case knownApplication || application:
		result.Recommendation = "likely_application_tracking"
	case jobSender || jobSubject:
		result.Recommendation = "likely_job_alert"
	case recurring:
		result.Recommendation = "recurring_review"
	}
	return result
}

func senderHasJobSignal(address string) bool {
	if len(address) > 320 {
		return false
	}
	folded := textcompat.Fold(address)
	at := strings.LastIndexByte(folded, '@')
	if at < 0 {
		return false
	}
	local, domain := folded[:at], folded[at+1:]
	tokens := strings.Fields(NormalizeSignal(local))
	compact := strings.Join(tokens, "")
	for _, marker := range []string{"careeralert", "jobalert", "jobnotification"} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	for _, token := range tokens {
		switch token {
		case "career", "careers", "job", "jobs", "recruit", "recruiter", "recruiting", "talent":
			return true
		}
	}
	for _, provider := range []string{"12twenty.com", "joinhandshake.com", "jobs2web.com"} {
		if domain == provider || strings.HasSuffix(domain, "."+provider) {
			return true
		}
	}
	return false
}

func observedSpanDays(first, last string) int {
	start, ok := parseMessageDate(first)
	if !ok {
		return 0
	}
	end, ok := parseMessageDate(last)
	if !ok || end.Before(start) {
		return 0
	}
	return int((end.Unix() - start.Unix()) / 86400)
}

// The receipt writer stores YYYY-MM-DD. Python date.fromisoformat also accepts
// basic calendar and ISO-week forms, so retain them for older rows.
func parseMessageDate(raw string) (time.Time, bool) {
	if date, matched, valid := parseISOWeekDate(raw); matched {
		return date, valid
	}
	layout := "2006-01-02"
	if len(raw) == 8 {
		layout = "20060102"
	} else if len(raw) != 10 {
		return time.Time{}, false
	}
	date, err := time.Parse(layout, raw)
	return date, err == nil && date.Year() >= 1 && date.Format(layout) == raw
}

func parseISOWeekDate(raw string) (time.Time, bool, bool) {
	var yearText, weekText, dayText string
	switch {
	case (len(raw) == 7 || len(raw) == 8) && raw[4] == 'W':
		yearText, weekText = raw[:4], raw[5:7]
		if len(raw) == 8 {
			dayText = raw[7:]
		}
	case (len(raw) == 8 || len(raw) == 10) && raw[4:6] == "-W":
		yearText, weekText = raw[:4], raw[6:8]
		if len(raw) == 10 {
			if raw[8] != '-' {
				return time.Time{}, true, false
			}
			dayText = raw[9:]
		}
	default:
		return time.Time{}, false, false
	}
	if dayText == "" {
		dayText = "1" // Python date.fromisoformat defaults an ISO week to Monday.
	}
	year, yearErr := strconv.Atoi(yearText)
	week, weekErr := strconv.Atoi(weekText)
	day, dayErr := strconv.Atoi(dayText)
	if yearErr != nil || weekErr != nil || dayErr != nil || year < 1 || year > 9999 || week < 1 || week > 53 || day < 1 || day > 7 {
		return time.Time{}, true, false
	}
	jan4 := time.Date(year, time.January, 4, 0, 0, 0, 0, time.UTC)
	weekday := int(jan4.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	date := jan4.AddDate(0, 0, 1-weekday+(week-1)*7+day-1)
	isoYear, isoWeek := date.ISOWeek()
	return date, true, isoYear == year && isoWeek == week && date.Year() >= 1 && date.Year() <= 9999
}
