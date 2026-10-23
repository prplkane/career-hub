package publicjobresolver

import (
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/ianaindex"
)

func decodeHTML(body []byte, charset string) (string, error) {
	key := strings.ToLower(strings.ReplaceAll(charset, "_", "-"))
	if len(key) == 6 && strings.HasPrefix(key, "cp125") && key[5] >= '0' && key[5] <= '8' {
		key = "windows-" + key[2:]
	}
	switch key {
	case "utf-8", "utf8", "utf-8-sig":
		if key == "utf-8-sig" && len(body) >= 3 && string(body[:3]) == "\xef\xbb\xbf" {
			body = body[3:]
		}
		// Python consumes the valid prefix of a truncated/invalid continuation
		// sequence as one replacement; invalid leading bytes remain separate.
		var b strings.Builder
		for len(body) > 0 {
			r, n := utf8.DecodeRune(body)
			if r == utf8.RuneError && n == 1 && body[0] >= 0xc2 && body[0] <= 0xf4 {
				want := 2
				if body[0] >= 0xf0 {
					want = 4
				} else if body[0] >= 0xe0 {
					want = 3
				}
				for n < min(want, len(body)) {
					c := body[n]
					if c < 0x80 || c > 0xbf {
						break
					}
					if n == 1 && (body[0] == 0xe0 && c < 0xa0 || body[0] == 0xed && c >= 0xa0 || body[0] == 0xf0 && c < 0x90 || body[0] == 0xf4 && c >= 0x90) {
						break
					}
					n++
				}
			}
			b.WriteRune(r)
			body = body[n:]
		}
		return b.String(), nil
	case "ascii", "us-ascii":
		var b strings.Builder
		for _, c := range body {
			if c < 128 {
				b.WriteByte(c)
			} else {
				b.WriteRune('\ufffd')
			}
		}
		return b.String(), nil
	case "latin1", "latin-1", "iso8859-1", "iso-8859-1":
		var b strings.Builder
		for _, c := range body {
			b.WriteRune(rune(c))
		}
		return b.String(), nil
	}
	encoding, err := ianaindex.IANA.Encoding(key)
	if err != nil || encoding == nil {
		return "", errors.New("unsupported HTML encoding")
	}
	decoded, err := encoding.NewDecoder().Bytes(body)
	if err != nil {
		return "", errors.New("unsupported HTML encoding")
	}
	return string(decoded), nil
}
