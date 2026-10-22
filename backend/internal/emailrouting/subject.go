// Package emailrouting ports the offline, header-only subject-policy contract.
// It neither opens a mailbox nor writes Career Hub data.
package emailrouting

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"careerhub.local/backend/internal/textcompat"
)

const (
	SenderPurposeJobAlerts           = "job_alerts"
	SenderPurposeApplicationTracking = "application_tracking"
	SenderPurposeBoth                = "both"

	maxPhrases     = 20
	maxPhraseChars = 120
)

// ErrInvalidPolicy is the same fail-closed error code as the Python source.
// It never contains policy text, message subjects, or other private data.
var ErrInvalidPolicy = errors.New("EMAIL_CONNECTION_INVALID")

type routePolicy struct {
	includeAny []string
	excludeAny []string
}

// Policy can only be constructed by parsing a complete source-compatible
// policy or by using EmptyPolicy. Its phrases are normalized and immutable.
type Policy struct {
	jobs         routePolicy
	applications routePolicy
}

func EmptyPolicy() Policy { return Policy{} }

// NormalizeSignal matches Python's ASCII [a-z0-9]+ tokens after full casefold.
// In particular, it does not apply Unicode compatibility normalization.
func NormalizeSignal(value string) string {
	var out strings.Builder
	inToken := false
	for _, r := range textcompat.Fold(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if !inToken && out.Len() != 0 {
				out.WriteByte(' ')
			}
			out.WriteByte(byte(r))
			inToken = true
		} else {
			inToken = false
		}
	}
	return out.String()
}

func normalizePhrases(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, ErrInvalidPolicy
	}
	seen := make(map[string]bool, len(items))
	phrases := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, ErrInvalidPolicy
		}
		phrase := NormalizeSignal(text)
		if phrase == "" || len(phrase) > maxPhraseChars {
			return nil, ErrInvalidPolicy
		}
		if !seen[phrase] {
			seen[phrase] = true
			phrases = append(phrases, phrase)
		}
	}
	if len(phrases) > maxPhrases {
		return nil, ErrInvalidPolicy
	}
	slices.Sort(phrases)
	return phrases, nil
}

func parseRoute(value any) (routePolicy, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 2 {
		return routePolicy{}, ErrInvalidPolicy
	}
	include, hasInclude := object["include_any"]
	exclude, hasExclude := object["exclude_any"]
	if !hasInclude || !hasExclude {
		return routePolicy{}, ErrInvalidPolicy
	}
	includeAny, err := normalizePhrases(include)
	if err != nil {
		return routePolicy{}, err
	}
	excludeAny, err := normalizePhrases(exclude)
	if err != nil {
		return routePolicy{}, err
	}
	return routePolicy{includeAny: includeAny, excludeAny: excludeAny}, nil
}

// ParsePolicyJSON mirrors _subject_policy_from_json. Only absent, empty,
// JSON null, or an empty object means the default permissive policy. Every
// nonempty policy must contain exactly the two known routes and their keys.
func ParsePolicyJSON(raw string) (Policy, error) {
	if raw == "" {
		return EmptyPolicy(), nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return Policy{}, ErrInvalidPolicy
	}
	if decoded == nil {
		return EmptyPolicy(), nil
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return Policy{}, ErrInvalidPolicy
	}
	if len(object) == 0 {
		return EmptyPolicy(), nil
	}
	if len(object) != 2 {
		return Policy{}, ErrInvalidPolicy
	}
	jobsValue, hasJobs := object[SenderPurposeJobAlerts]
	applicationsValue, hasApplications := object[SenderPurposeApplicationTracking]
	if !hasJobs || !hasApplications {
		return Policy{}, ErrInvalidPolicy
	}
	jobs, err := parseRoute(jobsValue)
	if err != nil {
		return Policy{}, err
	}
	applications, err := parseRoute(applicationsValue)
	if err != nil {
		return Policy{}, err
	}
	return Policy{jobs: jobs, applications: applications}, nil
}

// CanonicalJSON matches _subject_policy_json's sorted, compact ASCII output.
func (policy Policy) CanonicalJSON() string {
	makeRoute := func(route routePolicy) map[string][]string {
		include := append([]string{}, route.includeAny...)
		exclude := append([]string{}, route.excludeAny...)
		return map[string][]string{"include_any": include, "exclude_any": exclude}
	}
	raw, _ := json.Marshal(map[string]map[string][]string{
		SenderPurposeJobAlerts:           makeRoute(policy.jobs),
		SenderPurposeApplicationTracking: makeRoute(policy.applications),
	})
	return string(raw)
}

func containsPhrase(subject string, phrases []string) bool {
	if subject == "" {
		return false
	}
	padded := " " + subject + " "
	for _, phrase := range phrases {
		if strings.Contains(padded, " "+phrase+" ") {
			return true
		}
	}
	return false
}

func routeMatches(subject string, route routePolicy) bool {
	if len(route.excludeAny) != 0 && containsPhrase(subject, route.excludeAny) {
		return false
	}
	return len(route.includeAny) == 0 || containsPhrase(subject, route.includeAny)
}

// RoutedPurpose evaluates a header subject before fetching any body. An empty
// result means filtered. Unknown sender purposes also fail closed.
func RoutedPurpose(senderPurpose string, policy Policy, subject string) string {
	normalized := NormalizeSignal(subject)
	jobsEnabled := senderPurpose == SenderPurposeJobAlerts || senderPurpose == SenderPurposeBoth
	applicationsEnabled := senderPurpose == SenderPurposeApplicationTracking || senderPurpose == SenderPurposeBoth
	jobs := jobsEnabled && routeMatches(normalized, policy.jobs)
	applications := applicationsEnabled && routeMatches(normalized, policy.applications)
	if jobs && applications {
		return SenderPurposeBoth
	}
	if jobs {
		return SenderPurposeJobAlerts
	}
	if applications {
		return SenderPurposeApplicationTracking
	}
	return ""
}
