package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (h *handler) serveEmailUpdateLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	const limit = 16 * 1024
	if r.ContentLength > limit {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_REQUEST_TOO_LARGE")
		return
	}
	fields, code, _ := materialJSONChecked(r, func(raw []byte) bool {
		return len(raw) <= limit && applicationScalarJSON(raw)
	})
	if code != "" {
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	if !materialKeys(fields, "contract_version", "outcome_id", "job_id", "expected_revision") ||
		len(fields) != 4 || string(fields["contract_version"]) != "1" {
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_APPLICATION_UPDATE_INVALID", "email_application_update_link")
		return
	}
	request := EmailUpdateLinkRequest{}
	var ok bool
	request.ExpectedRevision, ok = materialRevision(fields["expected_revision"])
	if !ok || request.ExpectedRevision < 1 ||
		string(fields["outcome_id"]) == "null" || string(fields["job_id"]) == "null" ||
		json.Unmarshal(fields["outcome_id"], &request.OutcomeID) != nil ||
		json.Unmarshal(fields["job_id"], &request.JobID) != nil {
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_APPLICATION_UPDATE_INVALID", "email_application_update_link")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.EmailUpdateLink(ctx, request)
	})
}
