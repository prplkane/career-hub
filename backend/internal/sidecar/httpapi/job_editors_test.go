package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/intake/resumeplan"
)

const testContact = "contact_0123456789abcdef0123456789abcdef"

func TestJobEditorRoutesAndSourceEnvelopeBounds(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, test := range []struct{ method, path, body, operation string }{
		{"GET", "/api/contacts", "", "contact.list:"},
		{"GET", "/api/contacts/" + testContact, "", "contact.get:" + testContact},
		{"PUT", "/api/contacts/" + testContact, `{"contract_version":1,"expected_revision":1,"updates":{"name":"Test"}}`, "contact.update:" + testContact},
		{"POST", "/api/contacts/" + testContact + "/archive", `{"contract_version":1,"expected_revision":1}`, "contact.archive:" + testContact},
		{"POST", "/api/contacts/" + testContact + "/restore", `{"contract_version":1,"expected_revision":2}`, "contact.restore:" + testContact},
		{"POST", "/api/contacts/" + testContact + "/unlink", `{"contract_version":1,"expected_revision":3,"job_ids":["job-one"],"discovery_ids":[]}`, "contact.unlink:" + testContact},
		{"GET", "/api/jobs/job-one/resume-plan", "", "plan.load:job-one"},
		{"POST", "/api/jobs/job-one/resume-plan", `{"action":"save_draft","expected_revision":0,"plan":{}}`, "plan.save:job-one"},
	} {
		response(t, h, vaultRequest(test.method, test.path, token, test.body), 200)
		if spy.calls[len(spy.calls)-1] != "jobs."+test.operation {
			t.Fatal("wrong owner", test.operation)
		}
	}
	response(t, h, request("GET", "/api/contacts?include_archived=TRUE", token), 200)
	if spy.fields["include_archived"] != true {
		t.Fatal("archive query lost")
	}
	body := `{"action":"approve","expected_revision":0,"plan":{}}`
	response(t, h, vaultRequest("POST", "/api/jobs/job-one/resume-plan", token, body+strings.Repeat(" ", MaxMaterialRequestBytes-len(body))), 200)
	response(t, h, vaultRequest("POST", "/api/jobs/job-one/resume-plan", token, body+strings.Repeat(" ", MaxMaterialRequestBytes)), 413)
	count := len(spy.calls)
	for _, path := range []string{"/api/contacts/create", "/api/contacts/confirm-candidate/", "/api/contacts/candidate-preview/", "/api/contacts/" + testContact + "/link", "/api/contacts/" + testContact + "/delete", "/api/contacts/a%2Fb", "/api/contacts/%2e%2e"} {
		response(t, h, vaultRequest("POST", path, token, "{}"), 404)
	}
	for _, body := range []string{
		`{}`, `{"action":"approve","expected_revision":true,"plan":{}}`,
		`{"action":"approve","expected_revision":1e0,"plan":{}}`,
		`{"action":"approve","expected_revision":-1,"plan":{}}`,
		`{"action":"approve","expected_revision":9223372036854775808,"plan":{}}`,
		`{"action":"approve","expected_revision":0,"plan":null}`,
		`{"action":"approve","expected_revision":0,"plan":{},"job_id":"other"}`,
	} {
		response(t, h, vaultRequest("POST", "/api/jobs/job-one/resume-plan", token, body), 409)
	}
	for _, body := range []string{
		`{"contract_version":1,"expected_revision":0,"updates":{"name":"Test"}}`,
		`{"contract_version":1,"expected_revision":1,"updates":{}}`,
		`{"contract_version":1,"expected_revision":1,"updates":{"channels":[]}}`,
		`{"contract_version":1,"expected_revision":1,"updates":{"name":null}}`,
		`{"contract_version":1,"expected_revision":1,"updates":{"name":"Test"},"provenance":{}}`,
	} {
		response(t, h, vaultRequest("PUT", "/api/contacts/"+testContact, token, body), 400)
	}
	response(t, h, vaultRequest("PUT", "/api/contacts/"+testContact, token, `{"contract_version":1,"expected_revision":1,"updates":{"name":"\ud800","name":"Test"}}`), 400)
	response(t, h, vaultRequest("POST", "/api/contacts/"+testContact+"/unlink", token, `{"contract_version":1,"expected_revision":1,"job_ids":null,"discovery_ids":[]}`), 400)
	response(t, h, vaultRequest("POST", "/api/jobs/job-one/resume-plan", "wrong", "{}"), 403)
	response(t, h, vaultRequest("PUT", "/api/contacts/"+testContact, "wrong", "{}"), 403)
	if count != len(spy.calls) {
		t.Fatal("refused editor request reached owner")
	}
}

func TestJobEditorRequestRetainsRevisionAndOrderedLastValues(t *testing.T) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(`{"action":"approve","expected_revision":9223372036854775807,"plan":{"job_id":"job-one"}}`), &fields) != nil {
		t.Fatal("fixture")
	}
	r := resumeplan.Request{Operation: "save", JobID: "job-one"}
	if !planEditorRequest(&r, fields) || r.ExpectedRevision != 9223372036854775807 || !r.Approve || string(r.Payload) != `{"job_id":"job-one"}` {
		t.Fatal("plan contract lost")
	}
	changes, ok := contactFactChanges([]byte(`{"context":"first","name":true,"context":"last","name":"Final"}`))
	if !ok || len(changes) != 2 || changes[0].Field != "context" || changes[0].Value != "last" || changes[1].Field != "name" || changes[1].Value != "Final" {
		t.Fatal("Python duplicate/insertion semantics lost")
	}
}

func TestJobEditorConflictPreservesWriteFacts(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Committed: true, CommitUnknown: true, CleanupFailed: true, RecoveryRequired: true}
	for code, status := range map[string]int{"CONTACT_CONFLICT": 409, "CONTACT_NOT_FOUND": 404, "CONTACT_OPERATION_FAILED": 500, "RESUME_PLAN_INVALID": 409, "RESUME_PLAN_SAVE_FAILED": 500, "APPLICATION_JOB_NOT_FOUND": 404} {
		spy.err = &ApplicationError{Code: code}
		result := response(t, h, request("GET", "/api/jobs/job-one/resume-plan", token), status)
		state := result["operation_state"].(map[string]any)
		if result["error_code"] != code || state["known"] != false || state["committed"] != true || state["cleanup_failed"] != true || state["recovery_required"] != true {
			t.Fatal("error/publication facts lost", code)
		}
	}
}
