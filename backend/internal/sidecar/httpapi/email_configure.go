package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// The source PUT contract is admitted only by the opt-in Go preview. It does
// not authorize a Python fallback or enable the email UI capability.
func (h *handler) serveEmailConfigure(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	const limit = 64 * 1024
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
	if len(fields) != 8 || !materialKeys(fields, "contract_version", "account_address", "app_password", "sender_addresses", "application_sender_addresses", "schedule_enabled", "interval_minutes", "expected_revision") ||
		string(fields["contract_version"]) != "2" {
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_connection_mutation")
		return
	}
	request := EmailConfigureRequest{}
	interval, intervalOK := materialRevision(fields["interval_minutes"])
	revision, revisionOK := materialRevision(fields["expected_revision"])
	if !intervalOK || interval < 15 || interval > 1440 || !revisionOK ||
		string(fields["account_address"]) == "null" ||
		string(fields["sender_addresses"]) == "null" ||
		string(fields["application_sender_addresses"]) == "null" ||
		string(fields["schedule_enabled"]) == "null" ||
		json.Unmarshal(fields["account_address"], &request.AccountAddress) != nil ||
		json.Unmarshal(fields["sender_addresses"], &request.JobSenders) != nil ||
		json.Unmarshal(fields["application_sender_addresses"], &request.ApplicationSenders) != nil ||
		json.Unmarshal(fields["schedule_enabled"], &request.ScheduleEnabled) != nil {
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_connection_mutation")
		return
	}
	if string(fields["app_password"]) != "null" {
		request.PasswordSupplied = true
		if json.Unmarshal(fields["app_password"], &request.Password) != nil {
			applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_connection_mutation")
			return
		}
	}
	request.IntervalMinutes, request.ExpectedRevision = int(interval), revision
	request.Reconnect = r.URL.Path == "/api/email-connection/reconnect"
	if request.Reconnect && (!request.PasswordSupplied || strings.TrimSpace(request.Password) == "" || revision < 1) {
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_connection_mutation")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	runID := strings.TrimSpace(r.Header.Get("X-Career-Hub-Diagnostic-Run"))
	h.applicationCallUnlocked(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.EmailConfigure(ctx, request, runID)
	})
}
