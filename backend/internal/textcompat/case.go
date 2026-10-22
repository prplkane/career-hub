package textcompat

import (
	"sort"
	"strings"
)

type caseRange struct{ first, last, step, delta rune }
type caseExpansion struct {
	source rune
	target string
}

func caseMap(point rune, ranges []caseRange, expansions []caseExpansion) string {
	i := sort.Search(len(expansions), func(i int) bool { return expansions[i].source >= point })
	if i < len(expansions) && expansions[i].source == point {
		return expansions[i].target
	}
	i = sort.Search(len(ranges), func(i int) bool { return ranges[i].last >= point })
	if i < len(ranges) && point >= ranges[i].first && (point-ranges[i].first)%ranges[i].step == 0 {
		return string(point + ranges[i].delta)
	}
	return string(point)
}
func isCaseIgnorable(point rune) bool {
	i := sort.Search(len(caseIgnorableRanges), func(i int) bool { return caseIgnorableRanges[i][1] >= point })
	return i < len(caseIgnorableRanges) && point >= caseIgnorableRanges[i][0]
}
func isCased(point rune) bool { return analysisFlags(point)&12 != 0 }

// Lower implements locale-independent Python3.12 full Unicode15 lowercasing,
// including dotted-I expansion and contextual final sigma. Never use display
// casing to rewrite source evidence or instead of the registry's Fold contract.
func Lower(value string) string { return changeCase(value, false) }

// Title implements Python str.title, not editorial/locale-aware title casing.
// A non-cased character starts a new word, even an apostrophe or combining mark.
func Title(value string) string { return changeCase(value, true) }

func changeCase(value string, title bool) string {
	points := []rune(value)
	ahead := make([]bool, len(points))
	next := false
	for i := len(points) - 1; i >= 0; i-- {
		ahead[i] = next
		if !isCaseIgnorable(points[i]) {
			next = isCased(points[i])
		}
	}
	var result strings.Builder
	previous, contextBefore := false, false
	for i, point := range points {
		if title && !previous {
			result.WriteString(caseMap(point, titleRanges[:], titleExpansions[:]))
		} else if point == '\u03a3' && contextBefore && !ahead[i] {
			result.WriteRune('\u03c2')
		} else {
			result.WriteString(caseMap(point, lowerRanges[:], lowerExpansions[:]))
		}
		previous = isCased(point)
		if !isCaseIgnorable(point) {
			contextBefore = previous
		}
	}
	return result.String()
}
