package diagnosticslog

import (
	"careerhub.local/backend/internal/promotiondiagnostics"
	"careerhub.local/backend/internal/publicjobresolver"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sourceupload"
	"encoding/json"
	"testing"
	"time"
)

func TestPublicResolutionDiagnosticProjection(t *testing.T) {
	r, err := publicjobresolver.NewDiagnostic()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"running", "success", "failed"} {
		value := r
		if state != "running" {
			value.Sequence = 2
			value.State = state
			value.Committed = true
		}
		if state == "success" {
			value.Stage = "complete"
			value.Outcome = "updated"
			value.Strategy = "semantic_container"
			value.ResponseBytes = 512
		}
		if state == "failed" {
			value.Stage = "persist"
			value.ErrorCode = "PUBLIC_JOB_RESOLUTION_FAILED"
			value.Partial = true
			value.CommitUnknown = true
		}
		raw, err := publicjobresolver.EncodeDiagnostic(value)
		if err != nil {
			t.Fatal(err)
		}
		meta := runtimepaths.FileMetadata{Name: value.OperationID + ".json", Modified: time.Now(), Size: int64(len(raw))}
		projected, ok := projectNativeIntake(raw, meta)
		var event nativeIntakeEvent
		if !ok || json.Unmarshal(projected, &event) != nil || event.RunID != value.RunID || event.Component != "public_job_resolution" {
			t.Fatal("lost resolver diagnostic")
		}
		if state == "failed" && (event.Status != "error" || event.Details["committed"] != "true" || event.Details["commit_unknown"] != "true") {
			t.Fatal("lost failure facts")
		}
		meta.Name = "ffffffffffffffffffffffffffffffff.json"
		if _, ok := projectNativeIntake(raw, meta); ok {
			t.Fatal("foreign diagnostic filename accepted")
		}
	}
}

func TestPromotionDiagnosticProjection(t *testing.T) {
	r, err := promotiondiagnostics.New("save_job")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"running", "failed", "success"} {
		r.Sequence, r.State, r.Stage = 4, state, "link"
		r.Committed, r.CommitUnknown, r.Partial = true, state == "failed", state == "failed"
		r.ChildRunID = "20260922T123000.123456Z_1234abcd"
		r.ErrorCode = ""
		if state == "failed" {
			r.ErrorCode = "DISCOVERY_WRITE_FAILED"
		}
		if state == "success" {
			r.Stage = "complete"
		}
		raw, err := promotiondiagnostics.Encode(r)
		if err != nil {
			t.Fatal(err)
		}
		meta := runtimepaths.FileMetadata{Name: r.OperationID + ".json", Modified: time.Now(), Size: int64(len(raw))}
		projected, ok := projectNativeIntake(raw, meta)
		var event nativeIntakeEvent
		if !ok || json.Unmarshal(projected, &event) != nil || event.RunID != r.RunID || event.Details["child_run_id"] != r.ChildRunID || event.Details["committed"] != "true" || event.Component != "discovery_promotion" {
			t.Fatal("promotion identity/effects lost")
		}
		want := map[string]string{"running": "running", "failed": "error", "success": "success"}[state]
		if event.Status != want || state == "failed" && (event.Details["commit_unknown"] != "true" || event.Details["partial"] != "true") {
			t.Fatal("parent state collapsed into child success")
		}
		meta.Name = "ffffffffffffffffffffffffffffffff.json"
		if _, ok := projectNativeIntake(raw, meta); ok {
			t.Fatal("foreign filename admitted")
		}
	}
}

func TestSourceUploadDiagnosticProjection(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	d := sourceupload.Diagnostic{SchemaVersion: 2, Operation: "source_upload", OperationID: id, RunID: "20260921T010203.000000Z_01234567", State: "unknown", ErrorCode: "SOURCE_UPLOAD_WRITE_UNCERTAIN", CommitAttempted: true}
	raw, e := sourceupload.Encode(d)
	if e != nil {
		t.Fatal(e)
	}
	meta := runtimepaths.FileMetadata{Name: id + ".json", Modified: time.Now(), Size: int64(len(raw))}
	projected, ok := projectNativeIntake(raw, meta)
	if !ok {
		t.Fatal("valid snapshot refused")
	}
	var event nativeIntakeEvent
	if json.Unmarshal(projected, &event) != nil || event.RunID != d.RunID || event.Event != "failed" || event.Status != "error" || event.Details["publication"] != "unknown" || event.Details["committed"] != "false" {
		t.Fatal("lost diagnostic facts")
	}
	meta.Name = "ffffffffffffffffffffffffffffffff.json"
	if _, ok := projectNativeIntake(raw, meta); ok {
		t.Fatal("foreign filename accepted")
	}
}

func TestApprovalDiagnosticProjection(t *testing.T) {
	for _, operation := range []string{"approval_save", "approval_recover"} {
		d := sourceupload.Diagnostic{SchemaVersion: 2, Operation: operation, OperationID: "0123456789abcdef0123456789abcdef", RunID: "20260921T010203.000000Z_01234567", State: "failed", ErrorCode: "APPROVAL_RECOVERY_REQUIRED", CommitAttempted: true, Committed: true}
		raw, err := sourceupload.Encode(d)
		if err != nil {
			t.Fatal(err)
		}
		projected, ok := projectNativeIntake(raw, runtimepaths.FileMetadata{Name: d.OperationID + ".json", Modified: time.Now(), Size: int64(len(raw))})
		var event nativeIntakeEvent
		if !ok || json.Unmarshal(projected, &event) != nil || event.Stage != operation || event.Source != "Resume approval" || event.Status != "error" || event.Details["committed"] != "true" || event.Details["error_code"] != "APPROVAL_RECOVERY_REQUIRED" {
			t.Fatal("partial approval hidden or mislabeled")
		}
	}
}

func TestCursorRefusesMalformedAndOutOfBounds(t *testing.T) {
	for _, raw := range []string{"", "0", "byte:0", "dl1:0:ne:000001:0", "dl1:1073741824:ne:000008:16777216", "dl2:0:ne:000001:0:nj:000001:0", "dl2:1073741824:ne:000008:16777216:nj:000008:16777216", "dl3:0:ne:000001:0:nj:000001:0:ni:0:e3b0c44298fc1c149afbf4c8996fb924:0"} {
		if _, _, _, _, problem := parseCursor(raw); problem != nil {
			t.Fatalf("valid cursor %q: %v", raw, problem)
		}
	}
	for _, raw := range []string{"byte:3", "dl1:1:ne:000000:0", "dl1:1:ne:000009:0", "dl1:1073741825:ne:000001:0", "dl1:0:ne:000001:16777217", "dl1:-1:ne:000001:0", "dl1:1:ne:1:0", "dl1:1:ne:000001:0:more", "dl2:0:ne:000001:0:nj:000000:0", "dl2:0:ne:000001:0:nj:000009:0", "dl2:0:ne:000001:0:nj:000001:16777217", "dl2:0:ne:000001:0:nj:000001:0:extra", "dl3:0:ne:000001:0:nj:000001:0:ni:1:e3b0c44298fc1c149afbf4c8996fb924:2", "../../private"} {
		if _, _, _, _, problem := parseCursor(raw); problem == nil || problem.Code != "DIAGNOSTIC_CURSOR_INVALID" {
			t.Fatalf("invalid cursor %q accepted: %v", raw, problem)
		}
	}
	if runtimepaths.MaxLegacyLogBytes != 1<<30 {
		t.Fatal("legacy cursor test must follow the reader file-size limit")
	}
}
