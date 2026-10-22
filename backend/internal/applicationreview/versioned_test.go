package applicationreview

import (
	"context"
	"strings"
	"testing"
)

func TestVersionedPredecodeWorkBound(t *testing.T) {
	if !versionedTokenBound(context.Background(), `{"values":[true,null,1,"é"]}`) {
		t.Fatal("valid structure")
	}
	if versionedTokenBound(context.Background(), "["+strings.Repeat("0,", 20001)+"0]") {
		t.Fatal("unbounded scalar graph")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if versionedTokenBound(ctx, "{}") || versionedTokenBound(nil, "{}") {
		t.Fatal("unbounded cancellation")
	}
}
