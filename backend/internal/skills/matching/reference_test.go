package matching

import (
	"bytes"
	"errors"
	"testing"
)

type boundedOutput struct{ bytes.Buffer }

func (out *boundedOutput) Write(value []byte) (int, error) {
	if out.Len()+len(value) > 8<<20 {
		return 0, errors.New("synthetic oracle output limit")
	}
	return out.Buffer.Write(value)
}

func verifyQuotes(t *testing.T, source string, got *Result) {
	t.Helper()
	points := []rune(source)
	verify := func(ref SourceRef, quote string) {
		if ref.Start < 0 || ref.Start >= ref.End || ref.End > len(points) || string(points[ref.Start:ref.End]) != quote {
			t.Fatal("source span is not its exact original quote")
		}
	}
	for _, item := range got.Mentions {
		verify(item.SourceRef, item.SourceText)
	}
	for _, item := range got.Issues {
		verify(item.SourceRef, item.SourceText)
	}
}
