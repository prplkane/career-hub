package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplicationAssessmentResolutionRequiresExactRevision(t *testing.T) {
	h, token, spy := applicationFixture(t)
	capabilities := response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)
	if capabilities["assessment_resolution_revision"] != true {
		t.Fatal("revision-bound assessment action was not advertised")
	}
	path := "/api/jobs/job-one/assessment/resolution"
	revision := "sha256:" + strings.Repeat("a", 64)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"assessment":{"assessment_revision":"synthetic"}}`)}
	body := `{"finding_key":"recommendation","resolution":"accepted_risk","note":"Reviewed","expected_assessment_revision":"` + revision + `"}`
	got := response(t, h, materialPost(path, token, body), 200)
	if got["assessment"] == nil || spy.calls[len(spy.calls)-1] != "jobs.assessment.resolve:job-one" || spy.fields["expected_assessment_revision"] != revision {
		t.Fatalf("revision-bound request was not passed to the owner: %+v", got)
	}
	withoutNote := `{"finding_key":"recommendation","resolution":"resolved","expected_assessment_revision":"` + revision + `"}`
	response(t, h, materialPost(path, token, withoutNote), 200)
	if _, present := spy.fields["note"]; present {
		t.Fatal("optional note was invented by the HTTP adapter")
	}
	before := len(spy.calls)
	for _, invalid := range []string{
		`{"finding_key":"recommendation","resolution":"accepted_risk","note":"Reviewed"}`,
		`{"finding_key":"recommendation","resolution":"accepted_risk","note":"Reviewed","expected_assessment_revision":"old"}`,
		`{"finding_key":"recommendation","resolution":"accepted_risk","note":"Reviewed","expected_assessment_revision":"` + revision + `","extra":"x"}`,
		`{"finding_key":"recommendation","resolution":"invented","note":"Reviewed","expected_assessment_revision":"` + revision + `"}`,
		`{"finding_key":"","resolution":"accepted_risk","note":"Reviewed","expected_assessment_revision":"` + revision + `"}`,
	} {
		if failure := response(t, h, materialPost(path, token, invalid), 400); failure["error_code"] != "JOB_REQUEST_INVALID" {
			t.Fatalf("invalid assessment request was accepted: %+v", failure)
		}
	}
	response(t, h, materialPost(path, "", body), 403)
	response(t, h, request("GET", path, token), 404)
	response(t, h, materialPost("/api/jobs/job-one/assessment/other", token, body), 404)
	if len(spy.calls) != before {
		t.Fatal("a refused assessment request reached the owner")
	}
}
