package textcompat

import "sort"

// IsAlpha matches Python3.12 str.isalpha for one Unicode15 scalar. It is not
// isalnum, a word boundary, an identifier policy or a Unicode security filter.
func IsAlpha(point rune) bool {
	index := sort.Search(len(alphaRanges), func(i int) bool { return alphaRanges[i][1] >= point })
	return index < len(alphaRanges) && point >= alphaRanges[index][0]
}
