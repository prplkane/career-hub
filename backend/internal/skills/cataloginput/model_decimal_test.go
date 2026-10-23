package cataloginput

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"
)

func TestDecimalTableExhaustivePython15Fingerprint(t *testing.T) {
	hash := sha256.New()
	count := 0
	var encoded [4]byte
	for value := rune(0); value <= 0x10ffff; value++ {
		if unicode15Decimal(value) {
			binary.BigEndian.PutUint32(encoded[:], uint32(value))
			hash.Write(encoded[:])
			count++
		}
	}
	if count != 680 || fmt.Sprintf("%x", hash.Sum(nil)) != "93a69f3604c97cdaa6460b7acf761f7edd20725f281233cd2a52a9dcfa38a312" {
		t.Fatalf("decimal table drift: count=%d hash=%x", count, hash.Sum(nil))
	}
}

func TestTimestampFractionPythonParseAndRegexBoundary(t *testing.T) {
	for _, fraction := range []string{"123456١", "123456789١", "123456𝟙", "123456\U00011f50"} {
		text := "2026-01-01T00:00:00." + fraction + "Z"
		terms, err := ValidateTerms(seq(baseTerm(entry("created_at", str(text)))))
		if err != nil || *terms[0].CreatedAt != text {
			t.Fatalf("valid fraction %q: %v", fraction, err)
		}
	}
	for _, fraction := range []string{"١234567", "12345١7", "123456²", "123456\U0001e5f1"} {
		_, err := ValidateTerms(seq(baseTerm(entry("created_at", str("2026-01-01T00:00:00."+fraction+"Z")))))
		if err == nil || len(err.Issues) != 1 || err.Issues[0].Type != "value_error" {
			t.Fatalf("invalid fraction %q: %+v", fraction, err)
		}
	}
}
