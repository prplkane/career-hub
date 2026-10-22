package httpapi

import (
	"strings"
	"testing"
)

func TestEmailUpdateLinkHTTPBoundary(t *testing.T) {
	h, token, spy := applicationFixture(t)
	path := "/api/application-email-updates/link"
	valid := `{"contract_version":1,"outcome_id":"email_outcome_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","job_id":"synthetic-job","expected_revision":2}`
	response(t, h, materialPost(path, token, valid), 200)
	if spy.calls[len(spy.calls)-1] != "email.link" || spy.fields["job_id"] != "synthetic-job" ||
		spy.fields["revision"] != int64(2) {
		t.Fatal("valid email link did not reach exact callback")
	}
	before := len(spy.calls)
	for _, body := range []string{
		`{}`, `{"contract_version":2,"outcome_id":"event","job_id":"job","expected_revision":2}`,
		`{"contract_version":1,"outcome_id":null,"job_id":"job","expected_revision":2}`,
		`{"contract_version":1,"outcome_id":"event","job_id":"job","expected_revision":0}`,
		`{"contract_version":1,"outcome_id":"event","job_id":"job","expected_revision":true}`,
		`{"contract_version":1,"outcome_id":"event","job_id":"job","expected_revision":2,"extra":1}`,
	} {
		if got := response(t, h, materialPost(path, token, body), 400); got["error_code"] != "EMAIL_APPLICATION_UPDATE_INVALID" {
			t.Fatal("invalid email link envelope accepted", got)
		}
	}
	response(t, h, request("GET", path, token), 404)
	response(t, h, materialPost(path+"?x=1", token, valid), 404)
	response(t, h, materialPost(path+"?", token, valid), 404)
	response(t, h, materialPost(strings.Replace(path, "email-updates", "email%2Dupdates", 1), token, valid), 404)
	denied := materialPost(path, "", valid)
	body := &materialBodySpy{}
	denied.Body = body
	response(t, h, denied, 403)
	if body.reads != 0 {
		t.Fatal("unauthorized email link body read")
	}
	denied = materialPost(path, token, valid)
	denied.Header.Set("Origin", "https://example.invalid")
	response(t, h, denied, 403)
	oversized := materialPost(path, token, valid)
	oversized.ContentLength = 16385
	response(t, h, oversized, 413)
	if len(spy.calls) != before {
		t.Fatal("denied email link reached owner")
	}
	spy.out = &ApplicationOutcome{CommitUnknown: true}
	spy.err = &ApplicationError{Code: "EMAIL_APPLICATION_UPDATE_LINK_COMMIT_UNCONFIRMED", Category: "email_application_update_link"}
	unknown := response(t, h, materialPost(path, token, valid), 503)
	state := unknown["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != nil || unknown["diagnostic_category"] != "email_application_update_link" {
		t.Fatal("uncertain email link was misreported as rollback", unknown)
	}
}
