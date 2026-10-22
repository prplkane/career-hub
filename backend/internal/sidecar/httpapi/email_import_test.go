package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"careerhub.local/backend/internal/emailimport"
)

const emailImportBody = `{"contract_version":1,"files":[{"file_name":"fixture.eml","content_base64":"YQ=="}]}`

func emailImportPost(target, token, body string) *http.Request {
	r := configurePut(target, token, body)
	r.Method = http.MethodPost
	return r
}

func TestEmailImportStrictRouteAndEnvelope(t *testing.T) {
	h, token, spy := applicationFixture(t)
	const target = "/api/discoveries/email-imports"
	response(t, h, emailImportPost(target, token, emailImportBody), http.StatusCreated)
	if len(spy.calls) != 1 || spy.calls[0] != "email.import" {
		t.Fatal("manual import was not dispatched")
	}
	before := len(spy.calls)
	for _, body := range []string{
		`null`, `{}`, `{"contract_version":1,"files":null}`,
		`{"contract_version":1,"files":[]}`,
		strings.Replace(emailImportBody, `"contract_version":1`, `"contract_version":2`, 1),
		strings.Replace(emailImportBody, `"contract_version":1`, `"contract_version":1,"extra":true`, 1),
		strings.Replace(emailImportBody, `"file_name":"fixture.eml"`, `"file_name":"fixture.eml","file_name":"other.eml"`, 1),
		strings.Replace(emailImportBody, `fixture.eml`, `\ud800.eml`, 1),
		emailImportBody + `{}`,
	} {
		response(t, h, emailImportPost(target, token, body), 400)
	}
	response(t, h, emailImportPost(target, "wrong", emailImportBody), 403)
	for _, path := range []string{target + "?extra=1", target + "?", "/api/discoveries/%65mail-imports"} {
		response(t, h, emailImportPost(path, token, emailImportBody), 404)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		r := emailImportPost(target, token, emailImportBody)
		r.Method = method
		response(t, h, r, 404)
	}
	if len(spy.calls) != before {
		t.Fatal("invalid import reached the native owner")
	}
}

func TestEmailImportTransportBoundsBeforeOwner(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
		func(r *http.Request) { r.Header["Content-Type"] = []string{"application/json", "application/json"} },
		func(r *http.Request) { r.Header["Content-Length"] = []string{"10", "10"} },
		func(r *http.Request) { r.TransferEncoding = []string{"chunked"} },
		func(r *http.Request) { r.ContentLength = -1 },
		func(r *http.Request) { r.ContentLength++ },
	} {
		r := emailImportPost("/api/discoveries/email-imports", token, emailImportBody)
		mutate(r)
		response(t, h, r, 400)
	}
	r := emailImportPost("/api/discoveries/email-imports", token, emailImportBody)
	r.ContentLength = emailimport.MaxBatchJSONBytes + 1
	response(t, h, r, 413)
	if len(spy.calls) != 0 {
		t.Fatal("invalid transport reached the native owner")
	}
}

func TestEmailImportPreservesPriorCommitAndUncertainty(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Committed: true, CommitUnknown: true, Partial: true, CleanupFailed: true, RecoveryRequired: true}
	spy.err = &ApplicationError{Code: "EMAIL_IMPORT_WRITE_UNVERIFIED", Category: "email_import"}
	got := response(t, h, emailImportPost("/api/discoveries/email-imports", token, emailImportBody), 503)
	state := got["operation_state"].(map[string]any)
	if got["error_code"] != spy.err.Code || state["known"] != false || state["recovery_required"] != true || state["committed"] != true {
		t.Fatal("partial/unknown import was described as rollback", got)
	}
}
