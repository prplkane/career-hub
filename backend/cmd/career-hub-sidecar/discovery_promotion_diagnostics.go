package main

import (
	"context"
	"encoding/json"
	"time"

	"careerhub.local/backend/internal/discovery/promotiondiagnostics"
	"careerhub.local/backend/internal/sidecar/httpapi"
)

type promotionCheckpoint func(string, *httpapi.ApplicationOutcome) bool
type promotionDiagnosticWriter func(context.Context, *promotiondiagnostics.Record, promotiondiagnostics.Record) (bool, error)

// A parent snapshot cannot share the child's serial native-jobs stream: a
// completed intake is not a completed promotion. This wrapper writes only
// diagnostics; individual owners keep their existing gates and recovery rules.
func runDiagnosedPromotion(ctx context.Context, action string, write promotionDiagnosticWriter,
	run func(promotionCheckpoint) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError),
) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	record, err := promotiondiagnostics.New(action)
	if err != nil {
		return promotionFailure(nil, "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED")
	}
	observed, err := write(ctx, nil, record)
	if err != nil || !observed {
		out := &httpapi.ApplicationOutcome{}
		if observed {
			out.RunID = record.RunID
		}
		return promotionFailure(out, "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED")
	}
	logFailed := false
	checkpoint := func(stage string, out *httpapi.ApplicationOutcome) bool {
		next := promotionRecordFacts(record, out)
		next.Sequence++
		next.Stage = stage
		observed, err := write(ctx, &record, next)
		if err != nil || !observed {
			// The old or new snapshot may remain. Do not blindly replace it or
			// invoke another domain owner after an uncertain diagnostic write.
			logFailed = true
			return false
		}
		record = next
		return true
	}
	out, problem := run(checkpoint)
	out = mergePromotionFacts(out, nil)
	terminal := promotionRecordFacts(record, out)
	out.RunID = record.RunID
	if problem == nil && (out.CommitUnknown || out.Partial || out.RecoveryRequired || out.CleanupFailed) {
		out, problem = promotionFailure(out, "DISCOVERY_PROMOTION_FAILED")
	}
	if problem == nil {
		var result map[string]json.RawMessage
		if json.Unmarshal(out.Result, &result) != nil || result == nil {
			out, problem = promotionFailure(out, "LOCAL_RESPONSE_INVALID")
		} else {
			result["run_id"], _ = json.Marshal(record.RunID)
			out.Result, err = json.Marshal(result)
			if err != nil {
				out, problem = promotionFailure(out, "LOCAL_RESPONSE_INVALID")
			}
		}
	}
	if logFailed {
		return promotionFailure(out, "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED")
	}
	terminal.Sequence++
	terminal.State = "success"
	if problem != nil {
		out.Result = nil
		out.Partial = out.Partial || out.Committed || out.CommitUnknown || out.RecoveryRequired
		terminal.Facts = promotionFacts(out)
		terminal.State = "failed"
		terminal.ErrorCode = promotiondiagnostics.SafeError(problem.Code)
	} else {
		terminal.Stage = "complete"
	}
	// Cancellation stops product work, not the bounded attempt to record its
	// actual effects. No product retry is made with this detached context.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	observed, err = write(finishCtx, &record, terminal)
	if err != nil || !observed {
		return promotionFailure(out, "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED")
	}
	return out, problem
}

func promotionFacts(out *httpapi.ApplicationOutcome) promotiondiagnostics.Facts {
	if out == nil {
		return promotiondiagnostics.Facts{}
	}
	return promotiondiagnostics.Facts{Committed: out.Committed, CommitUnknown: out.CommitUnknown,
		Partial: out.Partial, RecoveryRequired: out.RecoveryRequired, CleanupFailed: out.CleanupFailed}
}

func promotionRecordFacts(record promotiondiagnostics.Record, out *httpapi.ApplicationOutcome) promotiondiagnostics.Record {
	record.Facts = promotionFacts(out)
	if out != nil && out.RunID != "" && out.RunID != record.RunID {
		record.ChildRunID = out.RunID
	}
	return record
}
