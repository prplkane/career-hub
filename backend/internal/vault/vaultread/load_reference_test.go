package vaultread

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// Do not embed bytes.Buffer: its promoted ReadFrom would bypass the Write bound
// when os/exec copies pipe output through io.Copy.
type boundedReferenceOutput struct{ buffer bytes.Buffer }

func (b *boundedReferenceOutput) Len() int       { return b.buffer.Len() }
func (b *boundedReferenceOutput) Bytes() []byte  { return b.buffer.Bytes() }
func (b *boundedReferenceOutput) String() string { return b.buffer.String() }

func (b *boundedReferenceOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 65536 {
		return 0, errors.New("reference output exceeded bound")
	}
	return b.buffer.Write(p)
}

func TestReferenceCopyCannotBypassOutputBound(t *testing.T) {
	output := &boundedReferenceOutput{}
	reader := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 65537))}
	if _, err := io.Copy(output, reader); err == nil || output.Len() > 65536 {
		t.Fatal("reference pipe copy bypassed its byte limit")
	}
}
