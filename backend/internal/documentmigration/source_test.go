package documentmigration

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const sourceDocumentID = "doc_11111111111111111111111111111111"

func sourceMarker(version int) []byte {
	if version == 1 {
		return []byte(`{"application":"CareerHub","channel":"dev","format_version":1}` + "\n")
	}
	return []byte(`{"application":"CareerHub","channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef","format_version":2}` + "\n")
}

func protect(content []byte) []byte {
	result := append([]byte("SYNTHETIC-DPAPI\x00"), content...)
	return result
}

func unprotect(content []byte) ([]byte, error) {
	prefix := []byte("SYNTHETIC-DPAPI\x00")
	if !strings.HasPrefix(string(content), string(prefix)) {
		return nil, errors.New("synthetic failure")
	}
	return append([]byte(nil), content[len(prefix):]...), nil
}

func sourceFixture(t *testing.T) sourceBytes {
	t.Helper()
	content := []byte("synthetic legacy document")
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	record := map[string]any{
		"id": sourceDocumentID, "title": "Legacy identity", "category": "Identity",
		"issued_on": "2026-01-01", "expires_on": "2028-01-01", "notes": "Synthetic fixture",
		"filename": "identity.pdf", "content_type": "application/pdf", "size_bytes": len(content),
		"sha256": digest(content), "created_at": now, "updated_at": now, "archived": false,
	}
	ledgerBytes, err := json.Marshal(map[string]any{"version": 1, "documents": []any{record}})
	if err != nil {
		t.Fatal(err)
	}
	return sourceBytes{
		Marker: sourceMarker(1), DocumentsRootPresent: true, Windows: true,
		LedgerPresent: true, LedgerCiphertext: protect(ledgerBytes),
		Files: map[string][]byte{sourceDocumentID + ".dpapi": protect(content)},
	}
}

func TestValidateSourceBytesBuildsReviewedPlan(t *testing.T) {
	source := sourceFixture(t)
	plan, problem := validateSourceBytes(source, unprotect)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	if plan.DocumentCount != 1 || plan.CiphertextFileCount != 2 ||
		plan.TotalCiphertextBytes != len(source.LedgerCiphertext)+len(source.Files[sourceDocumentID+".dpapi"]) {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestValidateSourceBytesPreservesEmptyAndPlatformOrder(t *testing.T) {
	for _, source := range []sourceBytes{
		{Marker: sourceMarker(1)},
		{Marker: sourceMarker(1), DocumentsRootPresent: true},
	} {
		plan, problem := validateSourceBytes(source, nil)
		if problem != nil || plan.DocumentCount != 0 || plan.CiphertextFileCount != 0 {
			t.Fatalf("empty source was refused: %+v / %#v", plan, problem)
		}
	}
	legacy := sourceFixture(t)
	legacy.Windows = false
	if plan, problem := validateSourceBytes(legacy, unprotect); plan != (Plan{}) || problem == nil || problem.Code != CodeRequiresWindows {
		t.Fatalf("non-Windows ciphertext was not refused: %+v / %#v", plan, problem)
	}
	current := sourceFixture(t)
	current.Marker = sourceMarker(2)
	if plan, problem := validateSourceBytes(current, unprotect); plan != (Plan{}) || problem == nil || problem.Code != CodeNotRequired {
		t.Fatalf("current source was not classified as complete: %+v / %#v", plan, problem)
	}
}

func TestValidateSourceBytesFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*sourceBytes)
		code   string
	}{
		{"invalid_marker", func(s *sourceBytes) { s.Marker = []byte("{}") }, CodeDataHomeInvalid},
		{"files_without_root", func(s *sourceBytes) { s.DocumentsRootPresent = false }, CodeSourceInvalid},
		{"files_without_ledger", func(s *sourceBytes) { s.LedgerPresent = false }, CodeSourceInvalid},
		{"missing_blob", func(s *sourceBytes) { s.Files = map[string][]byte{} }, CodeSourceInvalid},
		{"extra_blob", func(s *sourceBytes) { s.Files["doc_22222222222222222222222222222222.dpapi"] = protect([]byte("extra")) }, CodeSourceInvalid},
		{"bad_ledger_ciphertext", func(s *sourceBytes) { s.LedgerCiphertext = []byte("bad") }, CodeSourceInvalid},
		{"bad_blob_ciphertext", func(s *sourceBytes) { s.Files[sourceDocumentID+".dpapi"] = []byte("bad") }, CodeSourceInvalid},
		{"blob_integrity", func(s *sourceBytes) { s.Files[sourceDocumentID+".dpapi"] = protect([]byte("changed")) }, CodeSourceInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := sourceFixture(t)
			test.mutate(&source)
			plan, problem := validateSourceBytes(source, unprotect)
			if plan != (Plan{}) || problem == nil || problem.Code != test.code || problem.Error() != "document migration validation failed" {
				t.Fatalf("got %+v / %#v, want %s", plan, problem, test.code)
			}
		})
	}
}

