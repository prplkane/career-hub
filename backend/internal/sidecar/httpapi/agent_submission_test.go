package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const submissionPath = "/api/jobs/job-one/application/record-submission"

func TestRecordSubmissionHTTPAdmissionAndFacts(t *testing.T) {
	app, token, _ := applicationFixture(t)
	h := app.(*handler)
	response(t, h, materialPost(submissionPath, token, `{"confirmed_submitted":true}`), 404)
	if _, found := response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)["record_submission"]; found {
		t.Fatal("unwired capability")
	}
	calls := 0
	h.application.RecordSubmission = func(_ context.Context, r RecordSubmissionRequest) (*ApplicationOutcome, *ApplicationError) {
		calls++
		if r.JobID != "job-one" || !r.ConfirmedSubmitted || r.Note != "fixture" || r.ResumeID == nil || *r.ResumeID != "resume_one" {
			t.Fatal("lost request")
		}
		return &ApplicationOutcome{Committed: true, Result: json.RawMessage(`{"result":"recorded","external_action_performed_by_tool":false}`)}, nil
	}
	if response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)["record_submission"] != float64(1) {
		t.Fatal("capability")
	}
	body := `{"confirmed_submitted":true,"resume_id":"resume_one","note":"fixture"}`
	got := response(t, h, materialPost(submissionPath, token, body), 200)
	if got["operation_state"].(map[string]any)["committed"] != true {
		t.Fatal("lost commit")
	}
	for _, bad := range []string{`{}`, `null`, `[]`, `{"confirmed_submitted":false}`, `{"confirmed_submitted":1}`, `{"confirmed_submitted":true,"note":null}`, `{"confirmed_submitted":true,"note":"\ud800"}`, `{"confirmed_submitted":true,"resume_id":2}`, `{"confirmed_submitted":true,"occurred_at":false}`, `{"confirmed_submitted":true,"force":true}`, `{"confirmed_submitted":true,"confirmed_submitted":false}`, `{"confirmed_submitted":true,"\u006eote":"a","note":"b"}`} {
		response(t, h, materialPost(submissionPath, token, bad), 400)
	}
	response(t, h, materialPost(submissionPath+"?force=1", token, body), 400)
	response(t, h, materialPost(submissionPath, token, strings.Repeat(" ", (1<<20)+1)), 413)
	response(t, h, materialPost(submissionPath, "", body), 403)
	response(t, h, request("GET", submissionPath, token), 404)
	for _, edit := range []func(*http.Request){func(r *http.Request) { r.Host = "evil.test" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }} {
		r := materialPost(submissionPath, token, body)
		edit(r)
		response(t, h, r, 403)
	}
	if e := h.configureExtensions([]string{pairedOrigin}, bytes.NewReader(append(bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)...))); e != nil {
		t.Fatal(e)
	}
	for _, credential := range []string{token, h.extensions.capture, h.extensions.helper} {
		r := materialPost(submissionPath, credential, body)
		r.Header.Set("Origin", pairedOrigin)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		response(t, h, r, 403)
	}
	if calls != 1 {
		t.Fatal("invalid or extension request reached writer")
	}
	for _, state := range []*ApplicationOutcome{{CommitUnknown: true}, {Committed: true, CleanupFailed: true}} {
		h.application.RecordSubmission = func(context.Context, RecordSubmissionRequest) (*ApplicationOutcome, *ApplicationError) {
			return state, &ApplicationError{Code: "AGENT_SUBMISSION_FAILED"}
		}
		got := response(t, h, materialPost(submissionPath, token, body), 500)["operation_state"].(map[string]any)
		if got["known"] == state.CommitUnknown || got["cleanup_failed"] != state.CleanupFailed {
			t.Fatal("failure certainty lost")
		}
		if state.Committed && got["committed"] != true {
			t.Fatal("committed cleanup failed erased")
		}
	}
}
