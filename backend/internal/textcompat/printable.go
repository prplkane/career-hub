package textcompat

// IsPrintable matches Python3.12 Unicode15 str.isprintable for one scalar.
// It is used for source-compatible display/repr escaping, not input security.
// The compiler's Unicode17 unicode.IsPrint would change previously escaped text.
func IsPrintable(point rune) bool {
	if point < 0 || point > 0x10ffff {
		return false
	}
	lo, hi := 0, len(printableRanges)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if printableRanges[mid][1] < point {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo < len(printableRanges) && printableRanges[lo][0] <= point
}
