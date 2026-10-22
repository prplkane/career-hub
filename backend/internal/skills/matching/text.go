package matching

import (
	"context"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

type unit struct {
	point              rune
	byteStart, byteEnd int32
	charStart, charEnd int32
	first, last        bool // Do not recognize a partial full-casefold expansion.
}

type normalizedText struct {
	units       []unit
	original    []rune
	left, right []bool
}

func normalize(ctx context.Context, source string) (normalizedText, error) {
	text := normalizedText{original: []rune(source)}
	text.units = make([]unit, 0, len(text.original))
	index, normalizedBytes := 0, 0
	for offset, point := range source {
		if err := ctx.Err(); err != nil {
			return normalizedText{}, err
		}
		end := offset + utf8.RuneLen(point)
		if textcompat.IsSpace(point) {
			if len(text.units) > 0 && text.units[len(text.units)-1].point == ' ' {
				last := &text.units[len(text.units)-1]
				last.byteEnd, last.charEnd = int32(end), int32(index+1)
			} else {
				text.units = append(text.units, unit{' ', int32(offset), int32(end), int32(index), int32(index + 1), true, true})
				normalizedBytes++
			}
		} else {
			fold := string(point)
			if point >= 'A' && point <= 'Z' {
				fold = string(point + 32)
			} else if point >= 128 {
				fold = textcompat.Fold(fold)
			}
			for position, value := range fold {
				text.units = append(text.units, unit{value, int32(offset), int32(end), int32(index), int32(index + 1), position == 0, position+utf8.RuneLen(value) == len(fold)})
			}
			normalizedBytes += len(fold)
		}
		if normalizedBytes > MaxSourceBytes || len(text.units) > 2<<20 {
			return normalizedText{}, ErrLimit
		}
		index++
	}
	text.left, text.right = boundaries(text.original)
	if err := ctx.Err(); err != nil {
		return normalizedText{}, err
	}
	return text, nil
}

func word(point rune) bool      { return textcompat.IsWord(point) || textcompat.IsMark(point) }
func technical(point rune) bool { return point == '.' || point == '+' || point == '#' || point == '&' }

// Compute once in linear time: repeated scanning across long punctuation runs
// for every terminal candidate would multiply caller-controlled work.
func boundaries(points []rune) ([]bool, []bool) {
	left, right := make([]bool, len(points)+1), make([]bool, len(points)+1)
	left[0], right[len(points)] = true, true
	beforeTechnical := -1
	for index, point := range points {
		if !technical(point) {
			beforeTechnical = index
		}
		left[index+1] = !word(point) && (!technical(point) || beforeTechnical < 0 || !word(points[beforeTechnical]))
	}
	afterTechnical := len(points)
	for index := len(points) - 1; index >= 0; index-- {
		point := points[index]
		if !technical(point) {
			afterTechnical = index
		}
		right[index] = !word(point) && point != '+' && point != '#' && (!technical(point) || afterTechnical == len(points) || !word(points[afterTechnical]))
	}
	return left, right
}

// One bit per source codepoint keeps overlap selection bounded without a
// quadratic scan through previously selected spans or a mutable interval tree.
type occupancy []uint64

func mask(first, last int) uint64 { return (^uint64(0) << uint(first)) & (^uint64(0) >> uint(64-last)) }
func (used occupancy) overlaps(start, end int) bool {
	for start < end {
		block := start / 64
		stop := min(end, (block+1)*64)
		if used[block]&mask(start%64, stop-block*64) != 0 {
			return true
		}
		start = stop
	}
	return false
}
func (used occupancy) mark(start, end int) {
	for start < end {
		block := start / 64
		stop := min(end, (block+1)*64)
		used[block] |= mask(start%64, stop-block*64)
		start = stop
	}
}
