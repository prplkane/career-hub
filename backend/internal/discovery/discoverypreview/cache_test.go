package discoverypreview

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestCachedPreviewReevaluatesWithoutTrustingReceipt(t *testing.T) {
	in := inputFixture(t)
	fresh, err := Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "receipt", "assessment", "quote", "fingerprint", "context", "schema"} {
		t.Run(mode, func(t *testing.T) {
			var cached Prepared
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&cached); err != nil {
				t.Fatal(err)
			}
			input := inputFixture(t)
			switch mode {
			case "receipt":
				cached.Artifact.Receipt.Label = "Clean"
			case "assessment":
				cached.Artifact.Assessment.RequirementGaps = nil
			case "quote":
				cached.Digest.Occurrences[0].SourceText = "Invented"
			case "fingerprint":
				cached.Digest.Contacts.ContentSHA256 = "invalid"
			case "context":
				input.Evaluation.Index = nil
			case "schema":
				cached.Digest.Fields = nil
			}
			got, err := Restore(context.Background(), input, &cached)
			if mode == "valid" {
				if err != nil || got == nil {
					t.Fatalf("valid cached preview: %v", err)
				}
			} else if got != nil || err == nil {
				t.Fatal("invalid/stale cache accepted")
			}
		})
	}
}

func TestCachedPreviewEveryCancellationCheckpoint(t *testing.T) {
	in := inputFixture(t)
	cached, err := Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	baseline := &countContext{}
	if got, err := Restore(baseline, in, cached); err != nil || got == nil {
		t.Fatal(err)
	}
	for point := 1; point <= baseline.calls; point++ {
		if got, err := Restore(&countContext{stop: point}, in, cached); got != nil || err == nil {
			t.Fatalf("partial cached preview at checkpoint %d", point)
		}
	}
	t.Logf("verified %d cached-preview cancellation checkpoints", baseline.calls)
}
