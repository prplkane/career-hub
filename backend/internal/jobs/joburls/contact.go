package joburls

import (
	"strings"
	"unicode/utf8"
)

// ContactProfile preserves query order, tracking parameters and escaped path
// bytes. It is an identity normalizer only, never permission to open/fetch a
// page. Provider-specific job rewriting is deliberately not used here.
func ContactProfile(value string) (string, error) {
	if len(value) > MaxURLBytes || !utf8.ValidString(value) {
		return "", ErrUnsupported
	}
	origin, err := SessionOrigin(value, false)
	if err != nil {
		return "", err
	}
	_, path, query, ok := splitHTTP(value)
	if !ok {
		return "", ErrDiscoveryURL
	}
	path = strings.TrimRight(path, "/")
	if query != "" {
		path += "?" + query
	}
	return origin + path, nil
}
