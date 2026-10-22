package discovery

import (
	"slices"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/calendarinput"
	"careerhub.local/backend/internal/textcompat"
)

type Error struct{ Stage, Code string }

func (e *Error) Error() string          { return "discovery read could not be completed" }
func failure(stage, code string) *Error { return &Error{Stage: stage, Code: code} }

type Filters struct {
	States        []string `json:"states"`
	SourceKinds   []string `json:"source_kinds"`
	Readiness     []string `json:"readiness"`
	Queues        []string `json:"queues"`
	Search        string   `json:"search"`
	ObservedAfter string   `json:"observed_after"`
}
type PageOptions struct {
	Filters
	Sort   string `json:"sort"`
	Limit  int    `json:"limit"`
	Offset int64  `json:"offset"`
}

const queueSQL = `
CASE
WHEN discoveries.content_scope = 'link_only' THEN 'needs_jd'
WHEN discoveries.content_scope <> 'confirmed_full' THEN 'review'
WHEN assessments.discovery_id IS NULL THEN 'review'
WHEN json_extract(assessments.assessment_json, '$.receipt.status') = 'clear_conflict' THEN 'clear_conflict'
WHEN json_extract(assessments.assessment_json, '$.receipt.status') = 'no_clear_conflict' THEN 'review_first'
ELSE 'review' END
`

func plainText(value string, maximum int) (string, *Error) {
	if !utf8.ValidString(value) {
		return "", failure("request", "INPUT_UNSUPPORTED")
	}
	if len(value) > 2<<20 {
		return "", failure("request", "INPUT_LIMIT")
	}
	cleaned := normalize(value)
	if strings.ContainsRune(value, 0) || utf8.RuneCountInString(cleaned) > maximum {
		return "", failure("request", "INVALID_TEXT")
	}
	return cleaned, nil
}

func normalizedTimestamp(value string) (string, *Error) {
	if !utf8.ValidString(value) {
		return "", failure("request", "INPUT_UNSUPPORTED")
	}
	if len(value) > 16<<10 {
		return "", failure("request", "INPUT_LIMIT")
	}
	value = textcompat.Strip(value)
	var normalized string
	var valid bool
	if utf8.RuneCountInString(value) == 10 {
		normalized, valid = calendarinput.ISODateUTCSeconds(value)
	} else {
		normalized, valid = calendarinput.ISODateTimeUTCSeconds(strings.ReplaceAll(value, "Z", "+00:00"))
	}
	if !valid {
		return "", failure("request", "INVALID_TIMESTAMP")
	}
	return normalized, nil
}

func queryParts(filters Filters) (string, []any, *Error) {
	for _, input := range []struct {
		values, allowed []string
		code            string
	}{
		{filters.States, []string{"inbox", "shortlisted", "dismissed", "promoted"}, "INVALID_STATES"},
		{filters.SourceKinds, []string{"extension", "email_file", "manual", "watcher", "public_resolver"}, "INVALID_SOURCES"},
		{filters.Readiness, []string{"needs_jd", "needs_review", "ready"}, "INVALID_READINESS"},
		{filters.Queues, []string{"needs_jd", "review_first", "review", "clear_conflict"}, "INVALID_QUEUES"},
	} {
		if len(input.values) > 100 {
			return "", nil, failure("request", "INPUT_LIMIT")
		}
		for _, value := range input.values {
			if !slices.Contains(input.allowed, value) {
				return "", nil, failure("request", input.code)
			}
		}
	}
	search, err := plainText(filters.Search, 500)
	if err != nil {
		return "", nil, err
	}
	after := ""
	if filters.ObservedAfter != "" {
		after, err = normalizedTimestamp(filters.ObservedAfter)
		if err != nil {
			return "", nil, err
		}
	}
	clauses, args := []string{}, []any{}
	addIN := func(prefix, suffix string, values []string) {
		if len(values) == 0 {
			return
		}
		clauses = append(clauses, prefix+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+suffix)
		for _, value := range values {
			args = append(args, value)
		}
	}
	addIN("discoveries.state IN (", ")", filters.States)
	addIN("EXISTS (SELECT 1 FROM job_discovery_observations AS observations WHERE observations.discovery_id = discoveries.discovery_id AND observations.source_kind IN (", "))", filters.SourceKinds)
	if len(filters.Readiness) > 0 {
		parts := []string{}
		for _, value := range filters.Readiness {
			switch value {
			case "needs_jd":
				parts = append(parts, "discoveries.content_scope = 'link_only'")
			case "needs_review":
				parts = append(parts, "discoveries.content_scope IN ('selection', 'page_visible')")
			case "ready":
				parts = append(parts, "discoveries.content_scope = 'confirmed_full'")
			}
		}
		clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
	}
	addIN("("+queueSQL+") IN (", ")", filters.Queues)
	if search != "" {
		pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(textcompat.Fold(search)) + "%"
		clauses = append(clauses, `(LOWER(COALESCE(overrides.title, discoveries.title)) LIKE ? ESCAPE '\' OR LOWER(COALESCE(overrides.company, discoveries.company)) LIKE ? ESCAPE '\' OR LOWER(COALESCE(overrides.location, discoveries.location)) LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	if after != "" {
		clauses = append(clauses, "discoveries.last_seen_at >= ?")
		args = append(args, after)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	return where, args, nil
}

func ValidateFilters(filters Filters) *Error { _, _, err := queryParts(filters); return err }
func ValidatePage(options PageOptions) *Error {
	if options.Limit < 1 || options.Limit > 100 {
		return failure("request", "INVALID_LIMIT")
	}
	if options.Offset < 0 {
		return failure("request", "INVALID_OFFSET")
	}
	if !slices.Contains([]string{"last_desc", "first_desc", "title_asc", "queue_priority"}, options.Sort) {
		return failure("request", "INVALID_SORT")
	}
	return ValidateFilters(options.Filters)
}
