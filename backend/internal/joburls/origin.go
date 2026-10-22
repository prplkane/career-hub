package joburls

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// SessionOrigin mirrors the source urlsplit origin grammar, without provider
// posting rewrites or network access. Empty-after-folding hosts are rejected
// explicitly rather than issuing an unusable capability for "https://".
func SessionOrigin(value string, originOnly bool) (string, error) {
	if len(value) > MaxURLBytes || !utf8.ValidString(value) {
		return "", ErrUnsupported
	}
	value = strings.TrimLeftFunc(textcompat.Strip(value), func(r rune) bool { return r <= 0x20 })
	value = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(value)
	host, _, _, ok := splitHTTP(value)
	if !ok || host == "" {
		return "", ErrDiscoveryURL
	}
	scheme, rest, _ := strings.Cut(value, ":")
	scheme = strings.ToLower(scheme)
	rest = rest[2:]
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	if originOnly {
		path, fragment, _ := strings.Cut(tail, "#")
		path, query, _ := strings.Cut(path, "?")
		if (path != "" && path != "/") || query != "" || fragment != "" {
			return "", ErrDiscoveryURL
		}
	}
	port := ""
	if strings.HasPrefix(authority, "[") {
		_, suffix, _ := strings.Cut(authority, "]")
		port = strings.TrimPrefix(suffix, ":")
	} else {
		_, port, _ = strings.Cut(authority, ":")
	}
	if strings.ContainsRune(host, ':') {
		host = "[" + host + "]"
	}
	if port != "" {
		for _, c := range port {
			if c < '0' || c > '9' {
				return "", ErrDiscoveryURL
			}
		}
		trimmed := strings.TrimLeft(port, "0")
		if trimmed == "" {
			trimmed = "0"
		}
		n, err := strconv.ParseUint(trimmed, 10, 16)
		if err != nil {
			return "", ErrDiscoveryURL
		}
		if !(scheme == "https" && n == 443 || scheme == "http" && n == 80) {
			host += ":" + strconv.FormatUint(n, 10)
		}
	}
	return scheme + "://" + host, nil
}
