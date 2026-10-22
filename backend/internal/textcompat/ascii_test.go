package textcompat

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestASCIIProjectionValuesOrderAndBounds(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"Résumé & Café", "Resume & Cafe"}, {"Straße Ω", "Strae "}, {"ﬃＦ𝘼¹", "ffiFA1"},
		{"A" + strings.Repeat("\u0301", 90) + "e\u0327\u0301", "Ae"},
		{"\U0001ccd6X", "X"}, {"\x00\n\tA", "\x00\n\tA"}, {"用户", ""},
	} {
		got, e := ASCIIFromNFKD15(tc.input)
		if e != nil || got != tc.want {
			t.Fatalf("projection differs: %q %v", got, e)
		}
	}
	for _, input := range []string{"\xff", strings.Repeat("x", MaxNormalizationBytes+1)} {
		value, e := ASCIIFromNFKD15(input)
		if value != "" || !errors.Is(e, ErrNormalizationInput) {
			t.Fatal("invalid input not refused atomically")
		}
	}
}

type asciiReferenceOutput struct{ bytes.Buffer }

func (o *asciiReferenceOutput) Write(p []byte) (int, error) {
	if len(p) > 4096-o.Len() {
		return 0, errors.New("synthetic ASCII reference output limit")
	}
	return o.Buffer.Write(p)
}
