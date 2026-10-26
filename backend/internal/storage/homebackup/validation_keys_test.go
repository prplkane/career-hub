package homebackup

import (
	"bytes"
	"strings"
	"testing"
)

type syntheticValidationKeys struct {
	t       *testing.T
	missing bool
	reads   int
}

func (b *syntheticValidationKeys) Read(target string) ([]byte, bool, error) {
	b.reads++
	if target != "CareerHub/stable/document-key/"+testHomeID+"/key_"+strings.Repeat("a", 32) {
		b.t.Fatal("unexpected synthetic credential target")
	}
	if b.missing {
		return nil, false, nil
	}
	return bytes.Repeat([]byte{7}, 32), true, nil
}

func (b *syntheticValidationKeys) Write(string, []byte) error {
	b.t.Fatal("credential write during validation")
	return nil
}

func (b *syntheticValidationKeys) Delete(string) (bool, error) {
	b.t.Fatal("credential delete during validation")
	return false, nil
}
