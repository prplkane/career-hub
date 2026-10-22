package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const configureBody = `{"contract_version":2,"account_address":"jobs@example.test","app_password":"abcdefghijklmnop","sender_addresses":["alerts@example.test"],"application_sender_addresses":["tracking@example.test"],"schedule_enabled":false,"interval_minutes":30,"expected_revision":0}`

func TestReconnectRequiresExplicitRouteAndFreshPassword(t *testing.T) {
	h, token, spy := applicationFixture(t)
	body := strings.Replace(configureBody, `"expected_revision":0`, `"expected_revision":1`, 1)
	post := func(path, token, body string) *http.Request {
		r := configurePut(path, token, body)
		r.Method = http.MethodPost
		return r
	}
	response(t, h, post("/api/email-connection/reconnect", token, body), 200)
	if spy.fields["reconnect"] != true || spy.fields["password_supplied"] != true || spy.fields["revision"] != int64(1) {
		t.Fatal("reconnect intent lost")
	}
	response(t, h, configurePut("/api/email-connection", token, body), 200)
	if spy.fields["reconnect"] != false {
		t.Fatal("ordinary save became reconnect")
	}
	before := len(spy.calls)
	for _, invalid := range []string{configureBody,
		strings.Replace(body, `"app_password":"abcdefghijklmnop"`, `"app_password":null`, 1),
		strings.Replace(body, `"app_password":"abcdefghijklmnop"`, `"app_password":" "`, 1),
		strings.Replace(body, `"contract_version":2`, `"contract_version":2,"reconnect":true`, 1),
	} {
		response(t, h, post("/api/email-connection/reconnect", token, invalid), 400)
	}
	response(t, h, post("/api/email-connection/reconnect", "wrong", body), 403)
	response(t, h, post("/api/email-connection/reconnect?extra=1", token, body), 404)
	response(t, h, post("/api/email-connection/%72econnect", token, body), 404)
	response(t, h, configurePut("/api/email-connection/reconnect", token, body), 404)
	response(t, h, request(http.MethodGet, "/api/email-connection/reconnect", token), 404)
	if len(spy.calls) != before {
		t.Fatal("invalid reconnect reached owner")
	}
	for _, code := range []string{"EMAIL_CREDENTIAL_RECONNECT_REQUIRED", "EMAIL_CREDENTIAL_RECONNECT_NOT_REQUIRED", "EMAIL_CREDENTIAL_INTENT_SCOPE_CHANGED"} {
		spy.err = &ApplicationError{Code: code, Category: "email_connection_mutation"}
		got := response(t, h, post("/api/email-connection/reconnect", token, body), 409)
		if got["error_code"] != code {
			t.Fatal("reconnect refusal was masked")
		}
	}
}

func configurePut(target, token, body string) *http.Request {
	r := request(http.MethodPut, target, token)
	r.Body = http.NoBody
	if body != "" {
		r.Body = io.NopCloser(strings.NewReader(body))
	}
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestEmailConfigureRouteUsesOnlyTypedSourceEnvelope(t *testing.T) {
	h, token, spy := applicationFixture(t)
	r := configurePut("/api/email-connection", token, configureBody)
	r.Header.Set("X-Career-Hub-Diagnostic-Run", "20260914T040000.123456Z_deadbeef")
	got := response(t, h, r, 200)
	if got["saved"] != true || spy.calls[len(spy.calls)-1] != "email.configure" ||
		spy.fields["account"] != "jobs@example.test" || spy.fields["password_supplied"] != true ||
		spy.fields["revision"] != int64(0) || spy.fields["run_id"] != "20260914T040000.123456Z_deadbeef" {
		t.Fatal("connection save was not typed and dispatched", spy.fields)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "abcdefghijklmnop") {
		t.Fatal("password leaked into response")
	}
	before := len(spy.calls)
	for _, body := range []string{
		strings.Replace(configureBody, `"contract_version":2`, `"contract_version":1`, 1),
		strings.Replace(configureBody, `"interval_minutes":30`, `"interval_minutes":14`, 1),
		strings.Replace(configureBody, `"interval_minutes":30`, `"interval_minutes":true`, 1),
		strings.Replace(configureBody, `"sender_addresses":["alerts@example.test"]`, `"sender_addresses":null`, 1),
		strings.Replace(configureBody, `"account_address":"jobs@example.test"`, `"account_address":null`, 1),
		strings.Replace(configureBody, `"expected_revision":0`, `"expected_revision":-1`, 1),
		strings.TrimSuffix(configureBody, "}"),
	} {
		response(t, h, configurePut("/api/email-connection", token, body), 400)
	}
	response(t, h, configurePut("/api/email-connection?extra=1", token, configureBody), 404)
	response(t, h, configurePut("/api/email-connection", "", configureBody), 403)
	if len(spy.calls) != before {
		t.Fatal("invalid or unauthorized save reached connection owner")
	}
}

func TestEmailConfigurePreservesUncertainWriteFacts(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Committed: false, CommitUnknown: true, RecoveryRequired: true, RunID: "20260914T040000.123456Z_deadbeef"}
	spy.err = &ApplicationError{Code: "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED", Category: "email_connection_mutation"}
	got := response(t, h, configurePut("/api/email-connection", token, configureBody), 503)
	state := got["operation_state"].(map[string]any)
	if got["error_code"] != "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED" || state["known"] != false ||
		state["recovery_required"] != true || got["run_id"] != "20260914T040000.123456Z_deadbeef" {
		t.Fatal("unconfirmed credential commit was described as rollback", got)
	}
	spy.err = &ApplicationError{Code: "EMAIL_CREDENTIAL_LEGACY_OWNERSHIP_REQUIRED", Category: "email_connection_mutation"}
	spy.out = &ApplicationOutcome{}
	got = response(t, h, configurePut("/api/email-connection", token, configureBody), 409)
	if got["error_code"] != "EMAIL_CREDENTIAL_LEGACY_OWNERSHIP_REQUIRED" {
		t.Fatal("shared v1 target refusal was lost", got)
	}
}
