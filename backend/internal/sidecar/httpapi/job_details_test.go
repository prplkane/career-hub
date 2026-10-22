package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJobDetailsRevisionedRouteAndClosedEnvelope(t *testing.T) {
	h, token, spy := applicationFixture(t)
	capabilities := response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)
	if capabilities["job_details_revision"] != true {
		t.Fatal("Go-only revision capability was not advertised")
	}
	revision := "sha256:" + strings.Repeat("a", 64)
	path := "/api/jobs/job-one/details"
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"job":{"id":"job-one","card_revision":"` + revision + `"}}`)}
	got := response(t, h, appPut(path, token, `{"fields":{"title":"Synthetic Engineer","experience_requirements":[{"minimum_years":3,"area":"Go"}]},"expected_card_revision":"`+revision+`"}`), 200)
	if got["job"] == nil || spy.calls[len(spy.calls)-1] != "jobs.details.edit:job-one" || spy.revision != revision {
		t.Fatalf("revisioned edit was not routed: %+v %+v", got, spy)
	}
	if spy.fields["title"] != "Synthetic Engineer" || spy.fields["experience_requirements"].([]any)[0].(map[string]any)["minimum_years"] != json.Number("3") {
		t.Fatal("detail values were not passed without numeric coercion")
	}
	response(t, h, request("GET", "/api/jobs/job-one", token), 200)
	if spy.calls[len(spy.calls)-1] != "jobs.detail:job-one" {
		t.Fatal("exact-card refresh route was not connected")
	}
	before := len(spy.calls)
	for _, body := range []string{
		`{"fields":{"title":"X"}}`,
		`{"fields":null,"expected_card_revision":"` + revision + `"}`,
		`{"fields":[],"expected_card_revision":"` + revision + `"}`,
		`{"fields":{},"expected_card_revision":"bad"}`,
		`{"fields":{},"expected_card_revision":"` + revision + `","extra":1}`,
		`{"fields":{"title":"first","title":"second"},"expected_card_revision":"` + revision + `"}`,
		`{"fields":{"visa_signals":{"clearance_required":true,"clearance_required":false}},"expected_card_revision":"` + revision + `"}`,
		`{"fields":{},"fields":{"title":"hidden"},"expected_card_revision":"` + revision + `"}`,
		`{"fields":{},"\u0066ields":{"title":"hidden"},"expected_card_revision":"` + revision + `"}`,
	} {
		if failure := response(t, h, appPut(path, token, body), 400); failure["error_code"] != "JOB_DETAILS_REQUEST_INVALID" && failure["error_code"] != "LOCAL_REQUEST_INVALID" {
			t.Fatalf("invalid body accepted or misclassified: %+v", failure)
		}
	}
	response(t, h, appPut(path+"?force=1", token, `{"fields":{},"expected_card_revision":"`+revision+`"}`), 400)
	if failure := response(t, h, appPut(path, token, strings.Repeat(" ", 16385)), 413); failure["error_code"] != "LOCAL_REQUEST_TOO_LARGE" {
		t.Fatal("oversized detail request was not rejected before decoding")
	}
	response(t, h, request("GET", "/api/jobs/job-one?force=1", token), 400)
	response(t, h, appPut(path, "", `{"fields":{},"expected_card_revision":"`+revision+`"}`), 403)
	response(t, h, request("GET", "/api/jobs/job-one", ""), 403)
	response(t, h, request("POST", path, token), 404)
	response(t, h, request("GET", "/api/jobs/job-one/details", token), 404)
	if len(spy.calls) != before {
		t.Fatal("rejected detail request reached the owner")
	}
}

func TestJobDetailsConflictAndUnknownCommitStayExplicit(t *testing.T) {
	h, token, spy := applicationFixture(t)
	path := "/api/jobs/job-one/details"
	revision := "sha256:" + strings.Repeat("b", 64)
	body := `{"fields":{"title":"New"},"expected_card_revision":"` + revision + `"}`
	spy.out = &ApplicationOutcome{Committed: false}
	spy.err = &ApplicationError{Code: "JOB_DETAILS_REVISION_CONFLICT"}
	conflict := response(t, h, appPut(path, token, body), 409)
	if conflict["error_code"] != "JOB_DETAILS_REVISION_CONFLICT" || conflict["operation_state"].(map[string]any)["committed"] != false {
		t.Fatal("stale edit did not produce an explicit uncommitted conflict")
	}
	spy.out = &ApplicationOutcome{Committed: true}
	spy.err = &ApplicationError{Code: "JOB_DETAILS_SAVE_UNVERIFIED"}
	unverified := response(t, h, appPut(path, token, body), 503)
	if unverified["operation_state"].(map[string]any)["committed"] != true || !strings.Contains(unverified["suggestion"].(string), "do not retry") {
		t.Fatal("post-commit failure implied rollback or blind retry")
	}
}
