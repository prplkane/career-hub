package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestResumeManagementHTTPBoundary(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"resumes":[{"id":"fictional","digest_status":"digested","digest_supported":true,"digest_source_id":"run-a","digest_processed_at":"2023-11-14T22:13:20+00:00"}]}`)}
	if got := response(t, h, request("GET", "/api/resumes?include_archived=true&search=fiction", token), 200); got["resumes"] == nil {
		t.Fatal("missing management envelope")
	}
	if spy.calls[len(spy.calls)-1] != "jobs.resume.list:" {
		t.Fatal("wrong owner")
	}
	// The existing active picker still accepts its former query shape.
	response(t, h, request("GET", "/api/resumes?search=fiction&unknown=1", token), 200)
	if spy.calls[len(spy.calls)-1] != "jobs.resume.list:" {
		t.Fatal("active picker changed owner")
	}
	before := len(spy.calls)
	response(t, h, request("GET", "/api/resumes?include_archived=true", "wrong"), 403)
	cross := request("GET", "/api/resumes?include_archived=true", token)
	cross.Header.Set("Origin", "https://outside.example")
	response(t, h, cross, 403)
	response(t, h, request("HEAD", "/api/resumes?include_archived=true", token), 404)
	for _, query := range []string{"include_archived=true&include_archived=false", "include_archived=true&unknown=1", "include_archived=%ZZ"} {
		response(t, h, request("GET", "/api/resumes?"+query, token), 400)
	}
	if len(spy.calls) != before {
		t.Fatal("refused request reached owner")
	}
	for _, tc := range []struct {
		code, category string
		status         int
	}{
		{"RESUME_MANAGEMENT_INVALID", "input", 400}, {"RESUME_MANAGEMENT_LIMIT", "limit", 413},
		{"RESUME_MANAGEMENT_CANCELED", "canceled", 408}, {"RESUME_MANAGEMENT_CHANGED", "drift", 409},
		{"RESUME_MANAGEMENT_UNAVAILABLE", "storage", 503}, {"RESUME_MANAGEMENT_UNSAFE", "unsafe", 422},
		{"RESUME_MANAGEMENT_CLEANUP_FAILED", "cleanup", 500},
	} {
		spy.out = &ApplicationOutcome{}
		spy.err = &ApplicationError{Code: tc.code, Category: tc.category}
		got := response(t, h, request("GET", "/api/resumes?include_archived=true", token), tc.status)
		if got["error_code"] != tc.code || got["diagnostic_category"] != tc.category || got["resumes"] != nil {
			t.Fatal(tc, got)
		}
	}
	spy.err = nil
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"resumes":[{"label":"` + strings.Repeat("x", MaxResponseBytes) + `"}]}`)}
	if got := response(t, h, request("GET", "/api/resumes?include_archived=true", token), 500); got["error_code"] != "LOCAL_RESPONSE_INVALID" {
		t.Fatal(got)
	}
}

