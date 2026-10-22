package httpapi

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

const retireBody = `{"contract_version":2,"expected_revision":3}`

func retirePost(target, token, body string) *http.Request {
	r := request(http.MethodPost, target, token)
	r.Body = io.NopCloser(strings.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestEmailRetireRoutesAreClosedAndTyped(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, operation := range []string{"disconnect", "forget"} {
		path := "/api/email-connection/" + operation
		got := response(t, h, retirePost(path, token, retireBody), 200)
		if got["saved"] != true || spy.calls[len(spy.calls)-1] != "email.retire" ||
			spy.fields["operation"] != operation || spy.fields["revision"] != int64(3) {
			t.Fatal("retirement did not use the exact typed owner action", operation, spy.fields)
		}
		before := len(spy.calls)
		for _, body := range []string{
			strings.Replace(retireBody, `"contract_version":2`, `"contract_version":1`, 1),
			strings.Replace(retireBody, `"expected_revision":3`, `"expected_revision":0`, 1),
			strings.Replace(retireBody, `"expected_revision":3`, `"expected_revision":true`, 1),
			strings.Replace(retireBody, `"expected_revision":3`, `"expected_revision":null`, 1),
			strings.Replace(retireBody, `"expected_revision":3`, `"expected_revision":-1`, 1),
			strings.Replace(retireBody, `"expected_revision":3`, `"expected_revision":3,"password":"private"`, 1),
		} {
			response(t, h, retirePost(path, token, body), 400)
		}
		response(t, h, retirePost(path+"?extra=1", token, retireBody), 404)
		response(t, h, retirePost(path, "", retireBody), 403)
		response(t, h, request(http.MethodGet, path, token), 404)
		response(t, h, retirePost(path+"/", token, retireBody), 404)
		oversized := retirePost(path, token, retireBody+strings.Repeat(" ", 65536))
		response(t, h, oversized, 413)
		if len(spy.calls) != before {
			t.Fatal("invalid or unauthorized retirement reached owner", operation)
		}
	}
}

func TestEmailRetirePreservesUncertainCommitAndLegacyRefusal(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{CommitUnknown: true, RecoveryRequired: true, RevisionRefreshRequired: true}
	spy.err = &ApplicationError{Code: "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED", Category: "email_connection_mutation"}
	got := response(t, h, retirePost("/api/email-connection/disconnect", token, retireBody), 503)
	state := got["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != nil || state["recovery_required"] != true ||
		state["revision_refresh_required"] != true {
		t.Fatal("uncertain retirement reported rollback", got)
	}
	spy.out = &ApplicationOutcome{}
	spy.err = &ApplicationError{Code: "EMAIL_CREDENTIAL_LEGACY_OWNERSHIP_REQUIRED", Category: "email_connection_mutation"}
	got = response(t, h, retirePost("/api/email-connection/forget", token, retireBody), 409)
	if got["error_code"] != "EMAIL_CREDENTIAL_LEGACY_OWNERSHIP_REQUIRED" {
		t.Fatal("shared v1 refusal was lost", got)
	}
}
