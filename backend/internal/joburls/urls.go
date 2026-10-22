// Package joburls preserves the five exact posting-identity rules used by the
// Python schema-v18 backfill. It does not fetch URLs, authorize navigation,
// resolve redirects, or guess a posting from its title.
package joburls

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const MaxURLBytes = 64 << 10

var ErrUnsupported = errors.New("posting URL exceeds the supported scalar-text bounds")

type Posting struct {
	Identity string `json:"identity"`
	URL      string `json:"url"`
}

// ValidSource checks stored absolute HTTP(S) syntax, not permission to fetch a
// URL. No DNS, redirect, login, loopback or network authorization is performed.
// Unlike legacy posting identity, controls/credentials/malformed ports are not
// silently normalized. Provider-specific Resolve behavior remains unchanged.
func ValidSource(value string) (bool, error) {
	if len(value) > MaxURLBytes || !utf8.ValidString(value) {
		return false, ErrUnsupported
	}
	for _, r := range value {
		if r < 32 || r == 127 || textcompat.IsSpace(r) || r == '\\' {
			return false, nil
		}
	}
	if _, _, _, ok := splitHTTP(value); !ok {
		return false, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" {
		return false, nil
	}
	if !strings.HasPrefix(parsed.Host, "[") && strings.Contains(parsed.Hostname(), ":") {
		return false, nil
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return false, nil
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 0 || number > 65535 {
			return false, nil
		}
	}
	return true, nil
}

// Resolve returns an empty posting for an unrecognized/invalid URL. An explicit
// error, not an empty identity, marks input outside the supported migration
// bounds. Legacy URL parsing is deliberately not an HTTP security validator:
// e.g. Python never reads .port, so a malformed port does not reject an ID.
func Resolve(value string) (Posting, error) {
	if len(value) > MaxURLBytes || !utf8.ValidString(value) {
		return Posting{}, ErrUnsupported
	}
	host, path, query, ok := splitHTTP(value)
	if !ok {
		return Posting{}, nil
	}
	var provider, id, canonical string
	switch {
	case isHost(host, "linkedin.com"):
		provider = "linkedin"
		id = pathID(path, []string{"/jobs/view/", "/comm/jobs/view/"}, false)
		if id == "" {
			id = queryValue(query, "currentjobid")
			if !allDigits(id) {
				id = ""
			}
		}
		canonical = "https://www.linkedin.com/jobs/view/" + id + "/"
	case isHost(host, "indeed.com"):
		provider = "indeed"
		id = queryValue(query, "vjk", "jk")
		if !token(id, 100, false) {
			id = ""
		}
		id = textcompat.Fold(id)
		canonical = "https://www.indeed.com/viewjob?jk=" + id
	case isHost(host, "joinhandshake.com"):
		provider = "handshake"
		id = pathID(path, []string{"/job-search/", "/stu/jobs/", "/stu/postings/", "/jobs/"}, false)
		canonical = "https://app.joinhandshake.com/job-search/" + id
	case isHost(host, "meeboss.com"):
		provider = "meeboss"
		id = queryValue(query, "currentjobid")
		if !token(id, 160, true) {
			id = ""
		}
		// Every allowed token character is unchanged by Python urlencode.
		canonical = "https://meeboss.com/job?currentJobId=" + id
	case isHost(host, "12twenty.com"):
		provider = "12twenty"
		id = queryValue(query, "jobpostingid", "jobid")
		if !token(id, 100, false) {
			id = pathID(path, []string{"/jobpostings/", "jobpostings/"}, true)
		}
		canonical = "https://" + host + "/jobPostings/" + id
	}
	if id == "" {
		return Posting{}, nil
	}
	identity := provider + ":" + id
	if provider == "meeboss" || provider == "12twenty" {
		digest := sha256.Sum256([]byte(id))
		identity = provider + ":sha256:" + hex.EncodeToString(digest[:])
	}
	return Posting{Identity: identity, URL: canonical}, nil
}

