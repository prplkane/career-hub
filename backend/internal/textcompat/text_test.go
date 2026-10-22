package textcompat

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestNormalizationFixtures(t *testing.T) {
	for _, pair := range [][2]string{
		{" Straße ", "strasse"}, {"İ I ı i", "i\u0307 i ı i"}, {"Σςσ", "σσσ"},
		{"ﬃ", "ffi"}, {"Ꭰꭰ", "ᎠᎠ"}, {"C++ C# .NET Node.js", "c++ c# .net node.js"},
		{"\x1c VBA\u00a0\u2003SQL\x1f", "vba sql"}, {"E\u0301 É", "e\u0301 é"},
		{"\u180e\u200b\ufeff", "\u180e\u200b\ufeff"}, {"\u1c89\ua7cb", "\u1c89\ua7cb"},
		{"\t\n", ""}, {"🚀 中文", "🚀 中文"}, {"", ""},
	} {
		if got := Normalize(pair[0]); got != pair[1] {
			t.Errorf("Normalize(%q)=%q, want %q", pair[0], got, pair[1])
		}
	}
	if Strip("\x1c A  B \u3000") != "A  B" {
		t.Fatal("Strip must not fold/collapse")
	}
}

func TestGeneratedTableAndWhitespaceInvariants(t *testing.T) {
	if len(fullFolds) != 1530 {
		t.Fatal("table count drift")
	}
	expansions := 0
	for i, entry := range fullFolds {
		if i > 0 && fullFolds[i-1].source >= entry.source {
			t.Fatal("table not strictly sorted")
		}
		if !utf8.ValidString(entry.target) || entry.target == "" || entry.target == string(entry.source) {
			t.Fatal("invalid mapping")
		}
		if Fold(string(entry.source)) != entry.target {
			t.Fatal("lookup mismatch")
		}
		if utf8.RuneCountInString(entry.target) > 1 {
			expansions++
		}
	}
	if expansions != 104 {
		t.Fatal("expansion count drift")
	}
	spaces := 0
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if IsSpace(r) {
			spaces++
		}
	}
	if spaces != 29 {
		t.Fatal("whitespace count drift")
	}
}

// The same framed hash is independently computed by the pinned Python oracle.
// Every scalar, including unassigned/identity characters, is represented.
func TestExhaustiveScalarFingerprint(t *testing.T) {
	h := sha256.New()
	var word [4]byte
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue
		}
		binary.BigEndian.PutUint32(word[:], uint32(r))
		h.Write(word[:])
		value := string(r)
		for _, result := range []string{Fold(value), Strip(value), Normalize(value)} {
			binary.BigEndian.PutUint32(word[:], uint32(len(result)))
			h.Write(word[:])
			h.Write([]byte(result))
		}
		if IsSpace(r) {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
	}
	// Replaced only with independently measured Python15 evidence, not Go output.
	const expected = "15a49065d359eba87a56cb769dd7a0ed6ba79d2f4c67012fa54ccdc74c5e348f"
	if actual := fmt.Sprintf("%x", h.Sum(nil)); actual != expected {
		t.Fatalf("scalar fingerprint %s != %s", actual, expected)
	}
}

func TestConcurrentReads(t *testing.T) {
	var tasks sync.WaitGroup
	for n := 0; n < 16; n++ {
		tasks.Go(func() {
			for i := 0; i < 100; i++ {
				if Normalize(strings.Repeat("Straße\x1c", 8)) != strings.TrimSpace(strings.Repeat("strasse ", 8)) {
					t.Error("unstable normalization")
				}
			}
		})
	}
	tasks.Wait()
}