func TestResumeDigestHTTPBoundaryIsClosedAndOperationSpecific(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"resume_id":"resume-one","source_id":"src_resume_one_digest_a","technology_count":2,"bullet_count":3}`)}
	got := response(t, h, request("POST", "/api/resumes/resume-one/digest", token), 201)
	if got["source_id"] != "src_resume_one_digest_a" || spy.calls[len(spy.calls)-1] != "jobs.resume.digest:resume-one" {
		t.Fatal("digest did not use its exact owner route", got, spy.calls)
	}
	before := len(spy.calls)
	unknownBody := request("POST", "/api/resumes/resume-one/digest", token)
	unknownBody.Body = http.NoBody
	unknownBody.ContentLength = -1
	for _, invalid := range []*http.Request{
		request("POST", "/api/resumes/resume-one/digest?again=1", token),
		vaultRequest("POST", "/api/resumes/resume-one/digest", token, `{}`),
		unknownBody,
		request("GET", "/api/resumes/resume-one/digest", token),
		request("POST", "/api/resumes/%2e%2e/digest", token),
		request("POST", "/api/resumes/resume-one/digest", "wrong"),
	} {
		status := 404
		if invalid.URL.RawQuery != "" || invalid.ContentLength != 0 {
			status = 400
		}
		if invalid.Header.Get("X-Career-Hub-Token") == "wrong" {
			status = 403
		}
		response(t, h, invalid, status)
	}
	if len(spy.calls) != before {
		t.Fatal("invalid digest request reached owner")
	}
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"RESUME_DIGEST_REQUEST_INVALID", 400}, {"RESUME_DIGEST_NOT_FOUND", 404},
		{"RESUME_DIGEST_ARCHIVED", 409}, {"RESUME_DIGEST_SOURCE_INVALID", 409},
		{"RESUME_DIGEST_INPUT_CHANGED", 409}, {"RESUME_DIGEST_RECOVERY_REQUIRED", 409},
		{"RESUME_DIGEST_UNSUPPORTED", 422}, {"RESUME_DIGEST_UNSAFE", 422},
		{"RESUME_DIGEST_LIMIT", 413}, {"RESUME_DIGEST_CANCELED", 408},
		{"RESUME_DIGEST_UNAVAILABLE", 503}, {"RESUME_DIGEST_COMPLETED_UNCONFIRMED", 503},
		{"RESUME_DIGEST_CLEANUP_FAILED", 500}, {"RESUME_DIGEST_FAILED", 500},
	} {
		spy.out = &ApplicationOutcome{RecoveryRequired: tc.code == "RESUME_DIGEST_RECOVERY_REQUIRED"}
		spy.err = &ApplicationError{Code: tc.code, Category: "resume_digest"}
		failure := response(t, h, request("POST", "/api/resumes/resume-one/digest", token), tc.status)
		if failure["error_code"] != tc.code || failure["diagnostic_category"] != "resume_digest" || failure["retryable"] != false {
			t.Fatal(tc, failure)
		}
	}
}

func TestJobRoutesAreClosedAndUseExactFields(t *testing.T) {
	h, token, s := applicationFixture(t)
	for _, c := range []struct {
		method, path, body, operation string
		status                        int
	}{
		{"GET", "/api/jobs?limit=10&cursor=offset:20", "", "list:", 200},
		{"GET", "/api/jobs/job-one/application", "", "application:job-one", 200},
		{"GET", "/api/jobs/job-one/assessment", "", "assessment:job-one", 200},
		{"GET", "/api/jobs/job-one/source", "", "source:job-one", 200},
		{"GET", "/api/resumes?search=Primary", "", "resume.list:", 200},
		{"POST", "/api/jobs/job-one/application/resume-selection", `{"resume_id":null}`, "resume.select:job-one", 200},
		{"POST", "/api/jobs/job-one/application/submitted-resume", `{"resume_id":"resume-one","note":"verified"}`, "resume.submitted:job-one", 200},
		{"PUT", "/api/jobs/job-one/application/decision", `{"decision":"consider","notes":"review later"}`, "decision:job-one", 200},
		{"PUT", "/api/jobs/job-one/application/decision", `{"decision":"consider","notes":null,"expected_revision":3}`, "decision:job-one", 200},
		{"POST", "/api/jobs/job-one/application/status", `{"status":"applied","occurred_at":null,"note":"sent"}`, "status:job-one", 200},
		{"POST", "/api/jobs/job-one/application/status", `{"status":"applied","expected_revision":12}`, "status:job-one", 200},
		{"POST", "/api/jobs/job-one/application/start", `{}`, "start:job-one", 200},
		{"GET", "/api/jobs/job-one/application-session", "", "session.latest:job-one", 200},
		{"POST", "/api/jobs/job-one/application-session", `{"contract_version":1}`, "session.create:job-one", 201},
		{"GET", "/api/application-sessions/reviews", "", "session.reviews:", 200},
		{"POST", "/api/application-sessions/session-one/cancel", `{"contract_version":1,"expected_revision":1}`, "session.cancel:session-one", 200},
		{"GET", "/api/jobs/job-one/application-instructions", "", "checklist.load:job-one", 200},
	} {
		response(t, h, vaultRequest(c.method, c.path, token, c.body), c.status)
		if s.calls[len(s.calls)-1] != "jobs."+c.operation {
			t.Fatal("wrong owner", c, s.calls)
		}
	}
	// The optional revision guard reaches the owner as the exact integer and
	// the start route, which carries no guard, still refuses it.
	response(t, h, vaultRequest("POST", "/api/jobs/job-one/application/status", token, `{"status":"applied","expected_revision":12}`), 200)
	if s.fields["expected_revision"] != json.Number("12") || s.fields["status"] != "applied" {
		t.Fatal("expected_revision not forwarded", s.fields)
	}
	response(t, h, vaultRequest("PUT", "/api/jobs/job-one/application/decision", token, `{"decision":"consider"}`), 200)
	if _, present := s.fields["expected_revision"]; present {
		t.Fatal("absent expected_revision was invented", s.fields)
	}
	count := len(s.calls)
	for _, path := range []string{"/api/jobs/%2e%2e/source", "/api/jobs/a%2Fb/source", "/api/jobs/job-one/delete", "/api/application-sessions/one/claim", "/api/%6aobs/job-one/source"} {
		response(t, h, vaultRequest("POST", path, token, `{}`), 404)
	}
	for _, body := range []string{`{}`, `{"status":null}`, `{"status":true}`, `{"status":"invented"}`, `{"status":"applied","path":"private"}`, `{"status":"\ud800"}`, `{"status":"applied","note":null}`,
		`{"status":"applied","expected_revision":"3"}`, `{"status":"applied","expected_revision":1.5}`, `{"status":"applied","expected_revision":null}`, `{"status":"applied","expected_revision":true}`} {
		response(t, h, vaultRequest("POST", "/api/jobs/job-one/application/status", token, body), 400)
	}
	response(t, h, vaultRequest("PUT", "/api/jobs/job-one/application/decision", token, `{"decision":"apply","expected_revision":"2"}`), 400)
	response(t, h, vaultRequest("POST", "/api/jobs/job-one/application/start", token, `{"expected_revision":2}`), 400)
	for _, query := range []string{"limit=0", "limit=101", "limit=1.0", "cursor=offset:-1", "cursor=offset:9223372036854775808", "limit=%ZZ"} {
		response(t, h, request("GET", "/api/jobs?"+query, token), 400)
	}
	response(t, h, vaultRequest("POST", "/api/jobs/job-one/application/status", "wrong", `{"status":"applied"}`), 403)
	if len(s.calls) != count {
		t.Fatal("invalid request reached owner")
	}
}

func TestJobUnknownCommitIsVisible(t *testing.T) {
	h, token, s := applicationFixture(t)
	s.out = &ApplicationOutcome{Committed: true, CommitUnknown: true, RecoveryRequired: true, CleanupFailed: true}
	s.err = &ApplicationError{Code: "JOB_OPERATION_FAILED"}
	out := response(t, h, vaultRequest("POST", "/api/jobs/job-one/application/status", token, `{"status":"applied"}`), 500)
	state := out["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != true || state["recovery_required"] != true {
		t.Fatal("erased earlier commit", state)
	}
}
