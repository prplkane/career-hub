package sourceupload

import (
	"bytes"
	"testing"
)

func TestSourceUploadDiagnosticClosedFacts(t *testing.T) {
	base := Diagnostic{SchemaVersion: 2, Operation: "source_upload", OperationID: "0123456789abcdef0123456789abcdef", RunID: "20260921T010203.000000Z_01234567", State: "started"}
	for _, d := range []Diagnostic{base,
		{2, "source_upload", base.OperationID, base.RunID, "success", "", true, true},
		{2, "source_upload", base.OperationID, base.RunID, "failed", "SOURCE_ALREADY_EXISTS", false, false},
		{2, "source_upload", base.OperationID, base.RunID, "failed", "SOURCE_UPLOAD_COMMITTED_UNVERIFIED", true, true},
		{2, "source_upload", base.OperationID, base.RunID, "unknown", "SOURCE_UPLOAD_WRITE_UNCERTAIN", true, false},
	} {
		raw, e := Encode(d)
		if e != nil {
			t.Fatal(e)
		}
		got, e := Decode(raw)
		if e != nil || got != d {
			t.Fatal("lost facts", e)
		}
		if _, e := Decode(append(bytes.Clone(raw), '\n')); e == nil {
			t.Fatal("noncanonical record accepted")
		}
		if _, e := Decode(bytes.Replace(raw, []byte(`"schema_version":2`), []byte(`"private":"content","schema_version":2`), 1)); e == nil {
			t.Fatal("private field accepted")
		}
	}
	for _, mutate := range []func(*Diagnostic){
		func(d *Diagnostic) { d.Operation = "other" }, func(d *Diagnostic) { d.State = "other" },
		func(d *Diagnostic) { d.ErrorCode = "private error text" }, func(d *Diagnostic) { d.Committed = true },
		func(d *Diagnostic) { d.State = "success" }, func(d *Diagnostic) { d.State = "unknown"; d.ErrorCode = "SOURCE_UPLOAD_WRITE_UNCERTAIN" },
		func(d *Diagnostic) { d.OperationID = "../elsewhere" }, func(d *Diagnostic) { d.RunID = "private" },
	} {
		d := base
		mutate(&d)
		if _, e := Encode(d); e == nil {
			t.Fatal("invalid diagnostic accepted")
		}
	}
}

func TestApprovalDiagnosticsRetainPartialAndNoOpFacts(t *testing.T) {
	base := Diagnostic{SchemaVersion: 2, Operation: "approval_save", OperationID: "0123456789abcdef0123456789abcdef", RunID: "20260921T010203.000000Z_01234567"}
	for _, operation := range []string{"approval_save", "approval_recover", "source_remove"} {
		for _, state := range []string{"started", "success", "failed", "unknown"} {
			d := base
			d.Operation = operation
			d.State = state
			switch state {
			case "success":
				d.CommitAttempted = true
				d.Committed = true
			case "failed":
				d.CommitAttempted = true
				d.Committed = true
				d.ErrorCode = "APPROVAL_RECOVERY_REQUIRED"
			case "unknown":
				d.CommitAttempted = true
				d.ErrorCode = "APPROVAL_WRITE_UNCERTAIN"
			}
			raw, err := Encode(d)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := Decode(raw); err != nil || got != d {
				t.Fatal("lost approval publication facts")
			}
		}
	}
	base.Operation = "approval_recover"
	base.State = "success"
	if _, err := Encode(base); err != nil {
		t.Fatal("no-op recovery refused")
	}
	base.Operation = "source_remove"
	if _, err := Encode(base); err != nil {
		t.Fatal("already removed no-op refused")
	}
	base.CommitAttempted = true
	if _, err := Encode(base); err == nil {
		t.Fatal("uncertain recovery labeled success")
	}
}
