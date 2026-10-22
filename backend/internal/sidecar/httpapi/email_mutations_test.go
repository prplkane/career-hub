package httpapi

import (
	"io"
	"strings"
	"testing"
)

func TestEmailMutationHTTPBoundary(t *testing.T) {
	h, token, spy := applicationFixture(t)
	reset := "/api/email-connection/reset-cursor"
	sender := "/api/email-connection/senders"
	if result := response(t, h, materialPost(reset, token, `{"contract_version":2,"expected_revision":4}`), 200); result["saved"] != true || spy.revision != "4" || spy.calls[len(spy.calls)-1] != "email.reset" {
		t.Fatal("cursor reset did not reach exact callback")
	}
	if result := response(t, h, materialPost(sender, token, `{"contract_version":2,"sender_address":"jobs@example.test","disposition":"allowed","purpose":"job_alerts","expected_revision":5}`), 200); result["saved"] != true || spy.revision != "5" || spy.fields["sender"] != "jobs@example.test" || spy.fields["purpose"] != "job_alerts" || spy.calls[len(spy.calls)-1] != "email.sender" {
		t.Fatal("sender rule did not reach exact callback")
	}
	before := len(spy.calls)
	for _, tc := range []struct{ path, body string }{
		{reset, `{}`},
		{reset, `{"contract_version":true,"expected_revision":4}`},
		{reset, `{"contract_version":2,"expected_revision":0}`},
		{reset, `{"contract_version":2,"expected_revision":4.0}`},
		{reset, `{"contract_version":2,"expected_revision":4,"extra":1}`},
		{sender, `{"contract_version":2,"sender_address":"jobs@example.test","disposition":"allowed","expected_revision":4}`},
		{sender, `{"contract_version":2,"sender_address":null,"disposition":"allowed","purpose":"job_alerts","expected_revision":4}`},
		{sender, `{"contract_version":2,"sender_address":"jobs@example.test","disposition":"allowed","purpose":"job_alerts","expected_revision":4,"subject_policy":{}}`},
	} {
		if got := response(t, h, materialPost(tc.path, token, tc.body), 400); got["error_code"] != "EMAIL_CONNECTION_INVALID" {
			t.Fatal("bad email envelope was not rejected", got)
		}
	}
	for _, path := range []string{reset, sender} {
		response(t, h, request("GET", path, token), 404)
		response(t, h, materialPost(path+"?x=1", token, `{}`), 404)
		response(t, h, materialPost(path+"?", token, `{}`), 404)
		response(t, h, materialPost(strings.Replace(path, "email-connection", "email%2Dconnection", 1), token, `{}`), 404)
		request := materialPost(path, "", `{}`)
		body := &materialBodySpy{}
		request.Body = body
		response(t, h, request, 403)
		if body.reads != 0 {
			t.Fatal("unauthorized request body read")
		}
		request = materialPost(path, token, `{}`)
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		response(t, h, request, 403)
		request = materialPost(path, token, `{}`)
		request.ContentLength = 65537
		response(t, h, request, 413)
		request = materialPost(path, token, `{}`)
		request.Header.Set("Content-Type", "text/plain")
		response(t, h, request, 415)
		request = materialPost(path, token, `{}`)
		request.TransferEncoding = []string{"chunked"}
		response(t, h, request, 400)
		request = materialPost(path, token, `{}`)
		request.Body = io.NopCloser(strings.NewReader(`{} {}`))
		request.ContentLength = 5
		response(t, h, request, 400)
	}
	if len(spy.calls) != before {
		t.Fatal("rejected request reached email owner")
	}
	spy.out = &ApplicationOutcome{Committed: true, RevisionRefreshRequired: true}
	spy.err = &ApplicationError{Code: "EMAIL_SECRET_STORE_FAILED", Category: "email_connection_mutation"}
	failed := response(t, h, materialPost(reset, token, `{"contract_version":2,"expected_revision":6}`), 503)
	state := failed["operation_state"].(map[string]any)
	if state["committed"] != true || state["revision_refresh_required"] != true || failed["diagnostic_category"] != "email_connection_mutation" || strings.Contains(failed["suggestion"].(string), "No email or settings change") {
		t.Fatal("committed edit was misreported as rolled back", failed)
	}
	spy.out = &ApplicationOutcome{CommitUnknown: true, RevisionRefreshRequired: true}
	spy.err = &ApplicationError{Code: "EMAIL_CONNECTION_COMMIT_UNCONFIRMED", Category: "email_connection_mutation"}
	unknown := response(t, h, materialPost(reset, token, `{"contract_version":2,"expected_revision":6}`), 503)
	state = unknown["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != nil {
		t.Fatal("unknown commit reported a rollback", unknown)
	}
}
