// Package materialintegrity owns deterministic export checks and the versioned
// lineage wire contract. It does not render, publish files, or infer truth.
package materialintegrity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const MaxTextBytes = 2 << 20

type Error struct{ Code string }

func (*Error) Error() string                { return "Material integrity check could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func failure(code string) *Error            { return &Error{Code: code} }
func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// TextComparison contains hashes and byte offsets only, never source prose.
// Offsets point into the original UTF-8 strings, including EOF for truncation.
type TextComparison struct {
	Match          bool   `json:"match"`
	DraftOffset    int    `json:"draft_offset"`
	ArtifactOffset int    `json:"artifact_offset"`
	ExpectedSHA256 string `json:"expected_sha256"`
	ActualSHA256   string `json:"actual_sha256"`
}

type textCursor struct {
	text    string
	index   int
	started bool
}

func (c *textCursor) next() (rune, int, bool) {
	space := c.index
	for c.index < len(c.text) {
		r, size := utf8.DecodeRuneInString(c.text[c.index:])
		if !textcompat.IsSpace(r) {
			break
		}
		c.index += size
	}
	if c.index == len(c.text) {
		return 0, c.index, false
	}
	if c.index > space && c.started {
		return ' ', space, true
	}
	start := c.index
	r, size := utf8.DecodeRuneInString(c.text[start:])
	c.index += size
	c.started = true
	return r, start, true
}

func normalizedText(text string) (string, *Error) {
	if len(text) > MaxTextBytes {
		return "", failure("MATERIAL_FINAL_TEXT_LIMIT")
	}
	if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return "", failure("MATERIAL_FINAL_TEXT_INVALID")
	}
	value := strings.Join(strings.FieldsFunc(text, textcompat.IsSpace), " ")
	if value == "" {
		return "", failure("MATERIAL_FINAL_TEXT_EMPTY")
	}
	return value, nil
}

// CompareText permits whitespace/layout changes only. Case, punctuation,
// diacritics, repetition and order remain significant. A renderer must supply
// its canonical expected projection; callers must not strip unwanted content.
func CompareText(ctx context.Context, expected, extracted string) (*TextComparison, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("MATERIAL_INTEGRITY_CANCELED")
	}
	a, e := normalizedText(expected)
	if e != nil {
		return nil, e
	}
	b, e := normalizedText(extracted)
	if e != nil {
		return nil, e
	}
	result := &TextComparison{Match: a == b, DraftOffset: -1, ArtifactOffset: -1, ExpectedSHA256: digest([]byte(a)), ActualSHA256: digest([]byte(b))}
	if result.Match {
		if ctx.Err() != nil {
			return nil, failure("MATERIAL_INTEGRITY_CANCELED")
		}
		return result, nil
	}
	left, right := textCursor{text: expected}, textCursor{text: extracted}
	for i := 0; ; i++ {
		if i%4096 == 0 && ctx.Err() != nil {
			return nil, failure("MATERIAL_INTEGRITY_CANCELED")
		}
		l, lo, lok := left.next()
		r, ro, rok := right.next()
		if lok != rok || l != r {
			result.DraftOffset, result.ArtifactOffset = lo, ro
			return result, nil
		}
		if !lok {
			return nil, failure("MATERIAL_FINAL_TEXT_COMPARISON_INVALID")
		}
	}
}
