package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// serveEmailUpdateDismiss accepts exactly one of two bodies:
//
//	{"contract_version":1,"outcome_id":"…","expected_revision":N}  one update
//	{"contract_version":1,"all_unmatched":true}                     every unmatched one
func (h *handler) serveEmailUpdateDismiss(w http.ResponseWriter, r *http.Request) {
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
	request := EmailUpdateDismissRequest{}
	valid := string(fields["contract_version"]) == "1"
	switch {
	case valid && materialKeysExact(fields, "contract_version", "all_unmatched"):
		request.AllUnmatched = string(fields["all_unmatched"]) == "true"
		valid = request.AllUnmatched
	case valid && materialKeysExact(fields, "contract_version", "outcome_id", "expected_revision"):
		var ok bool
		request.ExpectedRevision, ok = materialRevision(fields["expected_revision"])
		valid = ok && request.ExpectedRevision >= 1 && string(fields["outcome_id"]) != "null" &&
			json.Unmarshal(fields["outcome_id"], &request.OutcomeID) == nil
	default:
		valid = false
	}
	if !valid {
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_APPLICATION_UPDATE_INVALID", "email_application_update_dismiss")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.EmailUpdateDismiss(ctx, request)
	})
}
