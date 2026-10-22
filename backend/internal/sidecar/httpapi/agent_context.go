package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"careerhub.local/backend/internal/agentcontext"
)

type AgentContextRequest struct {
	JobID, Purpose, RegistryMode string
	Task                         *agentcontext.TaskRequest
	Source                       *agentcontext.SourceRequest
}

func (h *handler) serveAgentContext(w http.ResponseWriter, r *http.Request, job string) {
	if h.application.AgentContext == nil {
		sendError(w, 404, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	invalid := func() { applicationFailure(w, &ApplicationOutcome{}, "AGENT_CONTEXT_REQUEST_INVALID") }
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		invalid()
		return
	}
	fields, code, status := materialJSONCheckedLimit(r, func(raw []byte) bool { return applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw) }, 4*agentcontext.MaxTaskJSONCharacters+1024)
	if code != "" {
		sendError(w, status, "AGENT_CONTEXT_REQUEST_INVALID", "Invalid context request")
		return
	}
	req := AgentContextRequest{JobID: job, RegistryMode: "off"}
	if !materialKeys(fields, "purpose", "registry_mode", "task_request") || json.Unmarshal(fields["purpose"], &req.Purpose) != nil || !agentcontext.ValidPurpose(req.Purpose) {
		invalid()
		return
	}
	if raw, ok := fields["registry_mode"]; ok {
		var mode *string
		if json.Unmarshal(raw, &mode) != nil || mode == nil || *mode != "off" && *mode != "exact-auto" {
			invalid()
			return
		}
		req.RegistryMode = *mode
	}
	if raw, ok := fields["task_request"]; ok {
		var e *agentcontext.Error
		req.Task, e = agentcontext.ParseTaskJSON(r.Context(), raw, req.Purpose)
		if e != nil {
			invalid()
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) { return h.application.AgentContext(ctx, req) })
}
