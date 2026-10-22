package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"careerhub.local/backend/internal/intakeextract"
)

type IntakeRequest struct {
	Offset                   int64
	Limit                    int
	Operation                string
	SourceID                 string
	ExpectedReviewRevision   *string
	ExpectedVaultRevision    string
	ExpectedRegistryRevision string
	EncodedName              string
	Content                  []byte
	Reimport                 bool
	SourceName               string
	Overwrite                bool
	Review                   json.RawMessage
}

// Upload names and document bytes must not leak through diagnostic formatting.
func (IntakeRequest) Format(state fmt.State, verb rune) {
	io.WriteString(state, "IntakeRequest{redacted}")
}

func intakeRoute(r *http.Request) (IntakeRequest, bool) {
	if r == nil || r.URL == nil {
		return IntakeRequest{}, false
	}
	path := r.URL.EscapedPath()
	for suffix, operation := range map[string]string{"/keep-for-applications": "review.keep", "/source": "review.remove_source"} {
		if !strings.HasPrefix(path, "/api/reviews/") || !strings.HasSuffix(path, suffix) {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/reviews/"), suffix)
		method := http.MethodPost
		if operation == "review.remove_source" {
			method = http.MethodDelete
		}
		if !validIntakeSourceID(id) || r.Method != method {
			return IntakeRequest{}, false
		}
		return IntakeRequest{Operation: operation, SourceID: id}, true
	}
	if strings.HasPrefix(path, "/api/reviews/") && strings.HasSuffix(path, "/apply-approval") && r.Method == http.MethodPost {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/reviews/"), "/apply-approval")
		if !validIntakeSourceID(id) {
			return IntakeRequest{}, false
		}
		return IntakeRequest{Operation: "review.apply", SourceID: id}, true
	}
	if strings.HasPrefix(path, "/api/reviews/") && strings.HasSuffix(path, "/approval-preview") && r.Method == http.MethodGet {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/reviews/"), "/approval-preview")
		if !validIntakeSourceID(id) {
			return IntakeRequest{}, false
		}
		return IntakeRequest{Operation: "review.preview", SourceID: id}, true
	}
	if strings.HasPrefix(path, "/api/reviews/") && r.Method == http.MethodPut {
		id := strings.TrimPrefix(path, "/api/reviews/")
		if !validIntakeSourceID(id) {
			return IntakeRequest{}, false
		}
		return IntakeRequest{Operation: "review.save", SourceID: id}, true
	}
	if path == "/api/extract" && r.Method == http.MethodPost {
		return IntakeRequest{Operation: "source.extract"}, true
	}
	if path == "/api/upload" && r.Method == http.MethodPost {
		return IntakeRequest{Operation: "source.upload"}, true
	}
	if path == "/api/upload/check" && r.Method == http.MethodPost {
		return IntakeRequest{Operation: "source.check"}, true
	}
	if path == "/api/sources" && r.Method == http.MethodGet {
		return IntakeRequest{Operation: "source.list"}, true
	}
	if path == "/api/extractions" && r.Method == http.MethodGet {
		return IntakeRequest{Operation: "extraction.list"}, true
	}
	if path == "/api/reviews/build" {
		return IntakeRequest{Operation: "review.build"}, r.Method == http.MethodPost
	}
	if strings.HasPrefix(path, "/api/reviews/") && strings.HasSuffix(path, "/suggest") {
		raw := strings.TrimSuffix(strings.TrimPrefix(path, "/api/reviews/"), "/suggest")
		if raw == "" || strings.Contains(raw, "/") || r.Method != http.MethodPost {
			return IntakeRequest{}, false
		}
		id, err := url.PathUnescape(raw)
		if err != nil || id != raw || !validIntakeSourceID(id) {
			return IntakeRequest{}, false
		}
		return IntakeRequest{Operation: "review.suggest", SourceID: id}, true
	}
	for prefix, operation := range map[string]string{
		"/api/extractions/": "extraction.read",
		"/api/reviews/":     "review.read",
	} {
		if !strings.HasPrefix(path, prefix) || r.Method != http.MethodGet {
			continue
		}
		raw := strings.TrimPrefix(path, prefix)
		if raw == "" || strings.Contains(raw, "/") {
			return IntakeRequest{}, false
		}
		id, err := url.PathUnescape(raw)
		if err != nil || id != raw || !validIntakeSourceID(id) {
			return IntakeRequest{}, false
		}
		return IntakeRequest{Operation: operation, SourceID: id}, true
	}
	return IntakeRequest{}, false
}

func (h *handler) serveIntake(w http.ResponseWriter, r *http.Request, request IntakeRequest) {
	if request.Operation == "source.list" || request.Operation == "extraction.list" {
		query, err := url.ParseQuery(r.URL.RawQuery)
		valid := err == nil
		for key, values := range query {
			if (key != "limit" && key != "cursor") || len(values) != 1 {
				valid = false
			}
		}
		limit, cursor := query.Get("limit"), query.Get("cursor")
		if limit == "" {
			limit = "20"
		}
		if cursor == "" {
			cursor = "offset:0"
		}
		request.Limit, err = strconv.Atoi(strings.TrimSpace(limit))
		valid = valid && err == nil && request.Limit >= 1 && request.Limit <= 100 && strings.HasPrefix(cursor, "offset:")
		request.Offset, err = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(cursor, "offset:")), 10, 64)
		if !valid || err != nil || request.Offset < 0 {
			sendError(w, http.StatusBadRequest, "INTAKE_QUERY_INVALID", "Invalid source pagination.")
			return
		}
	} else if r.URL.RawQuery != "" || r.URL.ForceQuery {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	if request.Operation == "source.list" || request.Operation == "extraction.list" || request.Operation == "extraction.read" || request.Operation == "review.read" || request.Operation == "review.preview" || request.Operation == "review.keep" || request.Operation == "review.remove_source" {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
			return
		}
	} else if request.Operation == "source.check" || request.Operation == "source.upload" {
		invalidCode := "SOURCE_UPLOAD_CHECK_FAILED"
		validReimport := len(r.Header.Values("X-Source-Reimport")) == 0
		if request.Operation == "source.upload" {
			invalidCode = "SOURCE_UPLOAD_FAILED"
			mode := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Source-Reimport")))
			validReimport = len(r.Header.Values("X-Source-Reimport")) <= 1 && (mode == "" || mode == "process-again")
			request.Reimport = mode == "process-again"
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/octet-stream" || len(r.Header.Values("Content-Type")) != 1 ||
			len(r.Header.Values("X-Source-Name")) != 1 || r.Header.Get("X-Source-Name") == "" ||
			!validReimport || r.ContentLength < 1 || r.ContentLength > intakeextract.MaxUploadBytes ||
			len(r.TransferEncoding) != 0 || r.Body == nil {
			applicationFailure(w, &ApplicationOutcome{}, invalidCode, "intake_upload")
			return
		}
		request.EncodedName = r.Header.Get("X-Source-Name")
		request.Content, err = io.ReadAll(io.LimitReader(r.Body, r.ContentLength+1))
		if err != nil || int64(len(request.Content)) != r.ContentLength {
			applicationFailure(w, &ApplicationOutcome{}, invalidCode, "intake_upload")
			return
		}
	} else if request.Operation == "review.apply" {
		if r.ContentLength < 1 || r.ContentLength > 16384 || len(r.TransferEncoding) != 0 || r.Body == nil {
			applicationFailure(w, &ApplicationOutcome{}, "APPROVAL_INVALID", "intake_review")
			return
		}
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(r.Body, 16385), r.Body}
		fields, code, _ := materialJSONChecked(r, func(raw []byte) bool {
			return len(raw) <= 16384 && applicationScalarJSON(raw) && uniqueIntakeBuildFields(raw)
		})
		var review string
		if code != "" || !materialKeysExact(fields, "expected_review_revision", "expected_vault_revision", "expected_registry_revision") ||
			json.Unmarshal(fields["expected_review_revision"], &review) != nil || !validSHA256Revision(review) ||
			json.Unmarshal(fields["expected_vault_revision"], &request.ExpectedVaultRevision) != nil || !validSHA256Revision(request.ExpectedVaultRevision) ||
			json.Unmarshal(fields["expected_registry_revision"], &request.ExpectedRegistryRevision) != nil || !validSHA256Revision("sha256:"+request.ExpectedRegistryRevision) {
			applicationFailure(w, &ApplicationOutcome{}, "APPROVAL_INVALID", "intake_review")
			return
		}
		request.ExpectedReviewRevision = &review
	} else if request.Operation == "review.save" {
		fields, code, _ := materialJSONChecked(r, func(raw []byte) bool { return applicationScalarJSON(raw) && uniqueIntakeBuildFields(raw) })
		var revision string
		if code != "" || !materialKeysExact(fields, "review", "expected_review_revision") || !materialJSONObject(fields["review"]) || json.Unmarshal(fields["expected_review_revision"], &revision) != nil || !validSHA256Revision(revision) {
			applicationFailure(w, &ApplicationOutcome{}, "REVIEW_UI_SAVE_FAILED", "intake_review")
			return
		}
		request.Review = fields["review"]
		request.ExpectedReviewRevision = &revision
	} else if request.Operation == "source.extract" {
		if r.ContentLength < 1 || r.ContentLength > 16384 || len(r.TransferEncoding) != 0 || r.Body == nil {
			applicationFailure(w, &ApplicationOutcome{}, "SOURCE_EXTRACTION_INVALID", "intake_extract")
			return
		}
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(r.Body, 16385), r.Body}
		fields, code, _ := materialJSONChecked(r, func(raw []byte) bool {
			return len(raw) <= 16384 && applicationScalarJSON(raw) && uniqueIntakeBuildFields(raw)
		})
		if code != "" || !materialKeysOptional(fields, []string{"source"}, "overwrite") || json.Unmarshal(fields["source"], &request.SourceName) != nil || request.SourceName == "" {
			applicationFailure(w, &ApplicationOutcome{}, "SOURCE_EXTRACTION_INVALID", "intake_extract")
			return
		}
		if raw, ok := fields["overwrite"]; ok {
			if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
				applicationFailure(w, &ApplicationOutcome{}, "SOURCE_EXTRACTION_INVALID", "intake_extract")
				return
			}
			request.Overwrite = bytes.Equal(raw, []byte("true"))
		}
	} else {
		invalidCode := "INTAKE_REVIEW_BUILD_REQUEST_INVALID"
		if request.Operation == "review.suggest" {
			invalidCode = "INTAKE_REVIEW_SUGGEST_REQUEST_INVALID"
		}
		if r.ContentLength < 0 || r.ContentLength > 16384 || len(r.TransferEncoding) != 0 {
			applicationFailure(w, &ApplicationOutcome{}, invalidCode, "intake_review")
			return
		}
		if r.Body != nil {
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.LimitReader(r.Body, 16385), r.Body}
		}
		fields, code, _ := materialJSONChecked(r, func(raw []byte) bool {
			return len(raw) <= 16384 && applicationScalarJSON(raw) && uniqueIntakeBuildFields(raw)
		})
		validFields := materialKeysOptional(fields, []string{"source_id"}, "expected_review_revision")
		if request.Operation == "review.suggest" {
			validFields = materialKeysExact(fields, "expected_review_revision")
		}
		if code != "" || !validFields {
			applicationFailure(w, &ApplicationOutcome{}, invalidCode, "intake_review")
			return
		}
		if request.Operation == "review.build" {
			if json.Unmarshal(fields["source_id"], &request.SourceID) != nil || !validIntakeSourceID(request.SourceID) {
				applicationFailure(w, &ApplicationOutcome{}, invalidCode, "intake_review")
				return
			}
		}
		if raw, present := fields["expected_review_revision"]; present && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var revision string
			if json.Unmarshal(raw, &revision) != nil || !validSHA256Revision(revision) {
				applicationFailure(w, &ApplicationOutcome{}, invalidCode, "intake_review")
				return
			}
			request.ExpectedReviewRevision = &revision
		}
		if request.Operation == "review.suggest" && request.ExpectedReviewRevision == nil {
			applicationFailure(w, &ApplicationOutcome{}, invalidCode, "intake_review")
			return
		}
	}
	timeout := 20 * time.Second
	if request.Operation == "source.extract" || request.Operation == "review.apply" {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	status := http.StatusOK
	if request.Operation == "source.upload" {
		status = http.StatusCreated
	}
	call := func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.Intake(ctx, request)
	}
	if request.Operation == "review.keep" {
		h.applicationCallByOutcome(w, ctx, func(out *ApplicationOutcome) int {
			var result struct {
				AlreadySaved *bool `json:"already_saved"`
			}
			if out == nil || json.Unmarshal(out.Result, &result) != nil || result.AlreadySaved == nil {
				return 0
			}
			if *result.AlreadySaved {
				return http.StatusOK
			}
			return http.StatusCreated
		}, call)
	} else {
		h.applicationCall(w, ctx, status, call)
	}
}

func materialKeysExact(fields map[string]json.RawMessage, required ...string) bool {
	if len(fields) != len(required) {
		return false
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	return true
}

func uniqueIntakeBuildFields(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return false
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
	}
	last, err := decoder.Token()
	return err == nil && last == json.Delim('}')
}

func materialKeysOptional(fields map[string]json.RawMessage, required []string, optional string) bool {
	if len(fields) < len(required) || len(fields) > len(required)+1 {
		return false
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	for key := range fields {
		if key != optional && !slices.Contains(required, key) {
			return false
		}
	}
	return true
}

func validIntakeSourceID(value string) bool {
	if len(value) < 5 || len(value) > 512 || !strings.HasPrefix(value, "src_") {
		return false
	}
	for _, point := range value[4:] {
		if point != '_' && (point < 'a' || point > 'z') && (point < '0' || point > '9') {
			return false
		}
	}
	return true
}
