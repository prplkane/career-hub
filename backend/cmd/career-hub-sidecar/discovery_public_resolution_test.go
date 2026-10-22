package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"careerhub.local/backend/internal/publicjobresolver"
	"careerhub.local/backend/internal/sidecar/httpapi"
)

func TestPublicResolutionDiagnosticFailuresRetainFacts(t *testing.T) {
	for _, mode := range []string{"success", "canceled", "commit_then_error", "start_failure", "finish_failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			owners, writes := 0, 0
			var retained *publicjobresolver.Diagnostic
			write := func(c context.Context, before *publicjobresolver.Diagnostic, next publicjobresolver.Diagnostic) (bool, error) {
				writes++
				if _, err := publicjobresolver.EncodeDiagnostic(next); err != nil {
					t.Fatalf("invalid diagnostic: %+v", next)
				}
				if c.Err() != nil {
					t.Fatal("terminal used canceled context")
				}
				if mode == "start_failure" || mode == "finish_failure" && before != nil {
					return false, errors.New("synthetic diagnostic failure")
				}
				retained = &next
				return true, nil
			}
			run := func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				owners++
				out := &httpapi.ApplicationOutcome{Result: json.RawMessage(`{"outcome":"updated","resolution":{"resolved_url":"https://example.com/","strategy":"semantic_container","redirect_count":1,"response_bytes":400},"discovery":{}}`), Committed: true}
				if mode == "canceled" {
					cancel()
					return publicResolutionFailure(out, "LOCAL_OPERATION_CANCELED", "persist")
				}
				if mode == "commit_then_error" {
					out.CommitUnknown = true
					out.RecoveryRequired = true
					out.CleanupFailed = true
					return publicResolutionFailure(out, "PUBLIC_JOB_RESOLUTION_FAILED", "review")
				}
				return out, nil
			}
			out, e := runDiagnosedPublicResolution(ctx, write, run)
			if mode == "start_failure" {
				if e == nil || owners != 0 || writes != 1 || out.Committed {
					t.Fatal("diagnostic refusal dispatched work")
				}
				return
			}
			if owners != 1 || writes != 2 || !out.Committed || out.RunID == "" {
				t.Fatal("lost operation identity/effects", out, e)
			}
			if mode == "success" {
				if e != nil || retained.State != "success" || retained.Strategy != "semantic_container" {
					t.Fatal(out, e, retained)
				}
				return
			}
			if e == nil || !out.Partial || out.Result != nil {
				t.Fatal("failed response reported success", out, e)
			}
			if mode == "commit_then_error" && (!out.CommitUnknown || !out.RecoveryRequired || !out.CleanupFailed || retained.State != "failed") {
				t.Fatal("lost uncertain write facts")
			}
		})
	}
}
