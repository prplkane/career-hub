package agentmaterials

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const submissionTestReceipt = `{"schema_version":1,"contract_version":4,"result":"recorded","job_id":"job-one","status":"applied","applied_resume":{"recorded":true,"resume":null,"history":[]},"external_action_performed_by_tool":false,"operation_state":{"known":true,"committed":true,"partial":false,"cleanup_failed":false}}`

func TestSubmissionClientAndReceipt(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/session" {
			fmt.Fprint(w, `{"client":"browser","capabilities":{"record_submission":1}}`)
			return
		}
		calls++
		var v SubmissionInput
		if r.Method != "POST" || r.URL.Path != "/api/jobs/job-one/application/record-submission" || json.NewDecoder(r.Body).Decode(&v) != nil || !v.ConfirmedSubmitted {
			t.Error("wrong submission request")
		}
		fmt.Fprint(w, submissionTestReceipt)
	}))
	defer s.Close()
	c, p := New(s.URL)
	if p != nil {
		t.Fatal(p)
	}
	c.token = syntheticToken
	r := Request{Command: "record-submission", JobID: "job-one", Submission: &SubmissionInput{ConfirmedSubmitted: true}}
	if _, p := c.Run(context.Background(), r); p != nil || calls != 1 {
		t.Fatal("client failed", p)
	}
	for _, change := range []func(*Request){func(r *Request) { r.Submission = nil }, func(r *Request) { r.Kind = "resume" }, func(r *Request) { r.Vocabulary = &VocabularyInput{} }, func(r *Request) { r.Command = "material-context" }} {
		bad := r
		change(&bad)
		if _, p := c.Run(context.Background(), bad); p == nil {
			t.Fatal("invalid accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid reached service")
	}
	for _, bad := range []string{
		strings.Replace(submissionTestReceipt, `"external_action_performed_by_tool":false`, `"external_action_performed_by_tool":true`, 1),
		strings.Replace(submissionTestReceipt, `"recorded":true`, `"recorded":null`, 1),
		strings.Replace(submissionTestReceipt, `"job-one"`, `"another"`, 1),
		strings.Replace(submissionTestReceipt, `"result":"recorded"`, `"result":"no_change_needed"`, 1),
		strings.Replace(submissionTestReceipt, `"known":true`, `"known":false`, 1),
		strings.Replace(submissionTestReceipt, `"cleanup_failed":false`, `"cleanup_failed":true`, 1),
	} {
		if _, p := checkedSubmissionReceipt([]byte(bad), "job-one"); p == nil {
			t.Fatal("invalid receipt accepted")
		}
	}
	private := strings.Replace(submissionTestReceipt, `"status":"applied"`, `"status":"applied","private":"SECRET"`, 1)
	if raw, p := checkedSubmissionReceipt([]byte(private), "job-one"); p != nil || strings.Contains(string(raw), "SECRET") {
		t.Fatal("unbounded receipt projection")
	}
	unknown := apiFailure([]byte(`{"error_code":"AGENT_SUBMISSION_FAILED","operation_state":{"known":false,"committed":null,"partial":false,"cleanup_failed":true,"recovery_required":true}}`))
	if unknown.State.Known || unknown.State.Committed != nil || unknown.State.CleanupFailed == nil || !*unknown.State.CleanupFailed || !unknown.State.RecoveryRequired {
		t.Fatal("unknown cleanup facts lost")
	}
}
