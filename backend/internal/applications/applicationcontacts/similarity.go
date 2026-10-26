package applicationcontacts

import (
	"careerhub.local/backend/internal/textcompat"
	"context"
	"strings"
)

func fold(s string) string { return textcompat.Fold(s) }

// SequenceMatcher with autojunk=False, no junk predicate. Longest blocks use
// Python's earliest-a/earliest-b tie breaking; edit distance is not equivalent.
// A shared work budget and cancellation bound repetitive legacy input.
func similar(ctx context.Context, left, right string, threshold float64, work *int) (bool, *Error) {
	a := []rune(strings.Join(strings.FieldsFunc(fold(left), textcompat.IsSpace), " "))
	b := []rune(strings.Join(strings.FieldsFunc(fold(right), textcompat.IsSpace), " "))
	if len(a) == 0 || len(b) == 0 {
		return false, nil
	}
	if string(a) == string(b) {
		return true, nil
	}
	if min(len(a), len(b)) < 5 {
		return false, nil
	}
	if len(a) > 4096 || len(b) > 4096 {
		return false, unsupported("SIMILARITY_LIMIT")
	}
	index := map[rune][]int{}
	for j, c := range b {
		index[c] = append(index[c], j)
	}
	stack := [][4]int{{0, len(a), 0, len(b)}}
	matches := 0
	for len(stack) > 0 {
		span := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		alo, ahi, blo, bhi := span[0], span[1], span[2], span[3]
		besti, bestj, best := alo, blo, 0
		previous := map[int]int{}
		for i := alo; i < ahi; i++ {
			if ctx.Err() != nil {
				return false, unsupported("CANCELED")
			}
			next := map[int]int{}
			for _, j := range index[a[i]] {
				*work--
				if *work < 0 {
					return false, unsupported("SIMILARITY_LIMIT")
				}
				if j < blo {
					continue
				}
				if j >= bhi {
					break
				}
				n := previous[j-1] + 1
				next[j] = n
				if n > best {
					besti, bestj, best = i-n+1, j-n+1, n
				}
			}
			previous = next
		}
		if best > 0 {
			matches += best
			if alo < besti && blo < bestj {
				stack = append(stack, [4]int{alo, besti, blo, bestj})
			}
			if besti+best < ahi && bestj+best < bhi {
				stack = append(stack, [4]int{besti + best, ahi, bestj + best, bhi})
			}
		}
	}
	return 2*float64(matches)/float64(len(a)+len(b)) >= threshold, nil
}
