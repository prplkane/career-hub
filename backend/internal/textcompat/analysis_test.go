package textcompat

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPinnedAnalysisProperties(t *testing.T) {
	hash := sha256.New()
	for point := rune(0); point <= 0x10ffff; point++ {
		hash.Write([]byte{analysisFlags(point)})
	}
	if hex.EncodeToString(hash.Sum(nil)) != "d6017d99ed01c04e23484b6180a093a5c1bd9b9c997832967d2275c3921d8702" {
		t.Fatal("Unicode15 analysis properties changed")
	}
	for _, point := range []rune{-1, 0x110000, 0xd800, 0x1c89, '\u0301'} {
		if IsWord(point) {
			t.Fatal("not a Python15 word character")
		}
	}
	for _, point := range []rune{'_', '²', '界', 'İ'} {
		if !IsWord(point) {
			t.Fatal("word property excludes a source character")
		}
	}
	for _, value := range []string{"", "123", "Ab", "ǅ", "\u1c89"} {
		if IsUpperString(value) {
			t.Fatal("not an uppercase heading")
		}
	}
	if !IsUpperString("Ⅻ ABC 123") {
		t.Fatal("uppercase property excludes a source character")
	}
}
func TestSplitLinesSeparators(t *testing.T) {
	cases := map[string][]string{"": {}, "\n": {""}, "a\r\nb\rc\nd\v\f\x1ce\x1df\x1eg\u0085h\u2028i\u2029j\x1fk\n": {"a", "b", "c", "d", "", "", "e", "f", "g", "h", "i", "j\x1fk"}}
	for input, want := range cases {
		if got := SplitLines(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("splitlines: %#v / %#v", got, want)
		}
	}
}
func TestNormalizationResourceAndIdentityGuards(t *testing.T) {
	for _, input := range []string{string([]byte{0xff}), strings.Repeat("x", MaxNormalizationBytes+1), strings.Repeat("ﷺ", maxDecompositionRunes/18+1)} {
		got, err := NFKC15(input)
		if got != "" || !errors.Is(err, ErrNormalizationInput) {
			t.Fatal("normalization budget/UTF8 refusal is not atomic")
		}
	}
	input := "A" + strings.Repeat("\u0301", 40)
	got, err := NFKC15(input)
	if err != nil || got != "Á"+strings.Repeat("\u0301", 39) || strings.ContainsRune(got, 0x34f) {
		t.Fatal("stream-safe insertion changed identity")
	}
	if got, err := NFKC15("\u1c89"); err != nil || got != "\u1c89" {
		t.Fatal("new Unicode assignment changed pinned input")
	}
}
