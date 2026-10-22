package textcompat

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestPinnedAlphaProperty(t *testing.T) {
	hash, count := sha256.New(), 0
	for point := rune(0); point <= 0x10ffff; point++ {
		var found byte
		if IsAlpha(point) {
			found = 1
			count++
		}
		_, _ = hash.Write([]byte{found})
	}
	if count != 136104 || fmt.Sprintf("%x", hash.Sum(nil)) != "08dc43075a56661a3cdf3ad72e163b2cea2197db7f5b30bf7242c2c9351cbdb8" {
		t.Fatal("Python15 isalpha fingerprint changed")
	}
	for _, point := range []rune{-1, 0x110000, 0xd800, 0x1c89, '²', '1', '_'} {
		if IsAlpha(point) {
			t.Fatal("nonletter or unsupported scalar accepted")
		}
	}
}
