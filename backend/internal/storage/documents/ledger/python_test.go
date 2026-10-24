package ledger

import (
	"bytes"
	"errors"
	"testing"
)

type cappedOutput struct {
	buffer bytes.Buffer
	max    int
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	if len(p) > b.max-b.buffer.Len() {
		return 0, errors.New("synthetic reference output bound")
	}
	return b.buffer.Write(p)
}

func TestReferenceOutputCap(t *testing.T) {
	buffer := &cappedOutput{max: 2}
	if n, e := buffer.Write([]byte("ab")); n != 2 || e != nil {
		t.Fatal("exact output limit")
	}
	if n, e := buffer.Write([]byte("c")); n != 0 || e == nil || buffer.buffer.String() != "ab" {
		t.Fatal("output limit bypassed")
	}
}
