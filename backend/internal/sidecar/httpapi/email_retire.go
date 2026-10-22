package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Disconnect and forget remain preview-only even though their source
// envelopes are preserved. The native owner refuses the channel-shared v1
// credential before any retirement side effect.
func (h *handler) serveEmailRetire(w http.ResponseWriter, r *http.Request) {
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
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	if len(fields) != 2 || !materialKeys(fields, "contract_version", "expected_revision") ||
		string(fields["contract_version"]) != "2" {
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_connection_mutation")
		return
	}
	revision, ok := materialRevision(fields["expected_revision"])
	if !ok || revision < 1 {
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_connection_mutation")
		return
	}
	operation := strings.TrimPrefix(r.URL.Path, "/api/email-connection/")
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	h.applicationCallUnlocked(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.EmailRetire(ctx, operation, revision)
	})
}
