// Package textcompat pins the Unicode semantics of existing skill identities.
// It is not linguistic/fuzzy matching or Unicode security normalization.
// Inputs must be valid UTF-8 scalar strings, validated at the decoding boundary.
package textcompat

import "strings"

//go:generate go run ./internal/generate

const UnicodeVersion = "15.0.0"

// IsSpace matches Python 3.12's no-argument str.split/strip, including the four
// C0 separators excluded by Go's unicode.IsSpace. Freeze the identity contract
// independently of the compiler's Unicode version.
func IsSpace(r rune) bool {
	return (r >= 0x09 && r <= 0x0d) || (r >= 0x1c && r <= 0x20) ||
		r == 0x85 || r == 0xa0 || r == 0x1680 || (r >= 0x2000 && r <= 0x200a) ||
		r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
}

func mapping(r rune) string {
	lo, hi := 0, len(fullFolds)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if fullFolds[mid].source < r {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(fullFolds) && fullFolds[lo].source == r {
		return fullFolds[lo].target
	}
	return ""
}

// Fold is Unicode15 default full case folding (C+F, not S/T). It deliberately
// does not apply NFC/NFKC or locale-sensitive casing. Characters introduced
// after Unicode15 remain unchanged, as in the pinned Python reference.
func Fold(value string) string {
	var out strings.Builder
	written := 0
	for index, r := range value {
		var replacement string
		if r >= 'A' && r <= 'Z' {
			replacement = string(r + ('a' - 'A'))
		} else if r >= 0x80 {
			replacement = mapping(r)
		}
		if replacement == "" {
			continue
		}
		if out.Cap() == 0 {
			out.Grow(len(value))
		}
		out.WriteString(value[written:index])
		out.WriteString(replacement)
		written = index + len(string(r))
	}
	if out.Cap() == 0 {
		return value
	}
	out.WriteString(value[written:])
	return out.String()
}

// Strip trims only Python whitespace at the two ends, without folding.
func Strip(value string) string { return strings.TrimFunc(value, IsSpace) }

// Normalize is the exact key function shared by canonical skill names/aliases.
func Normalize(value string) string {
	return strings.Join(strings.FieldsFunc(Fold(value), IsSpace), " ")
}
