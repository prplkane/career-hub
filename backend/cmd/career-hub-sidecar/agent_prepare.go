package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"careerhub.local/backend/internal/agentcontext"
	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

// Saved preparation is a two-stage operation, not an atomic context snapshot.
// The write commits before the fresh, identity-bound read. A read failure must
// never erase write facts or cause the write to be replayed. This host action
// does not itself advertise a route/capability or implement source intake.
func agentPrepareSavedAction(w *readonly.Workspace, start protocol.StartRecord) func(context.Context, httpapi.AgentContextRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	return func(ctx context.Context, req httpapi.AgentContextRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		if !w.MatchesStart(start) || w.CheckAdmission() != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "AGENT_PREPARE_UNAVAILABLE"}
		}
		home, err := w.StartupHome()
		if err != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "AGENT_PREPARE_UNAVAILABLE"}
		}
		out, problem := prepareSavedAgent(ctx, req, start.Channel,
			applicationwrite.New(start.DataRoot).PinnedToHome(home).PrepareSavedJob, w.AgentPreparedContext)
		if w.CheckAdmission() != nil {
			out.Result = nil
			out.Partial = true
			return out, &httpapi.ApplicationError{Code: "AGENT_PREPARE_UNAVAILABLE", Category: "agent_prepare"}
		}
		return out, problem
	}
}

// Private seams exercise stage transitions without inventing native commit
// evidence. Production always uses the pinned write and Workspace read above.
func prepareSavedAgent(ctx context.Context, req httpapi.AgentContextRequest, channel string,
	write func(context.Context, string) (*applicationwrite.Outcome, *applicationwrite.Error),
	read func(context.Context, agentcontext.SavedPacketRequest) (json.RawMessage, error),
) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	return prepareSavedAgentWithOrigin(ctx, req, channel, write, read, nil)
}

type agentPreparationOrigin struct {
	created bool
	before  *string
}

func prepareSavedAgentWithOrigin(ctx context.Context, req httpapi.AgentContextRequest, channel string,
	write func(context.Context, string) (*applicationwrite.Outcome, *applicationwrite.Error),
	read func(context.Context, agentcontext.SavedPacketRequest) (json.RawMessage, error),
	origin *agentPreparationOrigin,
) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	request := agentcontext.SavedPacketRequest{JobID: req.JobID, Purpose: req.Purpose, RegistryMode: req.RegistryMode, Task: req.Task, Operation: "context", Profile: "explicit", Channel: channel}
	if req.Source != nil || agentcontext.ValidateSavedPacketRequest(request) != nil {
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "AGENT_PREPARE_REQUEST_INVALID", Category: "agent_prepare"}
	}
	if ctx == nil || ctx.Err() != nil {
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "LOCAL_OPERATION_CANCELED", Category: "agent_prepare"}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	value, writeProblem := write(ctx, req.JobID)
	out, problem := jobWriteResult(value, writeProblem)
	if out == nil {
		out = &httpapi.ApplicationOutcome{}
	}
	if writeProblem != nil {
		out.Result = nil
		problem.Category = "agent_prepare"
		if writeProblem.Code == "AGENT_PREPARE_SOURCE_CHANGED" {
			problem.Code = writeProblem.Code
		}
		return out, problem
	}
	var receipt applicationwrite.SavedPreparationReceipt
	if value == nil || json.Unmarshal(value.Result, &receipt) != nil || receipt.JobID != req.JobID || value.CleanupFailed || value.RecoveryRequired || (value.CommitAttempted && !value.Committed) {
		out.Result = nil
		out.Partial = true
		return out, &httpapi.ApplicationError{Code: "AGENT_PREPARE_RECEIPT_INVALID", Category: "agent_prepare"}
	}
	request.Operation = "prepare"
	if origin != nil {
		// Whole-operation receipt: intake already registered Captured. Its
		// confirmed before-state, not that intermediate row, describes this call.
		receipt.WriteReceipt.JobCreated, receipt.WriteReceipt.JobReused = origin.created, !origin.created
		receipt.WriteReceipt.StatusBefore = origin.before
		receipt.WriteReceipt.Changed = origin.created || origin.before == nil || *origin.before != receipt.WriteReceipt.StatusAfter
		receipt.WriteReceipt.Result = "no_change_needed"
		if receipt.WriteReceipt.Changed {
			receipt.WriteReceipt.Result = "prepared"
		}
	}
	request.Preparation = &agentcontext.PreparationBinding{CardSHA256: receipt.CardSHA256, ApplicationSHA256: receipt.ApplicationSHA256}
	request.WriteReceipt, _ = json.Marshal(receipt.WriteReceipt) // Closed scalar-only struct.
	out.Result = nil                                             // Never expose the internal binding as an agent packet.
	if agentcontext.ValidateSavedPacketRequest(request) != nil {
		out.Partial = true
		return out, &httpapi.ApplicationError{Code: "AGENT_PREPARE_RECEIPT_INVALID", Category: "agent_prepare"}
	}
	packet, err := read(ctx, request)
	if err != nil {
		out.Partial = true
		code := "AGENT_PREPARE_CONTEXT_UNAVAILABLE"
		var public *readonly.Error
		if errors.As(err, &public) {
			switch public.Code {
			case "AGENT_CONTEXT_PREPARATION_CHANGED", "AGENT_CONTEXT_SOURCE_CHANGED":
				code = "AGENT_PREPARE_CONTEXT_CHANGED"
			case "AGENT_CONTEXT_CLEANUP_FAILED":
				code, out.CleanupFailed = "AGENT_PREPARE_CONTEXT_CLEANUP_FAILED", true
			}
		}
		if ctx.Err() != nil && !out.CleanupFailed {
			code = "LOCAL_OPERATION_CANCELED"
		}
		return out, &httpapi.ApplicationError{Code: code, Category: "agent_prepare"}
	}
	out.Result = packet
	return out, nil
}
