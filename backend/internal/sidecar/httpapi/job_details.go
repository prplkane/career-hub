package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// A correction is admitted only against the exact card the Go UI displayed.
// This route is deliberately separate from the scalar application mutations.
func (h *handler) serveJobDetails(w http.ResponseWriter, r *http.Request, request JobRequest) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		applicationFailure(w, &ApplicationOutcome{}, "JOB_DETAILS_REQUEST_INVALID")
		return
	}
	if r.ContentLength > 16384 {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_REQUEST_TOO_LARGE")
		return
	}
	if r.Body != nil {
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(r.Body, 16385), r.Body}
	}
	fields, code, _ := materialJSONChecked(r, func(raw []byte) bool {
		return len(raw) <= 16384 && applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw)
	})
	if code != "" {
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	if len(fields) != 2 || !materialKeys(fields, "fields", "expected_card_revision") ||
		json.Unmarshal(fields["expected_card_revision"], &request.ExpectedCardRevision) != nil ||
		!validSHA256Revision(request.ExpectedCardRevision) {
		applicationFailure(w, &ApplicationOutcome{}, "JOB_DETAILS_REQUEST_INVALID")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(fields["fields"]))
	decoder.UseNumber()
	if decoder.Decode(&request.Fields) != nil || request.Fields == nil {
		applicationFailure(w, &ApplicationOutcome{}, "JOB_DETAILS_REQUEST_INVALID")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.Jobs(ctx, request)
	})
}

// encoding/json accepts last-key-wins objects. Refuse duplicate keys at every
// depth before decoding corrections, including hidden nested gate fields.
func uniqueJobDetailsJSON(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if !uniqueJobDetailsValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func uniqueJobDetailsValue(decoder *json.Decoder, depth int) bool {
	if depth > 64 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			name, stringKey := key.(string)
			if err != nil || !stringKey || seen[name] {
				return false
			}
			seen[name] = true
			if !uniqueJobDetailsValue(decoder, depth+1) {
				return false
			}
		}
	case '[':
		for decoder.More() {
			if !uniqueJobDetailsValue(decoder, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	end, err := decoder.Token()
	if err != nil {
		return false
	}
	if delim == '{' {
		return end == json.Delim('}')
	}
	return end == json.Delim(']')
}
