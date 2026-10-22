package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/agentcontext"
	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func TestAgentPreparationStageFacts(t *testing.T) {
	for _, mode := range []string{"success", "no-op", "write-error", "unknown", "cleanup", "context-error", "context-changed", "context-cleanup", "canceled-after-write", "missing-outcome", "wrong-job", "missing-binding", "invalid-intent", "source", "canceled-before-write"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httpapi.AgentContextRequest{JobID: "synthetic", Purpose: "assessment", RegistryMode: "off"}
			if mode == "invalid-intent" {
				req.Purpose = "submit"
			}
			if mode == "source" {
				req.Source = &agentcontext.SourceRequest{Text: "JD"}
			}
			if mode == "canceled-before-write" {
				cancel()
			}
			writes, reads := 0, 0
			write := func(context.Context, string) (*applicationwrite.Outcome, *applicationwrite.Error) {
				writes++
				r := applicationwrite.SavedPreparationReceipt{JobID: "synthetic", CardSHA256: strings.Repeat("a", 64), ApplicationSHA256: strings.Repeat("b", 64), WriteReceipt: applicationwrite.PreparationWriteReceipt{Result: "prepared", Changed: true, JobReused: true, StatusAfter: "preparing"}}
				if mode == "wrong-job" {
					r.JobID = "another"
				}
				if mode == "missing-binding" {
					r.CardSHA256 = ""
				}
				raw, _ := json.Marshal(r)
				out := &applicationwrite.Outcome{Result: raw, CommitAttempted: true, Committed: true}
				switch mode {
				case "no-op":
					out.CommitAttempted, out.Committed = false, false
				case "missing-outcome":
					return nil, nil
				case "write-error":
					return &applicationwrite.Outcome{}, &applicationwrite.Error{Code: "PRIVATE_ERROR"}
				case "unknown":
					out.Committed = false
					return out, &applicationwrite.Error{Code: "PRIVATE_ERROR"}
				case "cleanup":
					out.CleanupFailed = true
					return out, &applicationwrite.Error{Code: "PRIVATE_ERROR"}
				case "canceled-after-write":
					cancel()
				}
				return out, nil
			}
			read := func(ctx context.Context, r agentcontext.SavedPacketRequest) (json.RawMessage, error) {
				reads++
				if r.JobID != req.JobID || r.Operation != "prepare" || r.Profile != "explicit" || r.Preparation == nil || r.Channel != "dev" {
					t.Fatal("unbound read")
				}
				switch mode {
				case "context-error":
					return nil, &readonly.Error{Code: "PRIVATE_ERROR"}
				case "context-changed":
					return nil, &readonly.Error{Code: "AGENT_CONTEXT_PREPARATION_CHANGED"}
				case "context-cleanup":
					return nil, &readonly.Error{Code: "AGENT_CONTEXT_CLEANUP_FAILED"}
				case "canceled-after-write":
					return nil, ctx.Err()
				}
				return json.RawMessage(`{"packet":"synthetic"}`), nil
			}
			out, e := prepareSavedAgent(ctx, req, "dev", write, read)
			if out == nil {
				t.Fatal("lost outcome")
			}
			if mode == "success" || mode == "no-op" {
				if e != nil || len(out.Result) == 0 || reads != 1 || writes != 1 || out.Partial || out.Committed != (mode == "success") {
					t.Fatal("success facts", out, e)
				}
				return
			}
			if e == nil || len(out.Result) != 0 || strings.Contains(e.Code, "PRIVATE") || writes > 1 || reads > 1 {
				t.Fatal("failure leaks/replays", out, e)
			}
			preflight := mode == "invalid-intent" || mode == "source" || mode == "canceled-before-write"
			if preflight && (writes != 0 || reads != 0) {
				t.Fatal("wrote invalid intent")
			}
			if (mode == "write-error" || mode == "unknown" || mode == "cleanup" || mode == "wrong-job" || mode == "missing-binding" || mode == "missing-outcome") && reads != 0 {
				t.Fatal("read after failed write/binding")
			}
			if !preflight && mode != "write-error" && mode != "unknown" && mode != "missing-outcome" && !out.Committed {
				t.Fatal("erased real commit")
			}
			if mode == "unknown" && !out.CommitUnknown || mode == "context-cleanup" && (!out.CleanupFailed || !out.Partial) {
				t.Fatal("erased uncertainty/cleanup")
			}
		})
	}
}
