package joburls

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

var (
	ErrDiscoveryURL   = errors.New("observed_url must be a valid absolute HTTP/HTTPS URL")
	ErrURLCredentials = errors.New("observed_url must not contain user information")
)

// CanonicalDiscovery supplies a conservative stored identity, NOT permission to
// navigate/fetch a URL. The caller already applied Discovery plain-text bounds.
// Unknown query fields and escaped paths survive; only known tracking is removed.
func CanonicalDiscovery(value string) (string, error) {
	if len(value) > MaxURLBytes || !utf8.ValidString(value) {
		return "", ErrUnsupported
	}
	if value == "" {
		return "", nil
	}
	parsed := strings.TrimLeftFunc(value, func(r rune) bool { return r <= 0x20 })
	parsed = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(parsed)
	scheme, rest, hasScheme := strings.Cut(parsed, ":")
	scheme = strings.ToLower(scheme)
	if !hasScheme || (scheme != "http" && scheme != "https") || !strings.HasPrefix(rest, "//") {
		return "", ErrDiscoveryURL
	}
	rest = rest[2:]
	netloc, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		netloc, tail = rest[:i], rest[i:]
	}
	for _, r := range netloc {
		if nfkcDelimiter(r) {
			return "", ErrDiscoveryURL
		}
	}
	user := false
	if i := strings.LastIndexByte(netloc, '@'); i >= 0 {
		user, netloc = true, netloc[i+1:]
	}
	host, port := netloc, ""
	if strings.ContainsAny(netloc, "[]") {
		if !strings.HasPrefix(netloc, "[") {
			return "", ErrDiscoveryURL
		}
		var suffix string
		var closed bool
		host, suffix, closed = strings.Cut(netloc[1:], "]")
		if !closed || !bracketedHost(host) || suffix != "" && !strings.HasPrefix(suffix, ":") {
			return "", ErrDiscoveryURL
		}
		port = strings.TrimPrefix(suffix, ":")
	} else {
		host, port, _ = strings.Cut(netloc, ":")
	}
	if host == "" {
		return "", ErrDiscoveryURL
	}
	portNumber := -1
	if port != "" {
		for _, c := range port {
			if c < '0' || c > '9' {
				return "", ErrDiscoveryURL
			}
		}
		n, err := strconv.ParseUint(strings.TrimLeft(port, "0"), 10, 16)
		if strings.TrimLeft(port, "0") == "" {
			n, err = 0, nil
		}
		if err != nil {
			return "", ErrDiscoveryURL
		}
		portNumber = int(n)
	}
	if user {
		return "", ErrURLCredentials
	}
	provider, err := Resolve(value)
	if err != nil {
		return "", err
	}
	if provider.URL != "" {
		return provider.URL, nil
	}
	host = strings.TrimRight(textcompat.Fold(host), ".")
	if strings.ContainsRune(host, ':') {
		host = "[" + host + "]"
	}
	if portNumber >= 0 && !(scheme == "http" && portNumber == 80 || scheme == "https" && portNumber == 443) {
		host += ":" + strconv.Itoa(portNumber)
	}
	tail, _, _ = strings.Cut(tail, "#")
	path, query, _ := strings.Cut(tail, "?")
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = "/"
	}
	type pair struct{ name, value, folded string }
	items := []pair{}
	for field := range strings.SplitSeq(query, "&") {
		if field == "" {
			continue
		}
		name, value, _ := strings.Cut(field, "=")
		name, value = queryUnicode(name), queryUnicode(value)
		folded := textcompat.Fold(name)
		if strings.HasPrefix(folded, "utm_") || slices.Contains([]string{"dclid", "fbclid", "gclid", "gbraid", "mc_cid", "mc_eid", "msclkid", "wbraid"}, folded) {
			continue
		}
		items = append(items, pair{name, value, folded})
	}
	slices.SortStableFunc(items, func(a, b pair) int {
		if c := strings.Compare(a.folded, b.folded); c != 0 {
			return c
		}
		return strings.Compare(a.value, b.value)
	})
	encoded := make([]string, len(items))
	for i, p := range items {
		encoded[i] = quoteQuery(p.name) + "=" + quoteQuery(p.value)
	}
	result := scheme + "://" + host + path
	if len(items) != 0 {
		result += "?" + strings.Join(encoded, "&")
	}
	return result, nil
}

// Python's UTF-8 replacement consumes a valid partial prefix as one error;
// Go range and strings.ToValidUTF8 have different invalid-byte grouping.
func queryUnicode(value string) string {
	raw := queryBytes(value)
	var b strings.Builder
	for len(raw) != 0 {
		r, n := utf8.DecodeRuneInString(raw)
		if r != utf8.RuneError || n != 1 {
			b.WriteString(raw[:n])
			raw = raw[n:]
			continue
		}
		n = 1
		first := raw[0]
		width := 0
		switch {
		case first >= 0xc2 && first <= 0xdf:
			width = 2
		case first >= 0xe0 && first <= 0xef:
			width = 3
		case first >= 0xf0 && first <= 0xf4:
			width = 4
		}
		for n < width && n < len(raw) {
			c := raw[n]
			if c < 0x80 || c > 0xbf || n == 1 && (first == 0xe0 && c < 0xa0 || first == 0xed && c >= 0xa0 || first == 0xf0 && c < 0x90 || first == 0xf4 && c >= 0x90) {
				break
			}
			n++
		}
		b.WriteRune(utf8.RuneError)
		raw = raw[n:]
	}
	return b.String()
}

func quoteQuery(value string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := range len(value) {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.ContainsRune("-_.~", rune(c)):
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
