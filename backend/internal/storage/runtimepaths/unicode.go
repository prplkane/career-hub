package runtimepaths

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func normalizeName(name string) (string, *Error) {
	if !utf8.ValidString(name) {
		return "", failure("PATH_UNICODE_UNSUPPORTED")
	}
	if norm.Version != "17.0.0" {
		return "", failure("PATH_UNICODE_VERSION")
	}
	for _, r := range name {
		if incompatibleNFCScalar(r) {
			return "", failure("PATH_UNICODE_UNSUPPORTED")
		}
	}
	normalized := norm.NFC.String(name)
	// A sequence of individually unchanged newly assigned scalars can compose
	// into a new character. Check the result as well as the source.
	for _, r := range normalized {
		if incompatibleNFCScalar(r) {
			return "", failure("PATH_UNICODE_UNSUPPORTED")
		}
	}
	if strings.Count(normalized, "\u034f") != strings.Count(name, "\u034f") {
		return "", failure("PATH_UNICODE_STREAM_UNSUPPORTED")
	}
	return normalized, nil
}

// Generated compatibility inventory, NOT normalization data: these 66 scalars
// differ between Python Unicode15 and x/text Unicode17 in canonical combining
// class or NFD decomposition. See TestNFCCompatibilityInventory for exhaustive
// executable derivation. Unicode normalization stability covers other existing
// scalars; newly assigned unchanged CCC=0 scalars are not needlessly rejected.
var incompatibleNFCRanges = [...][2]rune{
	{0x0897, 0x0897}, {0x1ACF, 0x1ADD}, {0x1AE0, 0x1AEB},
	{0x105C9, 0x105C9}, {0x105E4, 0x105E4}, {0x10D69, 0x10D6D}, {0x10EFA, 0x10EFB},
	{0x11383, 0x11383}, {0x11385, 0x11385}, {0x1138E, 0x1138E}, {0x11391, 0x11391},
	{0x113C5, 0x113C5}, {0x113C7, 0x113C8}, {0x113CE, 0x113D0},
	{0x16121, 0x16128}, {0x1612F, 0x1612F}, {0x16D68, 0x16D6A},
	{0x1E5EE, 0x1E5EF}, {0x1E6E3, 0x1E6E3}, {0x1E6E6, 0x1E6E6},
	{0x1E6EE, 0x1E6EF}, {0x1E6F5, 0x1E6F5},
}

func incompatibleNFCScalar(r rune) bool {
	for _, pair := range incompatibleNFCRanges {
		if r < pair[0] {
			return false
		}
		if r <= pair[1] {
			return true
		}
	}
	return false
}