func TestValidateSourceSnapshotOwnsAndClearsValidatedBytes(t *testing.T) {
	source := sourceFixture(t)
	marker := bytes.Clone(source.Marker)
	ledgerCiphertext := bytes.Clone(source.LedgerCiphertext)
	documentCiphertext := bytes.Clone(source.Files[sourceDocumentID+".dpapi"])
	plan, snapshot, problem := validateSourceSnapshot(
		source.Marker, true, true, source.LedgerCiphertext,
		[]string{sourceDocumentID + ".dpapi"}, true,
		func(name string) ([]byte, *Error) { return source.Files[name], nil }, unprotect,
	)
	if problem != nil || plan.DocumentCount != 1 || snapshot.Channel != "dev" ||
		!snapshot.DocumentsRootPresent || len(snapshot.Documents) != 1 ||
		snapshot.Documents[0].ID != sourceDocumentID {
		t.Fatalf("unexpected snapshot: %+v / %#v / %#v", plan, snapshot, problem)
	}
	clear(source.Marker)
	clear(source.LedgerCiphertext)
	clear(source.Files[sourceDocumentID+".dpapi"])
	if !bytes.Equal(snapshot.Marker, marker) ||
		!bytes.Equal(snapshot.LedgerCiphertext, ledgerCiphertext) ||
		!bytes.Equal(snapshot.Documents[0].Ciphertext, documentCiphertext) {
		t.Fatal("snapshot aliases source-owned ciphertext")
	}
	markerView := snapshot.Marker
	ledgerCiphertextView := snapshot.LedgerCiphertext
	ledgerPlaintextView := snapshot.LedgerPlaintext
	documentCiphertextView := snapshot.Documents[0].Ciphertext
	documentPlaintextView := snapshot.Documents[0].Plaintext
	snapshot.clear()
	if snapshot.Channel != "" || snapshot.DocumentsRootPresent || len(snapshot.Documents) != 0 {
		t.Fatalf("snapshot metadata survived clear: %#v", snapshot)
	}
	for name, content := range map[string][]byte{
		"marker": markerView, "ledger ciphertext": ledgerCiphertextView,
		"ledger plaintext": ledgerPlaintextView, "document ciphertext": documentCiphertextView,
		"document plaintext": documentPlaintextView,
	} {
		if !allZero(content) {
			t.Fatalf("%s survived clear", name)
		}
	}
}

func TestValidateSourceSnapshotClearsFailedPlaintext(t *testing.T) {
	t.Run("partial unprotect error", func(t *testing.T) {
		source := sourceFixture(t)
		partial := []byte("partial plaintext")
		plan, snapshot, problem := validateSourceSnapshot(
			source.Marker, true, true, source.LedgerCiphertext,
			[]string{sourceDocumentID + ".dpapi"}, true,
			func(name string) ([]byte, *Error) { return source.Files[name], nil },
			func([]byte) ([]byte, error) { return partial, errors.New("synthetic failure") },
		)
		if plan != (Plan{}) || problem == nil || problem.Code != CodeSourceInvalid ||
			snapshot.Channel != "" || !allZero(partial) {
			t.Fatalf("failed plaintext was retained: %+v / %#v / %q", plan, problem, partial)
		}
	})

	t.Run("document integrity failure", func(t *testing.T) {
		source := sourceFixture(t)
		var returned [][]byte
		calls := 0
		plan, snapshot, problem := validateSourceSnapshot(
			source.Marker, true, true, source.LedgerCiphertext,
			[]string{sourceDocumentID + ".dpapi"}, true,
			func(name string) ([]byte, *Error) { return source.Files[name], nil },
			func(content []byte) ([]byte, error) {
				calls++
				if calls == 1 {
					plaintext, err := unprotect(content)
					returned = append(returned, plaintext)
					return plaintext, err
				}
				plaintext := []byte("wrong size")
				returned = append(returned, plaintext)
				return plaintext, nil
			},
		)
		if plan != (Plan{}) || problem == nil || problem.Code != CodeSourceInvalid ||
			snapshot.Channel != "" || len(returned) != 2 {
			t.Fatalf("unexpected failure result: %+v / %#v / %#v", plan, problem, snapshot)
		}
		for index, plaintext := range returned {
			if !allZero(plaintext) {
				t.Fatalf("plaintext %d survived failed validation", index)
			}
		}
	})
}

func allZero(content []byte) bool {
	for _, value := range content {
		if value != 0 {
			return false
		}
	}
	return true
}
