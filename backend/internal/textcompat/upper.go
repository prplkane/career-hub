package textcompat

import (
	"sort"
	"strings"
)

// Upper is Python3.12's locale-independent full Unicode15 uppercasing, including
// multi-scalar expansions. It does not depend on the Go compiler's Unicode data.
func Upper(input string) string {
	var out strings.Builder
	for _, point := range input {
		index := sort.Search(len(fullUpper), func(i int) bool { return fullUpper[i].source >= point })
		if index < len(fullUpper) && fullUpper[index].source == point {
			out.WriteString(fullUpper[index].target)
		} else {
			out.WriteRune(point)
		}
	}
	return out.String()
}

// DecimalDigit returns the Unicode15 decimal value, not numeric lookalikes such
// as superscript two. It is used for the existing Python int(text) contract.
func DecimalDigit(point rune) (int, bool) {
	index := sort.Search(len(decimalZeros), func(i int) bool { return decimalZeros[i] > point }) - 1
	if index >= 0 && point < decimalZeros[index]+10 {
		return int(point - decimalZeros[index]), true
	}
	return 0, false
}
