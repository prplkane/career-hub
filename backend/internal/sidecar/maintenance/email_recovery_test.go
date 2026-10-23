package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/email/emailconnection"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/startup"
	"careerhub.local/backend/internal/storage/datahome"
)

func TestRecoveryInspectionDispatchIsReadOnlyAndStrict(t *testing.T) {
	for _, mode := range []string{"email", "job", "invalid_kind", "extra", "failure"} {
		t.Run(mode, func(t *testing.T) {
			payload := map[string]any{"command": protocol.DataHomeRecoveryInspectCommand,
				"protocol_version": 1, "data_root": fixtureDataRoot, "resource_root": fixtureResourceRoot, "channel": "dev"}
			if mode == "extra" {
				payload["confirm"] = true
			}
			called := false
			deps := processDependencies{inspect: func(ctx context.Context, start protocol.StartRecord) (datahome.RecoveryKind, error) {
				called = true
				if _, ok := ctx.Deadline(); !ok || start.Channel != "dev" {
					t.Fatal("inspection request changed")
				}
				switch mode {
				case "failure":
					return "", &startup.Error{Code: "DATA_HOME_RECOVERY_AMBIGUOUS"}
				case "job":
					return datahome.RecoveryNewJob, nil
				case "invalid_kind":
					return datahome.RecoveryApproval, nil
				default:
					return datahome.RecoveryEmailCredential, nil
				}
			}, email: func(context.Context, protocol.StartRecord) (emailconnection.Result, error) {
				t.Fatal("inspection dispatched mutation")
				return emailconnection.Result{}, nil
			}}
			raw, _ := json.Marshal(payload)
			var output bytes.Buffer
			status := runProcess(context.Background(), bytes.NewReader(append(raw, '\n')), &output, deps)
			record := decodeOutput(t, &output)
			if mode == "email" || mode == "job" {
				if status != 0 || record["event"] != "data_home_recovery_inspected" || len(record) != 3 {
					t.Fatal("invalid inspection response", record)
				}
			} else if status == 0 {
				t.Fatal("unsafe inspection accepted")
			}
			if called == (mode == "extra") {
				t.Fatal("invalid dispatch order")
			}
		})
	}
}

func TestEmailRecoveryProcessFactsAndRedaction(t *testing.T) {
	for _, mode := range []string{"success", "delete_unknown", "removed_but_error", "invalid_result", "private", "admission", "no_confirm"} {
		t.Run(mode, func(t *testing.T) {
			payload := map[string]any{"command": protocol.EmailCredentialRecoveryCommand, "protocol_version": 1,
				"data_root": fixtureDataRoot, "resource_root": fixtureResourceRoot, "channel": "dev", "confirm": mode != "no_confirm"}
			raw, _ := json.Marshal(payload)
			called := false
			deps := processDependencies{email: func(ctx context.Context, start protocol.StartRecord) (emailconnection.Result, error) {
				called = true
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 60*time.Second || start.Channel != "dev" {
					t.Fatal("unbounded or changed request")
				}
				out := emailconnection.Result{DatabaseSide: emailcredentials.BeforeCommit, Cleanup: emailcredentials.CleanupNew,
					CleanupAttempted: true, CleanupConfirmed: true, IntentRemoved: true}
				switch mode {
				case "delete_unknown":
					out.CleanupConfirmed, out.IntentRemoved = false, false
					return out, &emailconnection.Error{Code: "EMAIL_SECRET_STORE_FAILED"}
				case "removed_but_error":
					return out, &emailconnection.Error{Code: "EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED"}
				case "invalid_result":
					return emailconnection.Result{}, nil
				case "private":
					return out, errors.New("private target/account/password")
				case "admission":
					return emailconnection.Result{}, &startup.Error{Code: "DATA_HOME_IN_USE"}
				}
				return out, nil
			}}
			var output bytes.Buffer
			status := runProcess(context.Background(), bytes.NewReader(append(raw, '\n')), &output, deps)
			record := decodeOutput(t, &output)
			if strings.Contains(output.String(), "private") {
				t.Fatal("private details leaked")
			}
			if mode == "no_confirm" {
				if called || status != 3 {
					t.Fatal("unconfirmed recovery executed")
				}
				return
			}
			if !called || record["event"] != "email_credential_recovered" {
				t.Fatal("owner not dispatched")
			}
			if (status == 0) != (mode == "success") || record["success"] != (mode == "success") {
				t.Fatal("incorrect completion", record)
			}
			if mode == "delete_unknown" && (record["cleanup_attempted"] != true || record["cleanup_confirmed"] != false || record["intent_removed"] != false) {
				t.Fatal("unknown deletion concealed")
			}
			if mode == "removed_but_error" && record["intent_removed"] != true {
				t.Fatal("observed removal concealed")
			}
			if mode == "admission" && record["error_code"] != "DATA_HOME_IN_USE" {
				t.Fatal("admission code lost")
			}
		})
	}
}
