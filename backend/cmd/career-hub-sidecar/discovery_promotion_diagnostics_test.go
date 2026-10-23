package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"careerhub.local/backend/internal/discovery/promotiondiagnostics"
	"careerhub.local/backend/internal/sidecar/httpapi"
)

func TestPromotionDiagnosticLifecycle(t *testing.T) {
	for _, mode := range []string{"fresh", "already", "prepare_refused", "intake_failed", "link_unknown", "canceled", "invalid_result", "begin_failed", "begin_observed_error", "checkpoint_failed", "terminal_failed", "terminal_observed_error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var retained *promotiondiagnostics.Record
			writes, owners := 0, 0
			write := func(c context.Context, before *promotiondiagnostics.Record, next promotiondiagnostics.Record) (bool, error) {
				writes++
				if c.Err() != nil {
					t.Fatal("canceled context used for terminal diagnostic")
				}
				if _, err := promotiondiagnostics.Encode(next); err != nil || before != nil && (retained == nil || *before != *retained || !promotiondiagnostics.ValidTransition(*before, next)) {
					t.Fatalf("invalid diagnostic sequence: %+v %v", next, err)
				}
				if (mode == "begin_failed" && writes == 1) || (mode == "checkpoint_failed" && writes == 3) || (mode == "terminal_failed" && writes == 4) {
					return false, errors.New("synthetic log failure")
				}
				copy := next
				retained = &copy
				if mode == "begin_observed_error" && writes == 1 || mode == "terminal_observed_error" && writes == 4 {
					return true, errors.New("observed publication, failed cleanup")
				}
				return true, nil
			}
			out, problem := runDiagnosedPromotion(ctx, "save_job", write, func(checkpoint promotionCheckpoint) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				owners++
				out := &httpapi.ApplicationOutcome{}
				if mode == "prepare_refused" {
					return out, &httpapi.ApplicationError{Code: "DISCOVERY_REVISION_CONFLICT"}
				}
				phase := "intake"
				if mode == "already" {
					phase = "assessment"
				}
				if !checkpoint(phase, out) {
					return promotionFailure(out, "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED")
				}
				owners++
				if mode != "already" {
					out.Committed, out.RunID = true, "20260922T123000.123456Z_1234abcd"
				}
				if mode == "intake_failed" {
					out.RecoveryRequired = true
					return promotionFailure(out, "JOB_INTAKE_RECOVERY_REQUIRED")
				}
				if !checkpoint("link", out) {
					return promotionFailure(out, "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED")
				}
				owners++
				if mode == "link_unknown" {
					out.CommitUnknown, out.CleanupFailed, out.RecoveryRequired = true, true, true
					return promotionFailure(out, "DISCOVERY_WRITE_FAILED")
				}
				if mode == "canceled" {
					cancel()
					return promotionFailure(out, "LOCAL_OPERATION_CANCELED")
				}
				out.Result = json.RawMessage(`{"job":{"id":"fictional"},"run_id":"child"}`)
				if mode == "invalid_result" {
					out.Result = nil
				}
				return out, nil
			})
			if mode == "begin_failed" {
				if problem == nil || owners != 0 || retained != nil || out.RunID != "" {
					t.Fatal("failed log start dispatched work or invented durable run")
				}
				return
			}
			if retained == nil || out.RunID != retained.RunID || out.RunID == "20260922T123000.123456Z_1234abcd" {
				t.Fatal("parent identity lost")
			}
			if mode == "fresh" || mode == "already" {
				var result map[string]any
				if problem != nil || retained.State != "success" || json.Unmarshal(out.Result, &result) != nil || result["run_id"] != out.RunID {
					t.Fatal("parent completion not reflected in response", problem)
				}
				if (mode == "already") != (retained.ChildRunID == "" && !retained.Committed) {
					t.Fatal("no-op falsely claims intake or commit")
				}
				return
			}
			if problem == nil || out.Result != nil {
				t.Fatal("failure claimed success")
			}
			if mode == "begin_observed_error" && (owners != 0 || writes != 1) || mode == "checkpoint_failed" && (owners != 2 || writes != 3) {
				t.Fatal("diagnostic failure dispatched work or blindly retried")
			}
			if mode != "prepare_refused" && mode != "begin_observed_error" && (!out.Committed || !out.Partial) {
				t.Fatal("prior effect lost on later failure")
			}
			if mode == "link_unknown" && (!retained.Committed || !retained.CommitUnknown || !retained.RecoveryRequired || !retained.CleanupFailed || !retained.Partial || retained.State != "failed") {
				t.Fatal("prior commit and later uncertainty collapsed")
			}
		})
	}
}
