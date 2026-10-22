package main

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/promotiondiagnostics"
	"careerhub.local/backend/internal/savedassessment"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

// The HTTP application gate spans the complete composition. Individual native
// phases retain their own storage gate/ownership and recovery contracts. Intake
// is durable before linking: never report a failed link as a rolled-back job.
func discoveryPromotionAction(w *readonly.Workspace, start protocol.StartRecord) func(context.Context, httpapi.FeedRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	intake := jobIntakeAction(w, start)
	return func(ctx context.Context, request httpapi.FeedRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		if !slices.Contains([]string{"save_job", "prepare_application", "record_as_applied"}, request.Action) {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "DISCOVERY_UPDATE_INVALID"}
		}
		home, err := w.StartupHome()
		if err != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE"}
		}
		write := func(ctx context.Context, before *promotiondiagnostics.Record, after promotiondiagnostics.Record) (bool, error) {
			return promotiondiagnostics.Write(ctx, start.DataRoot, home, before, after)
		}
		return runDiagnosedPromotion(ctx, request.Action, write, func(checkpoint promotionCheckpoint) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			owner := applicationwrite.New(start.DataRoot).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
			prepared, problem := owner.EditDiscovery(ctx, discovery.EditRequest{Operation: "prepare", DiscoveryID: request.ID, ExpectedRevision: request.Revision})
			out := feedWriteOutcome(prepared)
			if problem != nil {
				return out, feedWriteError(problem)
			}
			var detail discovery.Detail
			if prepared == nil || json.Unmarshal(prepared.Result, &detail) != nil || detail.DiscoveryID != request.ID {
				return promotionFailure(out, "LOCAL_RESPONSE_INVALID")
			}
			already := detail.State == "promoted"
			jobID, duplicate := detail.PromotedJobID, already
			var job map[string]any
			if !already {
				if !checkpoint("intake", out) {
					return promotionFailure(out, "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED")
				}
				url := detail.ResolvedURL
				if url == "" {
					url = detail.CanonicalURL
				}
				if url == "" {
					url = detail.ObservedURL
				}
				captured, captureErr := intake(ctx, httpapi.JobIntakeRequest{SourceText: detail.SourceText, SourceName: request.ID + ".txt",
					Company: detail.Company, Title: detail.Title, SourceURL: url, IdempotencyKey: "discovery:" + request.ID, InitialStatus: "captured"})
				out = mergePromotionFacts(out, captured)
				if captureErr != nil {
					out.Result = nil
					out.Partial = out.Partial || out.Committed || out.CommitUnknown
					return out, captureErr
				}
				var response struct {
					Job       map[string]any `json:"job"`
					Duplicate bool           `json:"duplicate"`
				}
				if captured == nil || json.Unmarshal(captured.Result, &response) != nil || response.Job == nil {
					return promotionFailure(out, "LOCAL_RESPONSE_INVALID")
				}
				job, duplicate = response.Job, response.Duplicate
				jobID, _ = job["id"].(string)
			} else {
				if !checkpoint("assessment", out) {
					return promotionFailure(out, "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED")
				}
				if !jobcard.ValidID(jobID) {
					return promotionFailure(out, "LOCAL_RESPONSE_INVALID")
				}
				saved, readErr := w.SavedJob(ctx, jobID)
				if readErr != nil || saved == nil || saved.Card["valid"] != true {
					return promotionFailure(out, "JOB_CARD_NOT_FOUND")
				}
				now := time.Now().UTC()
				assessed, assessErr := owner.AssessSaved(ctx, start.ResourceRoot, savedassessment.Request{Operation: "load", Fields: map[string]any{"job_id": jobID}, Month: assessmentcontext.Month{Year: now.Year(), Number: int(now.Month())}})
				out = mergePromotionFacts(out, feedWriteOutcome(assessed))
				if assessErr != nil {
					return promotionFailure(out, "JOB_OPERATION_FAILED")
				}
				var assessment map[string]json.RawMessage
				if assessed == nil || json.Unmarshal(assessed.Result, &assessment) != nil || len(assessment["assessment"]) == 0 {
					return promotionFailure(out, "LOCAL_RESPONSE_INVALID")
				}
				job = saved.Card
				job["assessment"] = assessment["assessment"]
			}
			if !jobcard.ValidID(jobID) {
				return promotionFailure(out, "LOCAL_RESPONSE_INVALID")
			}
			if !checkpoint("link", out) {
				return promotionFailure(out, "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED")
			}
			finished, finishErr := owner.FinalizeDiscoveryPromotion(ctx, request.ID, request.Revision, jobID, request.Action)
			out = mergePromotionFacts(out, feedWriteOutcome(finished))
			if finishErr != nil {
				out.Result = nil
				out.Partial = out.Partial || out.Committed || out.CommitUnknown
				return out, feedWriteError(finishErr)
			}
			var linked struct {
				Discovery   json.RawMessage `json:"discovery"`
				Application json.RawMessage `json:"application"`
			}
			if finished == nil || json.Unmarshal(finished.Result, &linked) != nil || len(linked.Discovery) == 0 || len(linked.Application) == 0 {
				return promotionFailure(out, "LOCAL_RESPONSE_INVALID")
			}
			job["application"] = linked.Application
			outcome := "promoted"
			if already {
				outcome = "already_promoted"
			}
			out.Result, err = json.Marshal(map[string]any{"outcome": outcome, "discovery": linked.Discovery, "job": job, "duplicate": duplicate, "run_id": out.RunID})
			if err != nil {
				return promotionFailure(out, "LOCAL_RESPONSE_INVALID")
			}
			return out, nil
		})
	}
}

func mergePromotionFacts(out, next *httpapi.ApplicationOutcome) *httpapi.ApplicationOutcome {
	if out == nil {
		out = &httpapi.ApplicationOutcome{}
	}
	if next != nil {
		out.Committed = out.Committed || next.Committed
		out.CommitUnknown = out.CommitUnknown || next.CommitUnknown
		out.Partial = out.Partial || next.Partial
		out.CleanupFailed = out.CleanupFailed || next.CleanupFailed
		out.RecoveryRequired = out.RecoveryRequired || next.RecoveryRequired
		if next.RunID != "" {
			out.RunID = next.RunID
		}
	}
	return out
}
func promotionFailure(out *httpapi.ApplicationOutcome, code string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out = mergePromotionFacts(out, nil)
	out.Result = nil
	out.Partial = out.Partial || out.Committed || out.CommitUnknown || out.RecoveryRequired
	return out, &httpapi.ApplicationError{Code: code}
}
