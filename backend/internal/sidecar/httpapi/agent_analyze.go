package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"careerhub.local/backend/internal/agentcontext"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobdigest"
)

func (h *handler) serveAgentAnalyze(w http.ResponseWriter, r *http.Request) {
	if h.application.AgentAnalyze == nil {
		sendError(w, 404, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	invalid := func() { applicationFailure(w, &ApplicationOutcome{}, "AGENT_CONTEXT_REQUEST_INVALID") }
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		invalid()
		return
	}
	fields, code, status := materialJSONCheckedLimit(r, func(raw []byte) bool { return applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw) }, 6*jobdigest.MaxSourceBytes+(128<<10))
	if code != "" {
		sendError(w, status, "AGENT_CONTEXT_REQUEST_INVALID", "Invalid analysis request")
		return
	}
	if !materialKeys(fields, "job_id", "source_text", "company", "title", "source_url", "registry_mode") {
		invalid()
		return
	}
	req := AgentContextRequest{Purpose: "assessment", RegistryMode: "off"}
	values := map[string]string{}
	for key, raw := range fields {
		var value *string
		if json.Unmarshal(raw, &value) != nil || value == nil {
			invalid()
			return
		}
		values[key] = *value
	}
	if mode, ok := values["registry_mode"]; ok {
		req.RegistryMode = mode
	}
	if req.RegistryMode != "off" && req.RegistryMode != "exact-auto" {
		invalid()
		return
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
		for _, key := range []string{"company", "title", "source_url"} {
			if _, ok := values[key]; ok {
				invalid()
				return
			}
		}
		req.JobID = job
	} else {
		req.Source = &agentcontext.SourceRequest{Text: source, Company: values["company"], Title: values["title"], SourceURL: values["source_url"]}
		if agentcontext.ValidateSourceRequest(*req.Source) != nil {
			invalid()
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) { return h.application.AgentAnalyze(ctx, req) })
}
