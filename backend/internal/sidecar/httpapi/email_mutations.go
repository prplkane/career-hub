package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Only the two source-compatible edits are exposed in this preview. Neither
// route starts a mailbox worker or advertises the unfinished email UI.
func (h *handler) serveEmailMutation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery {
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
		if code == "MATERIAL_REQUEST_INVALID" && r.ContentLength > limit {
			code = "MATERIAL_REQUEST_TOO_LARGE"
		}
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	var call func(context.Context) (*ApplicationOutcome, *ApplicationError)
	if r.URL.Path == "/api/email-connection/reset-cursor" {
		if !materialKeys(fields, "contract_version", "expected_revision") || len(fields) != 2 || string(fields["contract_version"]) != "2" {
			applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_connection_mutation")
			return
		}
		revision, ok := materialRevision(fields["expected_revision"])
		if !ok || revision < 1 || h.application.EmailCursorReset == nil {
			applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_connection_mutation")
			return
		}
		call = func(ctx context.Context) (*ApplicationOutcome, *ApplicationError) {
			return h.application.EmailCursorReset(ctx, revision)
		}
	} else {
		if !materialKeys(fields, "contract_version", "sender_address", "disposition", "purpose", "expected_revision") || len(fields) != 5 || string(fields["contract_version"]) != "2" {
			applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_connection_mutation")
			return
		}
		request := EmailSenderRuleRequest{}
		var ok bool
		request.ExpectedRevision, ok = materialRevision(fields["expected_revision"])
		if !ok || request.ExpectedRevision < 1 ||
			string(fields["sender_address"]) == "null" ||
			string(fields["disposition"]) == "null" ||
			string(fields["purpose"]) == "null" ||
			json.Unmarshal(fields["sender_address"], &request.SenderAddress) != nil ||
			json.Unmarshal(fields["disposition"], &request.Disposition) != nil ||
			json.Unmarshal(fields["purpose"], &request.Purpose) != nil ||
			h.application.EmailSenderRule == nil {
			applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_connection_mutation")
			return
		}
		call = func(ctx context.Context) (*ApplicationOutcome, *ApplicationError) {
			return h.application.EmailSenderRule(ctx, request)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return call(ctx)
	})
}
