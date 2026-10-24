// Package agentvocabulary implements the existing exact-JD vocabulary boundary.
// Source matching is not semantic approval, registry mutation or candidate evidence.
package agentvocabulary

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const MaxSourceBytes = 4 << 20
const MaxSourceLines = 200000

type Error struct{ Code string }

func (*Error) Error() string                { return "The exact JD vocabulary operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func failed(code string) *Error             { return &Error{Code: "AGENT_VOCABULARY_" + code} }

type Occurrence struct {
	Name string
	Line int
}

func (Occurrence) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted exact JD occurrence]")
}

// regexRune is Python 3.12 re.IGNORECASE's single-scalar equivalence, NOT
// casefold (which expands sharp-s/ligatures) or locale-aware display casing.
// Lower's only single-character expansion is dotted I; _sre lowers it to i.
// The remaining classes below are pinned from Python 3.12 re._casefix.
func regexRune(point rune) rune {
	if point < utf8.RuneSelf {
		if point >= 'A' && point <= 'Z' {
			return point + ('a' - 'A')
		}
		return point
	}
	if point == '\u0130' {
		return 'i'
	}
	lower, _ := utf8.DecodeRuneInString(textcompat.Lower(string(point)))
	switch lower {
	case 305:
		return 105
	case 383:
		return 115
	case 956:
		return 181
	case 953, 8126:
		return 837
	case 8147:
		return 912
	case 8163:
		return 944
	case 976:
		return 946
	case 1013:
		return 949
	case 977:
		return 952
	case 1008:
		return 954
	case 982:
		return 960
	case 1009:
		return 961
	case 963:
		return 962
	case 981:
		return 966
	case 7296:
		return 1074
	case 7297:
		return 1076
	case 7298:
		return 1086
	case 7299:
		return 1089
	case 7300, 7301:
		return 1090
	case 7302:
		return 1098
	case 7303:
		return 1123
	case 42571:
		return 7304
	case 7835:
		return 7777
	case 64262:
		return 64261
	}
	return lower
}

// FindExactSource returns only the earliest matching source line. The owner must
// separately validate the saved card, resolve its managed raw path, keep an exact
// byte witness through publication and check both catalogs. No regex supplied by
// the caller is compiled; the source's escaped literal is matched in linear time.
func FindExactSource(ctx context.Context, raw []byte, name string, sourceLine *int, confirmed bool) (Occurrence, *Error) {
	if !confirmed {
		return Occurrence{}, failed("CONFIRMATION_REQUIRED")
	}
	if ctx == nil || ctx.Err() != nil {
		return Occurrence{}, failed("CANCELED")
	}
	if len(name) > 1<<20 || !utf8.ValidString(name) {
		return Occurrence{}, failed("NAME_INVALID")
	}
	name = textcompat.Strip(name)
	if name == "" || utf8.RuneCountInString(name) > 160 {
		return Occurrence{}, failed("NAME_INVALID")
	}
	if len(raw) > MaxSourceBytes || !utf8.Valid(raw) {
		return Occurrence{}, failed("SOURCE_INVALID")
	}
	lines := textcompat.SplitLines(strings.TrimPrefix(string(raw), "\ufeff"))
	if len(lines) > MaxSourceLines {
		return Occurrence{}, failed("SOURCE_LIMIT")
	}
	if sourceLine != nil && (*sourceLine <= 0 || *sourceLine > len(lines)) {
		return Occurrence{}, failed("LINE_INVALID")
	}
	pattern := []rune(name)
	for i := range pattern {
		pattern[i] = regexRune(pattern[i])
	}
	prefix := make([]int, len(pattern))
	for i, matched := 1, 0; i < len(pattern); i++ {
		for matched > 0 && pattern[i] != pattern[matched] {
			matched = prefix[matched-1]
		}
		if pattern[i] == pattern[matched] {
			matched++
		}
		prefix[i] = matched
	}
	first, last := 0, len(lines)
	if sourceLine != nil {
		first, last = *sourceLine-1, *sourceLine
	}
	for line := first; line < last; line++ {
		if ctx.Err() != nil {
			return Occurrence{}, failed("CANCELED")
		}
		points := []rune(lines[line])
		matched := 0
		for i, point := range points {
			if i%4096 == 0 && ctx.Err() != nil {
				return Occurrence{}, failed("CANCELED")
			}
			point = regexRune(point)
			for matched > 0 && point != pattern[matched] {
				matched = prefix[matched-1]
			}
			if point == pattern[matched] {
				matched++
			}
			if matched == len(pattern) {
				start := i + 1 - len(pattern)
				if (start == 0 || !textcompat.IsWord(points[start-1])) && (i+1 == len(points) || !textcompat.IsWord(points[i+1])) {
					return Occurrence{Name: name, Line: line + 1}, nil
				}
				matched = prefix[matched-1]
			}
		}
	}
	return Occurrence{}, failed("NOT_FOUND")
}
