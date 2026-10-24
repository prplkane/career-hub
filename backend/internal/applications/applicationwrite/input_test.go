package applicationwrite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/storage/operationgate"
)

func TestSnapshotBoundaries(t *testing.T) {
	for _, id := range []string{"a", "..", "CON", "long:prefix", "Ω-job", "\u00a0 name \x1c"} {
		expected := trim(id)
		out, e := snapshots([]map[string]any{{"id": id}}, &sourceReads{})
		if e != nil || len(out) != 1 || out[0].JobID != expected {
			t.Fatal("valid registration ID", id, e)
		}
	}
	for _, id := range []string{".", "C:foo", "é:foo", "1:foo", "a/b", "a\\b", "a\x00b"} {
		if _, e := snapshots([]map[string]any{{"id": id}}, &sourceReads{}); e == nil || e.SourceMessage != "job.id must be a plain identifier" {
			t.Fatal("path ID", id, e)
		}
	}
	for _, test := range []struct {
		job  map[string]any
		code string
	}{
		{map[string]any{"id": "\xff"}, "APPLICATION_LEDGER_UNICODE_UNSUPPORTED"},
		{map[string]any{"id": strings.Repeat("a", applications.MaxIDBytes+1)}, "APPLICATION_LEDGER_INPUT_LIMIT"},
		{map[string]any{"id": "x", "title": strings.Repeat("t", applications.MaxCellBytes+1)}, "APPLICATION_LEDGER_INPUT_LIMIT"},
		{map[string]any{"id": "x", "title": "\xff"}, "APPLICATION_LEDGER_UNICODE_UNSUPPORTED"},
	} {
		if _, e := snapshots([]map[string]any{test.job}, &sourceReads{}); e == nil || e.Code != test.code {
			t.Fatal("bounded request", e)
		}
	}
	if _, e := snapshots(make([]map[string]any, applications.MaxRequestedIDs+1), &sourceReads{}); e == nil || e.Code != "APPLICATION_LEDGER_INPUT_LIMIT" {
		t.Fatal("item count")
	}
	duplicate := map[string]any{"id": "same", "title": strings.Repeat("a", 600000)}
	if _, e := snapshots([]map[string]any{duplicate, duplicate}, &sourceReads{}); e == nil || e.Code != "APPLICATION_LEDGER_INPUT_LIMIT" {
		t.Fatal("full duplicate input budget")
	}
	for _, raw := range []any{nil, false, 42, []any{}, "", " \t"} {
		if _, e := snapshots([]map[string]any{{"id": "x", "raw_jd_path": raw}}, &sourceReads{}); e != nil {
			t.Fatal("ignored raw path", e)
		}
	}
	if _, e := snapshots([]map[string]any{{"id": "x", "content_fingerprint": "explicit", "raw_jd_path": "../unsafe"}}, &sourceReads{}); e != nil {
		t.Fatal("explicit fingerprint touched raw source")
	}
}
func TestFingerprintByteBoundary(t *testing.T) {
	clean, e := fingerprintBytes([]byte("Hello\nWORLD Straße"))
	if e != nil {
		t.Fatal(e)
	}
	for _, text := range []string{"\ufeff Hello\r\nWORLD Straße ", "Hello\rWORLD Straße", "hello\nworld STRASSE"} {
		got, e := fingerprintBytes([]byte(text))
		if e != nil || got != clean {
			t.Fatal("fingerprint normalization", e)
		}
	}
	if _, e := fingerprintBytes([]byte{0xff}); e == nil || e.SourceMessage != rawInvalid().SourceMessage {
		t.Fatal("invalid source encoding")
	}
	if _, e := fingerprintBytes([]byte(strings.Repeat("x", maxRawBytes+1))); e == nil || e.Code != "APPLICATION_LEDGER_INPUT_LIMIT" {
		t.Fatal("raw source limit")
	}
	reads := &sourceReads{index: map[string]int{"source": 0}, files: []rawObservation{{path: "source", raw: []byte("ignored"), fingerprint: "cached"}}}
	if got, e := reads.fingerprint("source"); e != nil || got != "cached" || reads.total != 0 {
		t.Fatal("duplicate source did not reuse bounded observation")
	}
}
func TestInputErrorsBeforeHomeAccess(t *testing.T) {
	s := New("not-a-home")
	if _, e := s.SetDecision(context.Background(), "absent", "nope", false, nil); e == nil || e.SourceMessage != "decision must be one of: apply, consider, skip" {
		t.Fatal("decision order")
	}
	if _, e := s.SetStatus(context.Background(), "absent", "applied", "bad", false, nil); e == nil || e.SourceMessage != "note must be a string" {
		t.Fatal("note order")
	}
	if _, e := s.StartApplication(context.Background(), "absent", ""); e == nil || e.Stage != "input" {
		t.Fatal("time order", e)
	}
	if _, e := s.SetStatus(context.Background(), "absent", "applied", nil, nil, nil); e == nil || e.SourceMessage != "note must be a string" {
		t.Fatal("explicit null note")
	}
	if _, e := s.SetDecision(context.Background(), "absent", "apply", strings.Repeat("n", applications.MaxRequestBytes+1), nil); e == nil || e.Code != "APPLICATION_LEDGER_INPUT_LIMIT" {
		t.Fatal("note budget")
	}
	if _, e := s.SetDecision(context.Background(), "absent", "apply", strings.Repeat("n", applications.MaxRequestBytes), nil); e == nil || e.Code != "APPLICATION_LEDGER_INPUT_LIMIT" {
		t.Fatal("combined request budget")
	}
	if _, e := s.SetStatus(context.Background(), "absent", "applied", nil, strings.Repeat("n", applications.MaxRequestBytes), nil); e == nil || e.Code != "APPLICATION_LEDGER_INPUT_LIMIT" {
		t.Fatal("combined status budget")
	}
	if _, e := s.SetDecision(context.Background(), "absent", "apply", nil, "3"); e == nil || e.SourceMessage != "expected_revision must be a positive integer" {
		t.Fatal("decision revision order", e)
	}
	if _, e := s.SetStatus(context.Background(), "absent", "applied", nil, "", json.Number("0")); e == nil || e.SourceMessage != "expected_revision must be a positive integer" {
		t.Fatal("status revision order", e)
	}
	var absent *Service
	if _, e := absent.RegisterJobs(context.Background(), nil); e == nil || e.Code != "APPLICATION_LEDGER_UNAVAILABLE" {
		t.Fatal("nil service")
	}
	if _, e := s.RegisterJobs(nil, nil); e == nil || e.Code != "APPLICATION_LEDGER_UNAVAILABLE" {
		t.Fatal("nil context")
	}
}
func TestLedgerSharedGateCancellation(t *testing.T) {
	release, e := operationgate.Acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, problem := New("not-a-home").RegisterJobs(ctx, nil)
	if problem == nil || problem.Code != "APPLICATION_LEDGER_CANCELED" || out.Committed || out.CommitAttempted {
		t.Fatal("gate cancellation")
	}
}
func TestLedgerErrorFormattingHasNoPrivateText(t *testing.T) {
	e := &Error{Stage: "input", Code: "fixed", SourceClass: "ApplicationNotFound", SourceMessage: "private synthetic value"}
	for _, value := range []string{fmt.Sprint(e), fmt.Sprintf("%+v", e), fmt.Sprintf("%#v", e), fmt.Sprintf("%q", e)} {
		if strings.Contains(value, "private") {
			t.Fatal("private formatting leak")
		}
	}
}

func TestRevisionInputAdmitsOnlyPositiveIntegers(t *testing.T) {
	if revision, e := revisionInput(nil); revision != nil || e != nil {
		t.Fatal("absent expectation must stay unguarded", revision, e)
	}
	for value, want := range map[any]int64{json.Number("7"): 7, 3: 3, int64(9): 9, json.Number("9223372036854775807"): 9223372036854775807} {
		if revision, e := revisionInput(value); e != nil || revision == nil || *revision != want {
			t.Fatal(value, revision, e)
		}
	}
	for _, value := range []any{"3", json.Number("1.5"), json.Number("1e2"), json.Number("0"), json.Number("-1"), json.Number("9223372036854775808"), json.Number("x"), 0, int64(-5), 1.0, true, []any{1}} {
		revision, e := revisionInput(value)
		if revision != nil || e == nil || e.Code != "APPLICATION_LEDGER_INVALID" || e.SourceClass != "ApplicationHistoryError" || e.SourceMessage != "expected_revision must be a positive integer" {
			t.Fatalf("%#v admitted: %v %v", value, revision, e)
		}
	}
}
