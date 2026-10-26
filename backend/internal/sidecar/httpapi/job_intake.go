package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobs/jobcard"
)

// JobIntakeRequest is the fixed first-party new-JD envelope. Formatting never
// exposes the retained description or user-entered metadata.
type JobIntakeRequest struct {
	SourceText, SourceName, Company, Title string
	SourceURL, PostingID, IdempotencyKey   string
	InitialStatus                          string
}

func (JobIntakeRequest) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted job intake request]")
}

func (h *handler) serveJobIntake(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		applicationFailure(w, &ApplicationOutcome{}, "JOB_INTAKE_REQUEST_INVALID", "job_intake")
		return
	}
	fields, code, _ := materialJSONChecked(r, func(raw []byte) bool {
		return applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw)
	})
	if code != "" {
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1), "job_intake")
		return
	}
	if !materialKeys(fields, "source_text", "source_name", "company", "title", "source_url", "posting_id", "idempotency_key", "initial_status") {
		applicationFailure(w, &ApplicationOutcome{}, "JOB_INTAKE_REQUEST_INVALID", "job_intake")
		return
	}
	request := JobIntakeRequest{InitialStatus: "captured"}
	destinations := map[string]*string{
		"source_text": &request.SourceText, "source_name": &request.SourceName,
		"company": &request.Company, "title": &request.Title,
		"source_url": &request.SourceURL, "posting_id": &request.PostingID,
		"idempotency_key": &request.IdempotencyKey, "initial_status": &request.InitialStatus,
	}
	for name, destination := range destinations {
		raw, present := fields[name]
		if !present {
			if name == "initial_status" {
				continue
			}
			if name != "source_text" && name != "idempotency_key" {
				*destination = ""
				continue
			}
			applicationFailure(w, &ApplicationOutcome{}, "JOB_INTAKE_REQUEST_INVALID", "job_intake")
			return
		}
		if json.Unmarshal(raw, destination) != nil || !utf8.ValidString(*destination) || strings.ContainsRune(*destination, 0) {
			applicationFailure(w, &ApplicationOutcome{}, "JOB_INTAKE_REQUEST_INVALID", "job_intake")
			return
		}
	}
	if _, sourceErr := jobcard.IntakeDigestSource(request.SourceText); sourceErr != nil {
		applicationFailure(w, &ApplicationOutcome{}, "JOB_INTAKE_SOURCE_INVALID", "job_intake")
		return
	}
	key, keyErr := jobcard.NormalizeIntakeKey(request.IdempotencyKey)
	if keyErr != nil || key == "" || !jobcard.ValidIntakeSourceName(request.SourceName) ||
		len(request.Company) > 4096 || len(request.Title) > 4096 || len(request.SourceURL) > 65536 || len(request.PostingID) > 65536 ||
		(request.InitialStatus != "captured" && request.InitialStatus != "applied") {
		applicationFailure(w, &ApplicationOutcome{}, "JOB_INTAKE_REQUEST_INVALID", "job_intake")
		return
	}
	request.IdempotencyKey = key
	if !resumeWriteDeadline(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCallByOutcome(w, ctx, func(out *ApplicationOutcome) int {
		if out.Created {
			return http.StatusCreated
		}
		return http.StatusOK
	}, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.JobIntake(ctx, request)
	})
}
