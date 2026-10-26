package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"careerhub.local/backend/internal/applications/applicationtexts"
	"careerhub.local/backend/internal/materials/materialwrite"
)

// Validate lexical Unicode before duplicate keys disappear. UseNumber retains
// Python's integer/float distinction and large integer limits without rounding.
func applicationTextRequest(r *http.Request) (applicationtexts.Draft, string, int) {
	fields, code, status := materialJSONChecked(r, applicationScalarJSON)
	if code != "" {
		return applicationtexts.Draft{}, code, status
	}
	payload := make(map[string]any, len(fields))
	for key, raw := range fields {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			return applicationtexts.Draft{}, "APPLICATION_TEXT_DRAFT_INVALID", http.StatusBadRequest
		}
		payload[key] = value
	}
	draft, e := applicationtexts.NormalizeDraft(payload)
	if e != nil {
		return applicationtexts.Draft{}, "APPLICATION_TEXT_DRAFT_INVALID", http.StatusBadRequest
	}
	return draft, "", http.StatusOK
}

// Closed public codes, not arbitrary internal errors or source text.
func applicationTextError(e *materialwrite.Error) (string, int) {
	switch e.Code {
	case "APPLICATION_TEXT_DRAFT_INVALID", "APPLICATION_TEXT_JOB_ID_INVALID":
		return e.Code, http.StatusBadRequest
	case "APPLICATION_TEXT_HISTORY_IMMUTABLE", "APPLICATION_TEXT_STORE_IDENTITY_INVALID",
		"APPLICATION_TEXT_STORE_INVALID", "APPLICATION_TEXT_STORE_LIMIT", "APPLICATION_TEXT_STORE_UNSUPPORTED",
		"APPLICATION_TEXT_JOB_INVALID", "APPLICATION_TEXT_APPLICATION_NOT_READY":
		return e.Code, http.StatusConflict
	case "APPLICATION_TEXT_APPLICATION_UNAVAILABLE", "APPLICATION_TEXT_STORE_UNAVAILABLE":
		return e.Code, http.StatusServiceUnavailable
	case "APPLICATION_TEXT_CANCELED":
		return e.Code, http.StatusRequestTimeout
	default:
		return materialError(e)
	}
}
