package httpapi

import "strconv"

// The caller has checked bounded UTF-8 and JSON grammar. Match the existing
// native-domain Unicode boundary before encoding/json can repair a lone
// surrogate, including fields subsequently hidden by duplicate-key decoding.
func applicationScalarJSON(raw []byte) bool {
	quoted, depth := false, 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '"':
			quoted = !quoted
		case '{', '[':
			if !quoted {
				depth++
				if depth > 64 {
					return false
				}
			}
		case '}', ']':
			if !quoted {
				depth--
			}
		case '\\':
			if !quoted {
				continue
			}
			if i+5 >= len(raw) || raw[i+1] != 'u' {
				i++
				continue
			}
			code, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
			i += 5
			if code >= 0xdc00 && code <= 0xdfff {
				return false
			}
			if code < 0xd800 || code > 0xdbff {
				continue
			}
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, _ := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
