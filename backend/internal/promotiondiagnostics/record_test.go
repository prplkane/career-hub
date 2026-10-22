package promotiondiagnostics

import (
	"bytes"
	"testing"
)

func TestRecordClosedAndMonotonic(t *testing.T) {
	start, err := New("save_job")
	if err != nil {
		t.Fatal(err)
	}
	intake := start
	intake.Sequence, intake.Stage = 2, "intake"
	link := intake
	link.Sequence, link.Stage, link.Committed = 3, "link", true
	link.ChildRunID = "20260922T123000.123456Z_1234abcd"
	failed := link
	failed.Sequence, failed.State, failed.ErrorCode = 4, "failed", "DISCOVERY_WRITE_FAILED"
	failed.Partial, failed.CommitUnknown, failed.RecoveryRequired, failed.CleanupFailed = true, true, true, true
	for i, pair := range [][2]Record{{start, intake}, {intake, link}, {link, failed}} {
		if !ValidTransition(pair[0], pair[1]) {
			t.Fatal("valid transition rejected", i)
		}
		raw, err := Encode(pair[1])
		decoded, de := Decode(raw)
		if err != nil || de != nil || decoded != pair[1] {
			t.Fatal("roundtrip lost facts", err, de)
		}
		if _, err := Decode(append(raw, ' ')); err == nil {
			t.Fatal("noncanonical bytes accepted")
		}
		if _, err := Decode(bytes.Replace(raw, []byte(`"schema_version":3`), []byte(`"schema_version":3,"private":"content"`), 1)); err == nil {
			t.Fatal("extra field accepted")
		}
	}
	for _, mutate := range []func(*Record){
		func(r *Record) { r.Committed = false },
		func(r *Record) { r.ChildRunID = "" },
		func(r *Record) { r.Action = "record_as_applied" },
		func(r *Record) { r.OperationID = "../../private" },
		func(r *Record) { r.RunID = "20260922T123000.123456Z_1234abcd" },
		func(r *Record) { r.Sequence = 3 },
		func(r *Record) { r.State = "success"; r.Stage = "complete" },
		func(r *Record) { r.ErrorCode = "private message" },
		func(r *Record) { r.Partial = false },
	} {
		invalid := failed
		mutate(&invalid)
		if ValidTransition(link, invalid) {
			t.Fatal("invalid transition accepted", invalid)
		}
	}
	noop := link
	noop.Committed, noop.ChildRunID = false, ""
	complete := noop
	complete.Sequence, complete.State, complete.Stage = 4, "success", "complete"
	if !ValidTransition(noop, complete) {
		t.Fatal("already-linked no-op cannot complete")
	}
	if SafeError("JD text or private error") != "DISCOVERY_PROMOTION_FAILED" {
		t.Fatal("free text entered diagnostics")
	}
}
