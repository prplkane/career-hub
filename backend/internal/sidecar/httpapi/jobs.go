package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"careerhub.local/backend/internal/applicationcontacts"
	"careerhub.local/backend/internal/contactcandidate"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/resumeplan"
)

// JobRequest selects a closed first-party workflow; no raw file/SQL access or
// extension submission capability can be selected through request fields.
type JobRequest struct {
	Operation, ID        string
	ExpectedCardRevision string
	Offset               int64
	Limit                int
	Fields               map[string]any
	Plan                 *resumeplan.Request
	ContactCandidate     *contactcandidate.SourceRequest
	ContactConfirmation  *contactcandidate.ConfirmationRequest
	ContactUpdates       []applicationcontacts.FactChange
	Content              []byte
}

func (JobRequest) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted job request]") }

func jobRoute(r *http.Request) (JobRequest, bool) {
	if r.URL == nil {
		return JobRequest{}, false
	}
	path := r.URL.EscapedPath()
	if path == "/api/agent/prepare" && r.Method == "POST" {
		return JobRequest{Operation: "agent.prepare"}, true
	}
	if path == "/api/agent/analyze" && r.Method == "POST" {
		return JobRequest{Operation: "agent.analyze"}, true
	}
	if request, ok := contactRoute(r); ok {
		return request, true
	}
	if request, ok := resumeRoute(r); ok {
		return request, true
	}
	if path == "/api/jobs" {
		switch r.Method {
		case "GET":
			return JobRequest{Operation: "list"}, true
		case "POST":
			return JobRequest{Operation: "intake"}, true
		}
	}
	if path == "/api/resumes" {
		switch r.Method {
		case "GET":
			return JobRequest{Operation: "resume.list"}, true
		case "POST":
			return JobRequest{Operation: "resume.upload"}, true
		}
	}
	if path == "/api/application-sessions/reviews" && r.Method == "GET" {
		return JobRequest{Operation: "session.reviews"}, true
	}
	if strings.HasPrefix(path, "/api/application-sessions/") && r.Method == "POST" {
		parts := strings.Split(strings.TrimPrefix(path, "/api/application-sessions/"), "/")
		if len(parts) == 2 && (parts[1] == "confirm" || parts[1] == "dismiss" || parts[1] == "cancel") {
			id, e := url.PathUnescape(parts[0])
			if e == nil && jobcard.ValidID(id) {
				return JobRequest{Operation: "session." + parts[1], ID: id}, true
			}
		}
		return JobRequest{}, false
	}
	if !strings.HasPrefix(path, "/api/jobs/") {
		return JobRequest{}, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/jobs/"), "/")
	if len(parts) < 1 || len(parts) > 4 {
		return JobRequest{}, false
	}
	id, e := url.PathUnescape(parts[0])
	if e != nil || !jobcard.ValidID(id) {
		return JobRequest{}, false
	}
	if len(parts) == 1 {
		return JobRequest{Operation: "detail", ID: id}, r.Method == "GET"
	}
	if len(parts) == 4 {
		if r.Method == "GET" && strings.Join(parts[1:], "/") == "application/resume/file" {
			return JobRequest{Operation: "resume.attachment_file", ID: id}, true
		}
		if r.Method == "GET" && strings.Join(parts[1:], "/") == "application/submitted-resume/file" {
			return JobRequest{Operation: "resume.submitted_file", ID: id}, true
		}
		return JobRequest{}, false
	}
	op := map[string]string{
		"GET application": "application", "GET assessment": "assessment", "GET source": "source",
		"PUT details":                        "details.edit",
		"POST reanalyze":                     "reanalyze",
		"POST vocabulary/exact":              "vocabulary.exact",
		"POST application/record-submission": "submission.record",
		"POST agent-context":                 "agent.context",
		"POST assessment/resolution":         "assessment.resolve",
		"PUT application/decision":           "decision", "POST application/status": "status", "POST application/start": "start",
		"POST application/resume":           "resume.attach",
		"POST application/resume-selection": "resume.select", "POST application/submitted-resume": "resume.submitted",
		"GET application-session": "session.latest", "POST application-session": "session.create",
		"GET application-instructions": "checklist.load", "POST application-instructions/manual": "checklist.add_manual", "POST application-instructions/action": "checklist.set_state",
		"GET resume-plan": "plan.load", "POST resume-plan": "plan.save",
	}[r.Method+" "+strings.Join(parts[1:], "/")]
	return JobRequest{Operation: op, ID: id}, op != ""
}

func resumeRoute(r *http.Request) (JobRequest, bool) {
	path := r.URL.EscapedPath()
	if !strings.HasPrefix(path, "/api/resumes/") {
		return JobRequest{}, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/resumes/"), "/")
	if len(parts) != 1 && len(parts) != 2 {
		return JobRequest{}, false
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil || !jobcard.ValidID(id) {
		return JobRequest{}, false
	}
	if len(parts) == 1 {
		return JobRequest{Operation: "resume.delete", ID: id}, r.Method == "DELETE"
	}
	op := map[string]string{
		"GET file": "resume.file", "POST archive": "resume.archive", "POST restore": "resume.restore", "POST digest": "resume.digest",
	}[r.Method+" "+parts[1]]
	return JobRequest{Operation: op, ID: id}, op != ""
}

func (h *handler) serveJob(w http.ResponseWriter, r *http.Request, request JobRequest) {
	if request.Operation == "agent.prepare" {
		h.serveAgentPrepare(w, r)
		return
	}
	if request.Operation == "agent.context" {
		h.serveAgentContext(w, r, request.ID)
		return
	}
	if request.Operation == "agent.analyze" {
		h.serveAgentAnalyze(w, r)
		return
	}
	if request.Operation == "contact.candidate_preview" || request.Operation == "contact.candidate_confirm" {
		h.serveContactCandidate(w, r, request)
		return
	}
	if request.Operation == "intake" {
		h.serveJobIntake(w, r)
		return
	}
	if request.Operation == "details.edit" {
		h.serveJobDetails(w, r, request)
		return
	}
	if request.Operation == "reanalyze" {
		h.serveJobReanalysis(w, r, request)
		return
	}
	if request.Operation == "vocabulary.exact" {
		h.serveExactVocabulary(w, r, request.ID)
		return
	}
	if request.Operation == "submission.record" {
		h.serveRecordSubmission(w, r, request.ID)
		return
	}
	if request.Operation == "detail" && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
		applicationFailure(w, &ApplicationOutcome{}, "JOB_QUERY_INVALID")
		return
	}
	if strings.HasPrefix(request.Operation, "contact.") || strings.HasPrefix(request.Operation, "plan.") {
		h.serveJobEditor(w, r, request)
		return
	}
	request.Fields = map[string]any{}
	if request.Operation == "resume.upload" || request.Operation == "resume.attach" {
		h.serveResumeUpload(w, r, request)
		return
	}
	if request.Operation == "resume.file" || request.Operation == "resume.submitted_file" || request.Operation == "resume.attachment_file" {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		h.serveResumeFile(w, ctx, request)
		return
	}
	if request.Operation == "resume.digest" {
		h.serveResumeDigest(w, r, request)
		return
	}
	if request.Operation == "resume.archive" || request.Operation == "resume.restore" || request.Operation == "resume.delete" {
		if r.ContentLength > 0 || len(r.TransferEncoding) != 0 {
			code := "RESUME_LIBRARY_ARCHIVE_INVALID"
			if request.Operation == "resume.restore" {
				code = "RESUME_LIBRARY_RESTORE_INVALID"
			} else if request.Operation == "resume.delete" {
				code = "RESUME_VERSION_DELETE_INVALID"
			}
			applicationFailure(w, &ApplicationOutcome{}, code)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if request.Operation == "resume.delete" && !resumeWriteDeadline(w) {
			return
		}
		h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
			return h.application.Jobs(ctx, request)
		})
		return
	}
	if request.Operation == "resume.list" {
		query, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil {
			applicationFailure(w, &ApplicationOutcome{}, "JOB_QUERY_INVALID")
			return
		}
		archived := strings.ToLower(query.Get("include_archived"))
		includeArchived := archived == "1" || archived == "true"
		if includeArchived {
			for key, values := range query {
				if (key != "search" && key != "include_archived") || len(values) != 1 {
					applicationFailure(w, &ApplicationOutcome{}, "RESUME_MANAGEMENT_INVALID", "input")
					return
				}
			}
		}
		request.Fields["search"], request.Fields["include_archived"] = query.Get("search"), includeArchived
	} else if request.Operation == "list" {
		query, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil {
			applicationFailure(w, &ApplicationOutcome{}, "JOB_QUERY_INVALID")
			return
		}
		limit, cursor := query.Get("limit"), query.Get("cursor")
		if limit == "" {
			limit = "20"
		}
		if cursor == "" {
			cursor = "offset:0"
		}
		request.Limit, e = strconv.Atoi(strings.TrimSpace(limit))
		if e != nil || request.Limit < 1 || request.Limit > 100 || !strings.HasPrefix(cursor, "offset:") {
			applicationFailure(w, &ApplicationOutcome{}, "JOB_QUERY_INVALID")
			return
		}
		request.Offset, e = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(cursor, "offset:")), 10, 64)
		if e != nil || request.Offset < 0 {
			applicationFailure(w, &ApplicationOutcome{}, "JOB_QUERY_INVALID")
			return
		}
	} else if r.Method != "GET" {
		// Enforce the small endpoint ceiling. The shared decoder rejects chunked
		// bodies and owns UTF-8/JSON checks before last-key-wins map decoding.
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
		fields, code, _ := materialJSONChecked(r, func(raw []byte) bool { return len(raw) <= 16384 && applicationScalarJSON(raw) })
		if code != "" {
			applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
			return
		}
		keys := map[string][]string{
			"decision": {"decision", "notes"}, "status": {"status", "occurred_at", "note"}, "start": {"occurred_at"},
			"assessment.resolve": {"finding_key", "resolution", "note", "expected_assessment_revision"},
			"resume.select":      {"resume_id"}, "resume.submitted": {"resume_id", "occurred_at", "note"},
			"session.create": {"contract_version"}, "session.confirm": {"contract_version", "expected_revision"}, "session.dismiss": {"contract_version", "expected_revision"}, "session.cancel": {"contract_version", "expected_revision"},
			"checklist.add_manual": {"contract_version", "expected_source_fingerprint", "category", "selected_text"},
			"checklist.set_state":  {"contract_version", "expected_source_fingerprint", "instruction_fingerprint", "state"},
		}[request.Operation]
		valid := materialKeys(fields, keys...)
		for key, raw := range fields {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			var value any
			if decoder.Decode(&value) != nil {
				valid = false
				break
			}
			if key == "contract_version" {
				valid = valid && value == json.Number("1")
				continue
			}
			if key == "expected_revision" {
				number, ok := value.(json.Number)
				valid = valid && ok && !strings.ContainsAny(string(number), ".eE")
				request.Fields[key] = value
				continue
			}
			_, isString := value.(string)
			if !isString && !(value == nil && (key == "occurred_at" || key == "notes" || key == "resume_id")) {
				valid = false
			}
			request.Fields[key] = value
		}
		if strings.HasPrefix(request.Operation, "session.") || strings.HasPrefix(request.Operation, "checklist.") {
			valid = valid && string(fields["contract_version"]) == "1"
		}
		if request.Operation == "decision" {
			value, ok := request.Fields["decision"].(string)
			valid = valid && ok && slices.Contains([]string{"apply", "consider", "skip"}, value)
		}
		if request.Operation == "status" {
			value, ok := request.Fields["status"].(string)
			valid = valid && ok && slices.Contains([]string{"captured", "preparing", "applied", "interview", "offer", "rejected", "withdrawn", "closed"}, value)
		}
		if request.Operation == "assessment.resolve" {
			value, ok := request.Fields["resolution"].(string)
			finding, findingOK := request.Fields["finding_key"].(string)
			_, revisionPresent := request.Fields["expected_assessment_revision"]
			valid = valid && len(fields) >= 3 && len(fields) <= len(keys) && ok && findingOK && revisionPresent
			valid = valid && strings.TrimSpace(finding) != "" && len(finding) <= 160
			valid = valid && slices.Contains([]string{"confirmed", "resolved", "not_applicable", "accepted_risk"}, value)
			valid = valid && validSHA256Revision(request.Fields["expected_assessment_revision"])
		}
		if strings.HasPrefix(request.Operation, "checklist.") || strings.HasPrefix(request.Operation, "session.") {
			valid = valid && len(fields) == len(keys)
		}
		if !valid {
			applicationFailure(w, &ApplicationOutcome{}, "JOB_REQUEST_INVALID")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status := http.StatusOK
	if request.Operation == "session.create" {
		status = http.StatusCreated
	}
	h.applicationCall(w, ctx, status, func() (*ApplicationOutcome, *ApplicationError) { return h.application.Jobs(ctx, request) })
}

func validSHA256Revision(value any) bool {
	text, ok := value.(string)
	if !ok || len(text) != 71 || !strings.HasPrefix(text, "sha256:") {
		return false
	}
	for _, c := range text[7:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
