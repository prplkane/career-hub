package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"careerhub.local/backend/internal/applicationwrite"
)

type RecordSubmissionRequest = applicationwrite.RecordSubmissionRequest

func (h *handler) serveRecordSubmission(w http.ResponseWriter, r *http.Request, jobID string) {
	if h.application.RecordSubmission == nil {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	invalid := func() { applicationFailure(w, &ApplicationOutcome{}, "AGENT_SUBMISSION_REQUEST_INVALID") }
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		invalid()
		return
	}
	fields, code, _ := materialJSONCheckedLimit(r, func(raw []byte) bool { return applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw) }, 1<<20)
	if code != "" {
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	req := RecordSubmissionRequest{JobID: jobID}
	if !materialKeys(fields, "confirmed_submitted", "resume_id", "occurred_at", "note") ||
		json.Unmarshal(fields["confirmed_submitted"], &req.ConfirmedSubmitted) != nil || !req.ConfirmedSubmitted {
		invalid()
		return
	}
	for _, optional := range []struct {
		key    string
		target **string
		limit  int
	}{
		{"resume_id", &req.ResumeID, 160}, {"occurred_at", &req.OccurredAt, 256},
	} {
		if raw, ok := fields[optional.key]; ok {
			if json.Unmarshal(raw, optional.target) != nil || *optional.target != nil && len(**optional.target) > optional.limit {
				invalid()
				return
			}
		}
	}
	if raw, ok := fields["note"]; ok {
		var note *string
		if json.Unmarshal(raw, &note) != nil || note == nil {
			invalid()
			return
		}
		req.Note = *note
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		out, p := h.application.RecordSubmission(ctx, req)
		if p != nil || out == nil || ctx.Err() != nil {
			return out, p
		}
		var receipt map[string]json.RawMessage
		if len(out.Result) > MaxResponseBytes-1024 || json.Unmarshal(out.Result, &receipt) != nil || receipt == nil {
			return out, &ApplicationError{Code: "AGENT_SUBMISSION_RESULT_INVALID"}
		}
		receipt["operation_state"], _ = json.Marshal(applicationOperationState(out))
		raw, err := json.Marshal(receipt)
		if err != nil {
			return out, &ApplicationError{Code: "AGENT_SUBMISSION_RESULT_INVALID"}
		}
		out.Result = raw
		return out, nil
	})
}
