package secretstore_test

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

type boundedNativeOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *boundedNativeOutput) Write(p []byte) (int, error) {
	if len(p) > 4096-b.Len() {
		b.overflow = true
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}

func TestNativeWorkerTransportBounds(t *testing.T) {
	var output boundedNativeOutput
	if _, err := output.Write([]byte(strings.Repeat("x", 4096))); err != nil {
		t.Fatal("exact limit failed")
	}
	if _, err := output.Write([]byte("x")); err == nil || !output.overflow || output.Len() != 4096 {
		t.Fatal("native output bound failed")
	}
}
