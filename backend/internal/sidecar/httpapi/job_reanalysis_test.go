package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJobReanalysisRevisionedRouteAndClosedRequest(t *testing.T) {
	h, token, spy := applicationFixture(t)
	capabilities := response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)
	if capabilities["job_reanalysis_revision"] != true {
		t.Fatal("Go-only reanalysis revision capability missing")
	}
	revision := "sha256:" + strings.Repeat("a", 64)
	path := "/api/jobs/job-one/reanalyze"
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"job":{"id":"job-one"},"run_id":"20260914T090000.000000Z_1234abcd"}`)}
	got := response(t, h, materialPost(path, token, `{"expected_card_revision":"`+revision+`"}`), 200)
	if got["job"] == nil || spy.calls[len(spy.calls)-1] != "jobs.reanalyze:job-one" || spy.revision != revision || len(spy.fields) != 0 {
		t.Fatalf("revisioned rescan not routed to one job: %+v %+v", got, spy)
	}
	before := len(spy.calls)
	for _, body := range []string{
		`{}`, `{"expected_card_revision":null}`, `{"expected_card_revision":true}`,
		`{"expected_card_revision":"bad"}`, `{"expected_card_revision":"SHA256:` + strings.Repeat("a", 64) + `"}`,
		`{"expected_card_revision":"` + revision + `","extra":1}`,
		`{"expected_card_revision":"` + revision + `","expected_card_revision":"` + revision + `"}`,
		`{"expected_card_revision":"` + revision + `","\u0065xpected_card_revision":"` + revision + `"}`,
	} {
		if invalid := response(t, h, materialPost(path, token, body), 400); invalid["error_code"] != "JOB_REANALYSIS_REQUEST_INVALID" && invalid["error_code"] != "LOCAL_REQUEST_INVALID" {
			t.Fatal("invalid reanalysis body accepted", invalid)
		}
	}
	response(t, h, materialPost(path+"?force=1", token, `{"expected_card_revision":"`+revision+`"}`), 400)
	response(t, h, materialPost(path, token, strings.Repeat(" ", 4097)), 413)
	response(t, h, materialPost(path, "", `{"expected_card_revision":"`+revision+`"}`), 403)
	response(t, h, request("GET", path, token), 404)
	response(t, h, materialPost("/api/jobs/%2e%2e/reanalyze", token, `{"expected_card_revision":"`+revision+`"}`), 404)
	response(t, h, materialPost("/api/jobs/a%2Fb/reanalyze", token, `{"expected_card_revision":"`+revision+`"}`), 404)
	if len(spy.calls) != before {
		t.Fatal("refused rescan reached owner", spy.calls)
	}
}

func TestJobReanalysisErrorsKeepPublicationFacts(t *testing.T) {
	h, token, spy := applicationFixture(t)
	path := "/api/jobs/job-one/reanalyze"
	body := `{"expected_card_revision":"sha256:` + strings.Repeat("b", 64) + `"}`
	spy.out = &ApplicationOutcome{RunID: "20260914T090000.000000Z_1234abcd", RevisionRefreshRequired: true}
	spy.err = &ApplicationError{Code: "JOB_REANALYSIS_REVISION_CONFLICT"}
	stale := response(t, h, materialPost(path, token, body), 409)
	if stale["error_code"] != "JOB_REANALYSIS_REVISION_CONFLICT" || stale["operation_state"].(map[string]any)["revision_refresh_required"] != true {
		t.Fatal("stale reanalysis revision did not ask for review", stale)
	}
	spy.out = &ApplicationOutcome{RunID: "20260914T090000.000000Z_1234abcd", CommitUnknown: true, RecoveryRequired: true}
	spy.err = &ApplicationError{Code: "JOB_REANALYSIS_WRITE_UNCERTAIN"}
	unknown := response(t, h, materialPost(path, token, body), 503)
	state := unknown["operation_state"].(map[string]any)
	if unknown["error_code"] != "JOB_REANALYSIS_WRITE_UNCERTAIN" || state["known"] != false || state["committed"] != nil || state["recovery_required"] != true {
		t.Fatal("unknown write looked rolled back", unknown)
	}
	spy.out = &ApplicationOutcome{RunID: "20260914T090000.000000Z_1234abcd", Committed: true, Partial: true}
	spy.err = &ApplicationError{Code: "JOB_REANALYSIS_DIAGNOSTIC_FAILED"}
	postcommit := response(t, h, materialPost(path, token, body), 503)
	if postcommit["operation_state"].(map[string]any)["committed"] != true || !strings.Contains(strings.ToLower(postcommit["suggestion"].(string)), "do not rescan blindly") {
		t.Fatal("committed diagnostic failure implied a safe retry", postcommit)
	}
}
