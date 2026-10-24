package readonly

import (
	"encoding/json"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/discovery/discoverypreview"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
)

type previewCase struct {
	Name, Error string
	Files       map[string]*string
	Capture     struct {
		discovery.Record
		SourceText string `json:"source_text"`
	}
	Target             json.RawMessage
	TargetReceipt      json.RawMessage `json:"target_receipt"`
	SourcePersistCalls int             `json:"source_persist_calls"`
}

func previewMonth() assessmentcontext.Month { return assessmentcontext.Month{Year: 2026, Number: 9} }
func previewJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var result any
	// Complete JSON value/shape comparison. Scalar precision/type boundaries
	// are separately qualified in the digest/evaluator and receipt suites.
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func comparePreview(t *testing.T, test previewCase, got *DiscoveryPreview, err error) {
	t.Helper()
	if test.Error != "" {
		if err == nil || got != nil {
			t.Fatal("invalid context returned a partial or clean assessment")
		}
		return
	}
	if err != nil || got == nil {
		t.Fatalf("preview unavailable: %+v", err)
	}
	raw, err := json.Marshal(got.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(previewJSON(t, raw), previewJSON(t, test.TargetReceipt)) {
		t.Fatalf("receipt differs: want %.2000s got %.2000s", test.TargetReceipt, raw)
	}
	if !discoverypreview.Eligible(test.Capture.ContentScope) {
		if got.Prepared != nil || got.Identity != nil || got.Receipt.Availability != "partial_capture" || got.Instructions.Eligible || got.Contacts.Eligible {
			t.Fatal("partial capture promoted to full assessment")
		}
		return
	}
	if got.Prepared == nil || got.Prepared.Artifact == nil || got.Identity == nil || got.Identity.Version != 2 || got.Identity.Key == "" {
		t.Fatal("incomplete fresh preview")
	}
	raw, err = json.Marshal(got.Prepared.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	actual, expected := previewJSON(t, raw).(map[string]any), previewJSON(t, test.Target).(map[string]any)
	for key, want := range expected {
		if !reflect.DeepEqual(actual[key], want) {
			t.Errorf("full artifact family %s differs: want %.2500v got %.2500v", key, want, actual[key])
		}
	}
	if len(actual) != len(expected) {
		t.Fatal("artifact family count differs")
	}
	if !reflect.DeepEqual(got.Receipt, got.Prepared.Artifact.Receipt) || !reflect.DeepEqual(got.Instructions, got.Prepared.Artifact.Instructions) || !reflect.DeepEqual(got.Contacts, got.Prepared.Artifact.Contacts) {
		t.Fatal("projections not from complete fresh artifact")
	}
}
