package textcompat

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestMarkPropertiesPinned(t *testing.T) {
	values := make([]byte, 0x110000)
	for p := range values {
		if IsMark(rune(p)) {
			values[p] = 1
		}
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(values)); got != "e6fbe2b96cfd2ab74a4042a43d342474a2ec2b93b3c95c8c07e26614eef2bae7" {
		t.Fatalf("Unicode15 mark property drift: %s", got)
	}
	if IsMark(-1) || IsMark(0x110000) || IsMark(0x1e5ee) || !IsMark('\u0301') || !IsMark('\u20dd') {
		t.Fatal("mark property boundaries/version changed")
	}
}
