package agentvocabulary

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 2<<20 {
		return 0, fmt.Errorf("oracle output bound")
	}
	return b.Buffer.Write(p)
}

func TestExactSourceRefusalsAndPrivacy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		ctx     context.Context
		raw     []byte
		name    string
		confirm bool
	}{
		{context.Background(), []byte("Python"), "Python", false},
		{ctx, []byte("Python"), "Python", true},
		{nil, []byte("Python"), "Python", true},
		{context.Background(), []byte{0xff}, "Python", true},
		{context.Background(), []byte("Python"), string([]byte{0xff}), true},
		{context.Background(), []byte("Python"), " ", true},
		{context.Background(), []byte("Python"), strings.Repeat("a", 161), true},
		{context.Background(), []byte(strings.Repeat("x", MaxSourceBytes+1)), "Python", true},
		{context.Background(), []byte(strings.Repeat("\n", MaxSourceLines+1)), "Python", true},
	} {
		if _, e := FindExactSource(c.ctx, c.raw, c.name, nil, c.confirm); e == nil {
			t.Fatal("invalid request admitted")
		}
	}
	for _, value := range []any{Occurrence{Name: "private-marker", Line: 1}, failed("NOT_FOUND")} {
		if strings.Contains(fmt.Sprintf("%+v", value), "private-marker") {
			t.Fatal("private debug output")
		}
	}
}
