package textcompat

import "sort"

// IsMark is Unicode15's Mn/Mc/Me category. Keep combining marks attached at
// literal skill boundaries without inheriting the compiler's newer UCD tables.
func IsMark(point rune) bool {
	i := sort.Search(len(markRanges), func(i int) bool { return markRanges[i][1] >= point })
	return i < len(markRanges) && point >= markRanges[i][0]
}
