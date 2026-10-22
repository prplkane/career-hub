package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmailSyncClosedHTTPBoundary(t *testing.T) {
	h, token, spy := applicationFixture(t)
	path := "/api/email-connection/sync"
	valid := `{"contract_version":2,"trigger":"manual"}`
	if result := response(t, h, materialPost(path, token, valid), 200); result["saved"] != true || spy.calls[len(spy.calls)-1] != "email.sync" || spy.fields["trigger"] != "manual" {
		t.Fatal("sync did not reach its fixed callback", result)
	}
	correlated := materialPost(path, token, valid)
	correlated.Header.Set("X-Career-Hub-Diagnostic-Run", "20260914T023000.123456Z_deadbeef")
	response(t, h, correlated, 200)
	if spy.fields["run_id"] != "20260914T023000.123456Z_deadbeef" {
		t.Fatal("source correlation header was not passed to the owned sync adapter")
	}
	response(t, h, materialPost(path, token, `{"contract_version":2,"trigger":"scheduled"}`), 200)
	if spy.fields["trigger"] != "scheduled" {
		t.Fatal("scheduled trigger lost")
	}
	before := len(spy.calls)
	for _, body := range []string{
		`{}`, `{"contract_version":true,"trigger":"manual"}`,
		`{"contract_version":2.0,"trigger":"manual"}`,
		`{"contract_version":2,"trigger":"unknown"}`,
		`{"contract_version":2,"trigger":null}`,
		`{"contract_version":2,"trigger":"manual","extra":1}`,
	} {
		if got := response(t, h, materialPost(path, token, body), 400); got["error_code"] != "EMAIL_CONNECTION_INVALID" {
			t.Fatal("bad sync envelope was not refused", got)
		}
	}
	response(t, h, request("GET", path, token), 404)
	response(t, h, materialPost(path+"?x=1", token, valid), 404)
	response(t, h, materialPost(path+"?", token, valid), 404)
	response(t, h, materialPost(strings.Replace(path, "email-connection", "email%2Dconnection", 1), token, valid), 404)
	unauthorized := materialPost(path, "", valid)
	bodySpy := &materialBodySpy{}
	unauthorized.Body = bodySpy
	response(t, h, unauthorized, 403)
	if bodySpy.reads != 0 {
		t.Fatal("unauthorized sync body was read")
	}
	crossSite := materialPost(path, token, valid)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	response(t, h, crossSite, 403)
	tooLarge := materialPost(path, token, valid)
	tooLarge.ContentLength = 65537
	response(t, h, tooLarge, 413)
	wrongType := materialPost(path, token, valid)
	wrongType.Header.Set("Content-Type", "text/plain")
	response(t, h, wrongType, 415)
	chunked := materialPost(path, token, valid)
	chunked.TransferEncoding = []string{"chunked"}
	response(t, h, chunked, 400)
	trailing := materialPost(path, token, valid)
	trailing.Body = io.NopCloser(strings.NewReader(valid + " {}"))
	trailing.ContentLength = int64(len(valid) + 3)
	response(t, h, trailing, 400)
	if len(spy.calls) != before {
		t.Fatal("refused sync request reached callback")
	}
	spy.err = &ApplicationError{Code: "EMAIL_SYNC_ALREADY_RUNNING", Category: "email_sync"}
	if got := response(t, h, materialPost(path, token, valid), 409); got["error_code"] != "EMAIL_SYNC_ALREADY_RUNNING" {
		t.Fatal("busy sync was not distinguished", got)
	}
	spy.out = &ApplicationOutcome{CommitUnknown: true, RevisionRefreshRequired: true,
		RunID: "20260914T023000.123456Z_deadbeef"}
	spy.err = &ApplicationError{Code: "EMAIL_SYNC_COMMIT_UNKNOWN", Category: "email_sync"}
	failed := response(t, h, materialPost(path, token, valid), 503)
	state := failed["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != nil ||
		failed["run_id"] != "20260914T023000.123456Z_deadbeef" {
		t.Fatal("uncertain sync was reported as rolled back", failed)
	}
	spy.out.RunID = "private@example.test"
	failed = response(t, h, materialPost(path, token, valid), 503)
	if _, exposed := failed["run_id"]; exposed {
		t.Fatal("invalid run ID was exposed in a failure envelope", failed)
	}
}

func TestEmailSyncDoesNotHoldUIReadLock(t *testing.T) {
	h, token, _ := applicationFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	app := h.(*handler).application
	app.EmailSync = func(context.Context, string, string) (*ApplicationOutcome, *ApplicationError) {
		close(started)
		<-release
		return &ApplicationOutcome{Result: json.RawMessage(`{"status":"succeeded"}`)}, nil
	}
	app.Runtime = func(context.Context) (json.RawMessage, error) {
		return json.RawMessage(`{"ready":true}`), nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), materialPost("/api/email-connection/sync", token, `{"contract_version":2,"trigger":"manual"}`))
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("sync callback did not start")
	}
	readDone := make(chan int, 1)
	go func() {
		writer := httptest.NewRecorder()
		h.ServeHTTP(writer, request(http.MethodGet, "/api/runtime", token))
		readDone <- writer.Code
	}()
	select {
	case status := <-readDone:
		if status != 200 {
			t.Fatal("unrelated UI read failed during sync", status)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("sync held the UI read lock")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sync callback did not exit")
	}
}
