package joburls

import (
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// ProviderName preserves the display provider/hostname projection. A provider
// label is not a fetch allowlist or proof that the URL identifies a job.
func ProviderName(value string) (string, error) {
	if len(value) > MaxURLBytes || !utf8.ValidString(value) {
		return "", ErrUnsupported
	}
	host, _, _, ok := splitHTTP(value)
	if !ok {
		return "", nil
	}
	for _, pair := range [][2]string{{"linkedin.com", "linkedin"}, {"indeed.com", "indeed"}, {"joinhandshake.com", "handshake"}, {"meeboss.com", "meeboss"}, {"12twenty.com", "12twenty"}} {
		if isHost(host, pair[0]) {
			return pair[1], nil
		}
	}
	return host, nil
}

// KnownNonJobDestination recognizes the narrow source event/resource path rule.
// It does not decode path escapes, inspect query destinations, fetch, or infer
// from prose; an explicit job path segment takes precedence over event/resource.
func KnownNonJobDestination(value string) (bool, error) {
	if len(value) > MaxURLBytes || !utf8.ValidString(value) {
		return false, ErrUnsupported
	}
	_, path, _, ok := splitHTTP(value)
	if !ok {
		return false, nil
	}
	nonJob := false
	for _, segment := range strings.Split(path, "/") {
		if textcompat.Strip(segment) == "" {
			continue
		}
		switch textcompat.Fold(segment) {
		case "job", "jobs", "jobposting", "jobpostings", "opening", "openings", "position", "positions":
			return false, nil
		case "event", "events", "resource", "resources":
			nonJob = true
		}
	}
	return nonJob, nil
}
