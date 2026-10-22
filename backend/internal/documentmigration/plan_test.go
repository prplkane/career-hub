package documentmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestPlanForValidatedSnapshotUsesExactSourceFingerprint(t *testing.T) {
	marker := []byte("synthetic-marker\n")
	ledger := []byte("synthetic-ledger-ciphertext")
	blob := []byte("synthetic-blob-ciphertext")
	plan := planForValidatedSnapshot(validatedSnapshot{
		MarkerSHA256: digest(marker), DocumentsRootPresent: true,
		Ledger: pointerCiphertext(observedCiphertext(ledger)),
		Documents: []validatedDocument{{
			ID: "doc_11111111111111111111111111111111", Ciphertext: observedCiphertext(blob),
		}},
	})

	canonical := []byte("{\"contract\":\"SEC-240-v1-to-v2\",\"documents_root_present\":true,\"files\":[{\"kind\":\"ledger\",\"sha256\":\"" + digest(ledger) + "\",\"size\":27},{\"kind\":\"blob\",\"object_id\":\"doc_11111111111111111111111111111111\",\"sha256\":\"" + digest(blob) + "\",\"size\":25}],\"marker_sha256\":\"" + digest(marker) + "\"}")
	wantHash := sha256.Sum256(canonical)
	wantID := "sha256:" + hex.EncodeToString(wantHash[:])
	if plan.PlanID != wantID {
		t.Fatalf("plan id = %q, want %q", plan.PlanID, wantID)
	}
	if plan.SourceFormatVersion != 1 || plan.TargetFormatVersion != 2 ||
		plan.DocumentCount != 1 || plan.CiphertextFileCount != 2 ||
		plan.TotalCiphertextBytes != len(ledger)+len(blob) {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestPlanForValidatedSnapshotDistinguishesAbsentAndPresentRoot(t *testing.T) {
	absent := planForValidatedSnapshot(validatedSnapshot{MarkerSHA256: digest([]byte("marker"))})
	present := planForValidatedSnapshot(validatedSnapshot{
		MarkerSHA256: digest([]byte("marker")), DocumentsRootPresent: true,
	})
	if absent.PlanID == present.PlanID {
		t.Fatal("documents-root presence must be bound into the reviewed plan")
	}
	if absent.DocumentCount != 0 || absent.CiphertextFileCount != 0 || absent.TotalCiphertextBytes != 0 {
		t.Fatalf("unexpected empty plan: %+v", absent)
	}
}
