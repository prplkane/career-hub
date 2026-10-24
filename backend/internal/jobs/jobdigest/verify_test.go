package jobdigest

import (
	"context"
	"testing"
)

func TestReferenceCheckIsNotACompletenessProof(t *testing.T) {
	in := fixtureInput(t)
	got, err := Analyze(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckReferences(context.Background(), in, got); err != nil {
		t.Fatal(err)
	}
	// Source references still agree after removing occurrences. Only the full
	// replay verifier proves that an externally supplied digest omitted nothing.
	// Cache callers must qualify the producer/identity/schema before reuse.
	got.Occurrences = got.Occurrences[:0]
	if err := CheckReferences(context.Background(), in, got); err != nil {
		t.Fatal(err)
	}
	if err := Verify(context.Background(), in, got); err == nil {
		t.Fatal("reference-only validation was treated as complete replay")
	}
	in.Source += "\nChanged source"
	if err := CheckReferences(context.Background(), in, got); err == nil {
		t.Fatal("changed source accepted")
	}
	if err := CheckReferences(nil, in, got); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := CheckReferences(context.Background(), in, nil); err == nil {
		t.Fatal("nil digest accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CheckReferences(ctx, in, got); err == nil {
		t.Fatal("canceled reference check succeeded")
	}
}
