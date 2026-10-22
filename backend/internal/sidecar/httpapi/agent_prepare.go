package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/agentcontext"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobdigest"
)

// AgentPrepareRequest is intent only. Receipts, status and recovery authority
// belong to the native owners and cannot be supplied by the caller.
type AgentPrepareRequest struct {
	AgentContextRequest
	SourceName, PostingID string
}

func (AgentPrepareRequest) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted preparation request]")
}

func (h *handler) serveAgentPrepare(w http.ResponseWriter, r *http.Request) {
	if h.application.AgentPrepare == nil {
		sendError(w, 404, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	invalid := func() { applicationFailure(w, &ApplicationOutcome{}, "AGENT_PREPARE_REQUEST_INVALID") }
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		invalid()
		return
	}
	fields, code, status := materialJSONCheckedLimit(r, func(raw []byte) bool { return applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw) }, 6*jobdigest.MaxSourceBytes+4*agentcontext.MaxTaskJSONCharacters+(128<<10))
	if code != "" {
		sendError(w, status, "AGENT_PREPARE_REQUEST_INVALID", "Invalid preparation request")
		return
	}
	if !materialKeys(fields, "job_id", "source_text", "company", "title", "source_url", "source_name", "posting_id", "purpose", "registry_mode", "task_request") {
		invalid()
		return
	}
	req := AgentPrepareRequest{AgentContextRequest: AgentContextRequest{Purpose: "assessment", RegistryMode: "off"}}
	values := map[string]string{}
	for key, raw := range fields {
		if key == "task_request" {
			continue
		}
		var value *string
		if json.Unmarshal(raw, &value) != nil || value == nil {
			invalid()
			return
		}
		values[key] = *value
	}
	if purpose, ok := values["purpose"]; ok {
		req.Purpose = purpose
	}
	if mode, ok := values["registry_mode"]; ok {
		req.RegistryMode = mode
	}
	if !agentcontext.ValidPurpose(req.Purpose) || req.RegistryMode != "off" && req.RegistryMode != "exact-auto" {
		invalid()
		return
	}
	if raw, ok := fields["task_request"]; ok {
		var problem *agentcontext.Error
		req.Task, problem = agentcontext.ParseTaskJSON(r.Context(), raw, req.Purpose)
		if problem != nil {
			invalid()
			return
		}
	}
	job, hasJob := values["job_id"]
	source, hasSource := values["source_text"]
	if hasJob == hasSource {
		invalid()
		return
	}
	if hasJob {
		if !jobcard.ValidID(job) {
			invalid()
			return
		}
		for _, key := range []string{"company", "title", "source_url", "source_name", "posting_id"} {
			if _, ok := values[key]; ok {
				invalid()
				return
			}
		}
		req.JobID = job
	} else {
		req.Source = &agentcontext.SourceRequest{Text: source, Company: values["company"], Title: values["title"], SourceURL: values["source_url"]}
		req.SourceName, req.PostingID = values["source_name"], values["posting_id"]
		if agentcontext.ValidateSourceRequest(*req.Source) != nil || !jobcard.ValidIntakeSourceName(req.SourceName) || len(req.PostingID) > 4096 || !utf8.ValidString(req.PostingID) {
			invalid()
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		out, problem := h.application.AgentPrepare(ctx, req)
		if problem != nil {
			// Preserve the owner's code/facts, but do not suggest an intake retry
			// key or a read-only retry for this composed write command.
			return out, &ApplicationError{Code: problem.Code, Category: "agent_prepare"}
		}
		return out, nil
	})
}
