package ledger

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

func scalarString(raw []byte, min, max int) (string, *Error) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", invalid()
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", invalid()
	}
	// encoding/json otherwise silently replaces unpaired surrogate escapes with
	// U+FFFD. Refuse that unsupported source string, never normalize it into data.
	if loneSurrogate(raw) {
		return "", &Error{Code: CodeUnsupported}
	}
	length := utf8.RuneCountInString(value)
	if length < min || length > max {
		return "", invalid()
	}
	return value, nil
}

// Grammar has already been checked by encoding/json. Escaped backslashes do
// not introduce Unicode escapes. A valid pair represents one scalar in Go.
func loneSurrogate(token []byte) bool {
	for i := 0; i < len(token); i++ {
		if token[i] != '\\' {
			continue
		}
		if i+5 >= len(token) || token[i+1] != 'u' {
			i++
			continue
		}
		code, _ := strconv.ParseUint(string(token[i+2:i+6]), 16, 16)
		i += 5
		if code >= 0xdc00 && code <= 0xdfff {
			return true
		}
		if code < 0xd800 || code > 0xdbff {
			continue
		}
		if i+6 >= len(token) || token[i+1] != '\\' || token[i+2] != 'u' {
			return true
		}
		low, _ := strconv.ParseUint(string(token[i+3:i+7]), 16, 16)
		if low < 0xdc00 || low > 0xdfff {
			return true
		}
		i += 6
	}
	return false
}
