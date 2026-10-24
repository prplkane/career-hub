package main

import (
	"context"
	"encoding/json"
	"errors"

	"careerhub.local/backend/internal/agent/agentcontext"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func agentContextAction(w *readonly.Workspace, start protocol.StartRecord) func(context.Context, httpapi.AgentContextRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	return func(ctx context.Context, req httpapi.AgentContextRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		if !w.MatchesStart(start) {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "AGENT_CONTEXT_UNAVAILABLE"}
		}
		// The explicit-root start protocol has no authoritative profile label.
		// Never infer one from a private path or accept a client-selected Home.
		var raw json.RawMessage
		var err error
		if req.Source != nil {
			if req.JobID != "" || req.Purpose != "assessment" || req.Task != nil {
				return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "AGENT_CONTEXT_REQUEST_INVALID"}
			}
			raw, err = w.AgentAnalyzeSource(ctx, agentcontext.SourcePacketRequest{Source: *req.Source, RegistryMode: req.RegistryMode, Profile: "explicit", Channel: start.Channel})
		} else {
			raw, err = w.AgentContext(ctx, agentcontext.SavedPacketRequest{JobID: req.JobID, Purpose: req.Purpose, RegistryMode: req.RegistryMode, Task: req.Task, Operation: "context", Profile: "explicit", Channel: start.Channel})
		}
		if err != nil {
			code := "AGENT_CONTEXT_UNAVAILABLE"
			var public *readonly.Error
			if errors.As(err, &public) {
				switch public.Code {
				case "AGENT_CONTEXT_SOURCE_CHANGED", "AGENT_CONTEXT_CLEANUP_FAILED", "AGENT_CONTEXT_SCHEMA_UNSUPPORTED", "AGENT_CONTEXT_JOB_CARD_NOT_FOUND", "AGENT_CONTEXT_JOB_CARD_INVALID", "AGENT_CONTEXT_EVIDENCE_VAULT_INVALID", "AGENT_CONTEXT_APPLICATION_TEXT_STORE_IDENTITY_INVALID", "AGENT_CONTEXT_POLICY_INVALID":
					code = public.Code
				}
			}
			if ctx.Err() != nil && code != "AGENT_CONTEXT_CLEANUP_FAILED" {
				code = "AGENT_CONTEXT_CANCELED"
			}
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: code}
		}
		return &httpapi.ApplicationOutcome{Result: raw}, nil
	}
}
