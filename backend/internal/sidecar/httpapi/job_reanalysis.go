package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// The Go preview requires the revision the user saw. Python's legacy empty
// request remains an independent contract; this endpoint never fetches an
// unseen revision on the caller's behalf or marks an application Applied.
func (h *handler) serveJobReanalysis(w http.ResponseWriter, r *http.Request, request JobRequest) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		applicationFailure(w, &ApplicationOutcome{}, "JOB_REANALYSIS_REQUEST_INVALID")
		return
	}
	if r.ContentLength > 4096 {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_REQUEST_TOO_LARGE")
		return
	}
	if r.Body != nil {
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(r.Body, 4097), r.Body}
	}
	fields, code, _ := materialJSONChecked(r, func(raw []byte) bool {
		return len(raw) <= 4096 && applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw)
	})
	if code != "" {
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	if len(fields) != 1 || !materialKeys(fields, "expected_card_revision") ||
		json.Unmarshal(fields["expected_card_revision"], &request.ExpectedCardRevision) != nil ||
		!validSHA256Revision(request.ExpectedCardRevision) {
		applicationFailure(w, &ApplicationOutcome{}, "JOB_REANALYSIS_REQUEST_INVALID")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.Jobs(ctx, request)
	})
}
