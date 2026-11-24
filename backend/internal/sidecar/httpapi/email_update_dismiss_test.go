package httpapi

import "testing"

func TestEmailUpdateDismissHTTPBoundary(t *testing.T) {
	h, token, spy := applicationFixture(t)
	path := "/api/application-email-updates/dismiss"
	one := `{"contract_version":1,"outcome_id":"email_outcome_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_revision":3}`
	response(t, h, materialPost(path, token, one), 200)
	if spy.calls[len(spy.calls)-1] != "email.dismiss" || spy.fields["revision"] != int64(3) || spy.fields["all_unmatched"] != false {
		t.Fatal("single dismiss did not reach the exact callback", spy.fields)
	}
	response(t, h, materialPost(path, token, `{"contract_version":1,"all_unmatched":true}`), 200)
	if spy.fields["all_unmatched"] != true || spy.fields["outcome_id"] != "" {
		t.Fatal("clear-unmatched did not reach the exact callback", spy.fields)
	}
	before := len(spy.calls)
	for _, body := range []string{
		`{}`,
		`{"contract_version":1}`,
		`{"contract_version":1,"all_unmatched":false}`,
		`{"contract_version":1,"all_unmatched":"true"}`,
		`{"contract_version":2,"all_unmatched":true}`,
		`{"contract_version":1,"all_unmatched":true,"outcome_id":"event","expected_revision":1}`,
		`{"contract_version":1,"outcome_id":null,"expected_revision":1}`,
		`{"contract_version":1,"outcome_id":"event","expected_revision":0}`,
		`{"contract_version":1,"outcome_id":"event","expected_revision":true}`,
		`{"contract_version":1,"outcome_id":"event"}`,
	} {
		if got := response(t, h, materialPost(path, token, body), 400); got["error_code"] != "EMAIL_APPLICATION_UPDATE_INVALID" {
			t.Fatal("invalid dismiss envelope accepted", body, got)
		}
	}
	response(t, h, request("GET", path, token), 404)
	response(t, h, materialPost(path+"?x=1", token, one), 404)
	denied := materialPost(path, "", one)
	body := &materialBodySpy{}
	denied.Body = body
	response(t, h, denied, 403)
	if body.reads != 0 {
		t.Fatal("unauthorized dismiss body read")
	}
	denied = materialPost(path, token, one)
	denied.Header.Set("Origin", "https://example.invalid")
	response(t, h, denied, 403)
	oversized := materialPost(path, token, one)
	oversized.ContentLength = 16385
	response(t, h, oversized, 413)
	if len(spy.calls) != before {
		t.Fatal("refused dismiss reached the owner")
	}
	spy.out = &ApplicationOutcome{CommitUnknown: true}
	spy.err = &ApplicationError{Code: "EMAIL_APPLICATION_UPDATE_DISMISS_COMMIT_UNCONFIRMED", Category: "email_application_update_dismiss"}
	unknown := response(t, h, materialPost(path, token, one), 503)
	if state := unknown["operation_state"].(map[string]any); state["known"] != false || unknown["diagnostic_category"] != "email_application_update_dismiss" {
		t.Fatal("uncertain dismiss was misreported", unknown)
	}
}
