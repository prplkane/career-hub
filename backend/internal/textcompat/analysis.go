package textcompat

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func analysisFlags(point rune) byte {
	i := sort.Search(len(analysisRanges), func(i int) bool { return analysisRanges[i].last >= point })
	if i < len(analysisRanges) && point >= analysisRanges[i].first {
		return analysisRanges[i].flags
	}
	return 0
}

// IsWord is the pinned Python regular-expression word property, not just letters.
func IsWord(point rune) bool { return analysisFlags(point)&2 != 0 }

// IsUpperString requires at least one uppercase character and no lowercase or
// titlecase character. Uncased digits/punctuation do not make a heading lowercase.
func IsUpperString(value string) bool {
	upper := false
	for _, point := range value {
		flags := analysisFlags(point)
		if flags&8 != 0 {
			return false
		}
		upper = upper || flags&4 != 0
	}
	return upper
}

// SplitLines preserves Python splitlines' separators and final-line convention.
func SplitLines(value string) []string {
	lines := make([]string, 0)
	start, skipLF := 0, false
	for offset, point := range value {
		if skipLF && point == '\n' {
			start = offset + 1
			skipLF = false
			continue
		}
		skipLF = false
		switch point {
		case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			lines = append(lines, value[start:offset])
			start = offset + utf8.RuneLen(point)
			skipLF = point == '\r'
		}
	}
	if start < len(value) {
		lines = append(lines, value[start:])
	}
	return lines
}

// MaxNormalizationBytes bounds both input and normalized output. The intermediate
// decomposition has a separate scalar limit; unsupported inputs fail atomically.
const MaxNormalizationBytes = 4 << 20
const maxDecompositionRunes = 2 << 20

var ErrNormalizationInput = errors.New("unsupported normalization input")

type combiningPoint struct {
	point rune
	class uint8
}

// NFKC15 implements UAX15 decomposition/order/composition without x/text's
// stream-safe CGJ insertion. Per-scalar normalization data is stable for assigned
// Unicode15 characters. Later assignments remain opaque, as in Python3.12.
// Never use this on evidence text; only the source identity uses normalization.
func NFKC15(value string) (string, error) {
	if len(value) > MaxNormalizationBytes || !utf8.ValidString(value) || norm.Version != "17.0.0" {
		return "", ErrNormalizationInput
	}
	ascii := true
	for _, b := range []byte(value) {
		if b >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return value, nil
	}
	points := make([]combiningPoint, 0, min(utf8.RuneCountInString(value), maxDecompositionRunes))
	for _, point := range value {
		decomposition := string(point)
		known := analysisFlags(point)&1 != 0
		if known {
			decomposition = norm.NFKD.String(decomposition)
		}
		for _, decomposed := range decomposition {
			if len(points) == maxDecompositionRunes {
				return "", ErrNormalizationInput
			}
			var class uint8
			if known {
				class = norm.NFC.PropertiesString(string(decomposed)).CCC()
			}
			points = append(points, combiningPoint{decomposed, class})
		}
	}
	// Sort each uninterrupted nonstarter run, including an initial run. Stable
	// ordering is essential for equal classes and long runs must not be quadratic.
	for first := 0; first < len(points); {
		if points[first].class == 0 {
			first++
			continue
		}
		last := first + 1
		for last < len(points) && points[last].class != 0 {
			last++
		}
		sort.SliceStable(points[first:last], func(i, j int) bool { return points[first+i].class < points[first+j].class })
		first = last
	}
	count, starter, lastClass := 0, -1, uint8(0)
	for _, current := range points {
		if starter >= 0 && (lastClass < current.class || lastClass == 0) &&
			analysisFlags(current.point)&1 != 0 && analysisFlags(points[starter].point)&1 != 0 &&
			!norm.NFC.PropertiesString(string(current.point)).BoundaryBefore() {
			pair := norm.NFC.String(string([]rune{points[starter].point, current.point}))
			if utf8.RuneCountInString(pair) == 1 {
				points[starter].point, _ = utf8.DecodeRuneInString(pair)
				continue
			}
		}
		if current.class == 0 {
			starter = count
		}
		points[count] = current
		count++
		lastClass = current.class
	}
	var result strings.Builder
	for _, point := range points[:count] {
		if result.Len()+utf8.RuneLen(point.point) > MaxNormalizationBytes {
			return "", ErrNormalizationInput
		}
		result.WriteRune(point.point)
	}
	return result.String(), nil
}
