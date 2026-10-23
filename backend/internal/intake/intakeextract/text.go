package intakeextract

import (
	"bytes"
	"fmt"
	"unicode"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// ParseTXTBlocks matches Python's utf-8-sig, splitlines and line.strip
// behavior for supported TXT intake. It does not read files or accept paths.
func ParseTXTBlocks(content []byte) ([]Block, error) {
	if len(content) > 10<<20 {
		return nil, ErrLimit
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return nil, ErrInput
	}
	text := string(content)
	if len(text) >= 3 && text[:3] == "\xef\xbb\xbf" {
		text = text[3:]
	}
	lines := splitLines(text)
	if len(lines) > 10000 {
		return nil, ErrLimit
	}
	blocks := make([]Block, 0, len(lines))
	for i, line := range lines {
		trimmed := textcompat.Strip(line)
		kind := "paragraph"
		if trimmed == "" {
			kind = "blank"
		}
		blocks = append(blocks, Block{
			ID: blockID(i), Order: i, Kind: kind, Text: trimmed, IsBullet: bulletPrefix(line) > 0,
		})
	}
	return blocks, nil
}

func blockID(order int) string { return fmt.Sprintf("block_%04d", order) }

func splitLines(text string) []string {
	if text == "" {
		return []string{}
	}
	result := []string{}
	start := 0
	for offset := 0; offset < len(text); {
		point, length := utf8.DecodeRuneInString(text[offset:])
		if point == '\r' || point == '\n' || point == '\v' || point == '\f' || point == '\x1c' || point == '\x1d' || point == '\x1e' || point == '\x85' || point == '\u2028' || point == '\u2029' {
			result = append(result, text[start:offset])
			offset += length
			if point == '\r' && offset < len(text) && text[offset] == '\n' {
				offset++
			}
			start = offset
			continue
		}
		offset += length
	}
	if start < len(text) {
		result = append(result, text[start:])
	}
	return result
}

func bulletPrefix(text string) int {
	points := []rune(text)
	i := 0
	for i < len(points) && textcompat.IsSpace(points[i]) {
		i++
	}
	if i == len(points) {
		return 0
	}
	if points[i] == '-' || points[i] == '*' || points[i] == '•' || points[i] == '▪' || points[i] == '◦' {
		i++
	} else {
		start := i
		for i < len(points) && unicode.IsDigit(points[i]) {
			i++
		}
		if i == start || i == len(points) || points[i] != '.' && points[i] != ')' {
			return 0
		}
		i++
	}
	if i == len(points) || !textcompat.IsSpace(points[i]) {
		return 0
	}
	for i < len(points) && textcompat.IsSpace(points[i]) {
		i++
	}
	return i
}

func cleanBullet(text string) string {
	points := []rune(text)
	if prefix := bulletPrefix(text); prefix > 0 {
		return textcompat.Strip(string(points[prefix:]))
	}
	return textcompat.Strip(text)
}
