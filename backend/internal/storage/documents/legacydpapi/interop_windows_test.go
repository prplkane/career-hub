//go:build windows

package legacydpapi_test

import (
	"bytes"
	"io"
	"testing"
)

type boundedOutput struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

// Do not embed bytes.Buffer: its promoted ReadFrom lets io.Copy bypass Write.
func (b *boundedOutput) Len() int      { return b.buffer.Len() }
func (b *boundedOutput) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.overflow = true
		return 0, io.ErrShortBuffer
	}
	return b.buffer.Write(p)
}

func TestNativeReferenceOutputBound(t *testing.T) {
	output := &boundedOutput{limit: 8}
	if n, err := output.Write([]byte("12345678")); n != 8 || err != nil {
		t.Fatal("exact output bound failed")
	}
	if _, err := output.Write([]byte("x")); err == nil || !output.overflow || output.Len() != 8 {
		t.Fatal("output bound not enforced")
	}
	// os/exec uses io.Copy from a pipe. Hide WriterTo here so this also catches
	// accidental promotion of a destination ReaderFrom that bypasses Write.
	copied := &boundedOutput{limit: 8}
	readerOnly := struct{ io.Reader }{bytes.NewReader([]byte("123456789"))}
	if _, err := io.Copy(copied, readerOnly); err == nil || !copied.overflow || copied.Len() > 8 {
		t.Fatal("stream copy bypassed output bound")
	}
}