func isHost(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// Only the urllib.parse.urlsplit behavior relevant to these identities is
// reproduced. Paths remain escaped, dot segments are not resolved, credentials
// are rejected, and NFKC delimiter checks use a frozen Unicode15 property.
func splitHTTP(value string) (host, path, query string, ok bool) {
	value = strings.TrimLeftFunc(textcompat.Strip(value), func(r rune) bool { return r <= 0x20 })
	value = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(value)
	scheme, rest, found := strings.Cut(value, ":")
	if !found || !(strings.ToLower(scheme) == "http" || strings.ToLower(scheme) == "https") || !strings.HasPrefix(rest, "//") {
		return
	}
	rest = rest[2:]
	netloc := rest
	path = ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		netloc, path = rest[:i], rest[i:]
	}
	if strings.Contains(netloc, "@") {
		return "", "", "", false
	}
	for _, r := range netloc {
		if nfkcDelimiter(r) {
			return "", "", "", false
		}
	}
	if strings.ContainsAny(netloc, "[]") {
		if !strings.HasPrefix(netloc, "[") {
			return "", "", "", false
		}
		var suffix string
		host, suffix, found = strings.Cut(netloc[1:], "]")
		if !found || (suffix != "" && !strings.HasPrefix(suffix, ":")) || !bracketedHost(host) {
			return "", "", "", false
		}
	} else {
		host, _, _ = strings.Cut(netloc, ":")
	}
	if host == "" {
		return "", "", "", false
	}
	host = strings.TrimRight(textcompat.Fold(host), ".")
	path, _, _ = strings.Cut(path, "#")
	path, query, _ = strings.Cut(path, "?")
	return host, path, query, true
}

func bracketedHost(host string) bool {
	if strings.HasPrefix(host, "v") {
		hexPart, body, found := strings.Cut(host[1:], ".")
		if !found || hexPart == "" || body == "" {
			return false
		}
		for i := range len(hexPart) {
			if _, ok := hexByte(hexPart[i]); !ok {
				return false
			}
		}
		return true
	}
	// ipaddress rejects empty/repeated scope delimiters. A zone is otherwise
	// opaque; validate the address separately from Go's zone interpretation.
	address, zone, hasZone := strings.Cut(host, "%")
	if hasZone && (zone == "" || strings.Contains(zone, "%")) {
		return false
	}
	parsed, err := netip.ParseAddr(address)
	return err == nil && parsed.Is6()
}

// Python re.IGNORECASE ASCII ranges/literals additionally match these four
// Unicode characters. Full casefold (especially dotted I) is not equivalent.
func pathFold(r rune) rune {
	switch r {
	case 'İ', 'ı':
		return 'i'
	case 'ſ':
		return 's'
	case 'K':
		return 'k'
	}
	if r >= 'A' && r <= 'Z' {
		return r + 'a' - 'A'
	}
	return r
}

func pathID(path string, prefixes []string, generalToken bool) string {
	for _, prefix := range prefixes {
		remaining := path
		matched := true
		for _, want := range prefix {
			got, size := utf8.DecodeRuneInString(remaining)
			if size == 0 || pathFold(got) != want {
				matched = false
				break
			}
			remaining = remaining[size:]
		}
		if !matched {
			continue
		}
		id, _, _ := strings.Cut(remaining, "/")
		count := 0
		for _, r := range id {
			count++
			if generalToken {
				r = pathFold(r)
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
					return ""
				}
			} else if r < '0' || r > '9' {
				return ""
			}
		}
		if count == 0 || (generalToken && (count < 4 || count > 100)) {
			return ""
		}
		return id
	}
	return ""
}

func token(value string, max int, tilde bool) bool {
	if len(value) < 4 || len(value) > max {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || (tilde && c == '~')) {
			return false
		}
	}
	return true
}

func queryValue(query string, names ...string) string {
	var result string
	for _, field := range strings.Split(query, "&") {
		key, value, present := strings.Cut(field, "=")
		if !present || value == "" {
			continue
		}
		key = textcompat.Fold(queryBytes(key))
		for _, name := range names {
			if key != name {
				continue
			}
			value = textcompat.Strip(queryBytes(value))
			if value == "" {
				break
			}
			if result != "" && result != value {
				return ""
			}
			result = value
			break
		}
	}
	return result
}

// Invalid UTF-8 replacement grouping is irrelevant to this narrow query:
// invalid keys never equal an allowed key, and every allowed ID excludes the
// replacement character. Leave invalid bytes invalid rather than coerce an ID.
func queryBytes(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] == '+' {
			result.WriteByte(' ')
		} else if value[i] == '%' && i+2 < len(value) {
			a, aOK := hexByte(value[i+1])
			b, bOK := hexByte(value[i+2])
			if aOK && bOK {
				result.WriteByte(a<<4 | b)
				i += 2
			} else {
				result.WriteByte(value[i])
			}
		} else {
			result.WriteByte(value[i])
		}
	}
	return result.String()
}

func hexByte(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		lo, hi := 0, len(digitRanges)
		for lo < hi {
			mid := lo + (hi-lo)/2
			if digitRanges[mid][1] < r {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo == len(digitRanges) || digitRanges[lo][0] > r {
			return false
		}
	}
	return true
}

func nfkcDelimiter(r rune) bool {
	for _, delimiter := range nfkcDelimiterRunes {
		if r == delimiter {
			return true
		}
	}
	return false
}
