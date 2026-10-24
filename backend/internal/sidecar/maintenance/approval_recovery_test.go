package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/skills/catalogtransaction"
)

func TestApprovalRecoveryProcessPreservesFailureFacts(t *testing.T) {
	for _, mode := range []string{"success", "partial", "unknown", "unverified", "private_error", "no_confirm", "nil"} {
		t.Run(mode, func(t *testing.T) {
			payload := map[string]any{"command": protocol.ApprovalRecoveryCommand, "protocol_version": 1,
				"data_root": fixtureDataRoot, "resource_root": fixtureResourceRoot, "channel": "dev", "confirm": mode != "no_confirm"}
			raw, _ := json.Marshal(payload)
			called := false
			deps := processDependencies{approval: func(ctx context.Context, start protocol.StartRecord) (*applicationwrite.Outcome, error) {
				called = true
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing recovery deadline")
				}
				out := &applicationwrite.Outcome{CommitAttempted: true, Committed: mode != "unknown", Result: []byte(`{"private":"content"}`), RunID: "private-invalid-run-id",
					ApprovalWrite: catalogtransaction.Report{Completed: true, FilesComplete: true}}
				switch mode {
				case "partial":
					out.RecoveryRequired = true
					out.ApprovalWrite = catalogtransaction.Report{}
					return out, &applicationwrite.Error{Code: "APPROVAL_WRITE_UNCERTAIN"}
				case "unknown":
					out.RecoveryRequired = true
					out.ApprovalWrite = catalogtransaction.Report{}
					return out, &applicationwrite.Error{Code: "APPROVAL_WRITE_UNCERTAIN"}
				case "unverified":
					out.CleanupFailed = true
				case "private_error":
					return out, errors.New("private error details")
				case "nil":
					return nil, nil
				}
				return out, nil
			}}
			var output bytes.Buffer
			status := runProcess(context.Background(), bytes.NewReader(append(raw, '\n')), &output, deps)
			record := decodeOutput(t, &output)
			if strings.Contains(output.String(), "private") {
				t.Fatal("private output leaked")
			}
			if mode == "no_confirm" {
				if called || status != 3 {
					t.Fatal("unconfirmed replay executed")
				}
				return
			}
			if !called {
				t.Fatal("recovery did not execute")
			}
			if mode == "success" {
				if status != 0 || record["success"] != true || record["files_complete"] != true {
					t.Fatal("success lost", record)
				}
			} else if status != 4 {
				t.Fatal("failure exit lost", status)
			}
			if mode == "partial" || mode == "unknown" {
				if record["committed"] != (mode == "partial") || record["commit_attempted"] != true || record["recovery_required"] != true || record["success"] != false {
					t.Fatal("publication facts lost", record)
				}
			}
		})
	}
}
