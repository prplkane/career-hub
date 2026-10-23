// Package publicjobresolver implements the retained, attended public-link API.
// It never discovers URLs, bypasses protected sources, or marks text verified.
package publicjobresolver

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const (
	MaxURLChars           = 4096
	MaxRedirects          = 3
	MaxDNSAddresses       = 16
	MaxConnectionAttempts = 4
	MaxResponseBytes      = 2 * 1024 * 1024
	MaxSourceChars        = 500000
	MinSourceChars        = 160
)

// Error contains only stable public classifications, never network/URL details.
type Error struct{ Code, Stage string }

func (e *Error) Error() string          { return e.Code }
func failure(code, stage string) *Error { return &Error{"PUBLIC_JOB_RESOLUTION_" + code, stage} }

var hostLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var protectedHosts = []string{"glassdoor.com", "indeed.com", "joinhandshake.com", "linkedin.com", "wellfound.com"}

// NormalizeURL preserves the source's escaped path/query (including literal %),
// default-port removal and IDNA2003 policy. This is not the looser stored-URL
// identity helper: every URL must pass this boundary before DNS or connection.
func NormalizeURL(value, previousScheme string) (string, *Error) {
	bad := func() (string, *Error) { return "", failure("URL_REJECTED", "url_policy") }
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxURLChars {
		return bad()
	}
	for _, c := range value {
		if c < 32 || c == 127 || c == '\\' || textcompat.IsSpace(c) {
			return bad()
		}
	}
	scheme, rest, ok := strings.Cut(value, ":")
	scheme = strings.ToLower(scheme)
	if !ok || (scheme != "https" && scheme != "http") || !strings.HasPrefix(rest, "//") {
		return bad()
	}
	rest = rest[2:]
	host, suffix := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		host, suffix = rest[:i], rest[i:]
	}
	if host == "" || strings.ContainsAny(host, "@[]") {
		return bad()
	}
	hostname, port, hasPort := strings.Cut(host, ":")
	if hasPort && port != "" {
		for _, c := range port {
			if c < '0' || c > '9' {
				return bad()
			}
		}
		n, err := strconv.Atoi(port)
		if err != nil || (scheme == "https" && n != 443) || (scheme == "http" && n != 80) {
			return bad()
		}
	}
	if previousScheme == "https" && scheme != "https" {
		return "", failure("REDIRECT_REJECTED", "redirect")
	}
	ascii, err := hostnameASCII(strings.TrimRight(hostname, "."))
	if err != nil || len(ascii) > 253 || !strings.Contains(ascii, ".") {
		return bad()
	}
	for _, label := range strings.Split(ascii, ".") {
		if !hostLabel.MatchString(label) {
			return bad()
		}
	}
	if _, err := netip.ParseAddr(ascii); err == nil {
		return bad()
	}
	for _, p := range protectedHosts {
		if ascii == p || strings.HasSuffix(ascii, "."+p) {
			return "", failure("PROTECTED_SOURCE", "url_policy")
		}
	}
	suffix, _, _ = strings.Cut(suffix, "#")
	path, query, _ := strings.Cut(suffix, "?")
	if path == "" {
		path = "/"
	}
	normalized := scheme + "://" + ascii + quoteURL(path, false)
	if query != "" {
		normalized += "?" + quoteURL(query, true)
	}
	if len(normalized) > MaxURLChars {
		return bad()
	}
	return normalized, nil
}

func quoteURL(s string, query bool) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/:@!$&'()*+,;=-._~%", rune(c)) || query && c == '?' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// Frozen Python3.12.14 ipaddress policy. IsGlobalUnicast alone admits private,
// documentation and reserved space and must never authorize this transport.
var private4 = prefixes("0.0.0.0/8 10.0.0.0/8 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.0.0.0/24 192.0.2.0/24 192.168.0.0/16 198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 240.0.0.0/4")
var exceptions4 = prefixes("192.0.0.9/32 192.0.0.10/32")
var private6 = prefixes("::1/128 ::/128 ::ffff:0:0/96 64:ff9b:1::/48 100::/64 2001::/23 2001:db8::/32 2002::/16 3fff::/20 fc00::/7 fe80::/10")
var exceptions6 = prefixes("2001:1::1/128 2001:1::2/128 2001:3::/32 2001:4:112::/48 2001:20::/28 2001:30::/28")
var reserved6 = prefixes("::/8 100::/8 200::/7 400::/6 800::/5 1000::/4 4000::/3 6000::/3 8000::/3 a000::/3 c000::/3 e000::/4 f000::/5 f800::/6 fe00::/9")
var shared4 = netip.MustParsePrefix("100.64.0.0/10")

func prefixes(value string) []netip.Prefix {
	var result []netip.Prefix
	for _, s := range strings.Fields(value) {
		result = append(result, netip.MustParsePrefix(s))
	}
	return result
}
func inPrefixes(a netip.Addr, values []netip.Prefix) bool {
	for _, p := range values {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
func publicAddress(a netip.Addr) bool {
	if !a.IsValid() || a.Zone() != "" {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	if a.Is4() {
		return !shared4.Contains(a) && (!inPrefixes(a, private4) || inPrefixes(a, exceptions4))
	}
	return !inPrefixes(a, reserved6) && (!inPrefixes(a, private6) || inPrefixes(a, exceptions6))
}
