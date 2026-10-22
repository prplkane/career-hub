package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestEmailUpdatesHTTPBoundary(t *testing.T) {
	h, token, spy := applicationFixture(t)
	path := "/api/application-email-updates"
	response(t, h, request(http.MethodGet, path, token), 200)
	if spy.fields["offset"] != int64(0) || spy.fields["limit"] != 20 || spy.calls[len(spy.calls)-1] != "email.updates" {
		t.Fatal("default email update page not forwarded")
	}
	response(t, h, request(http.MethodGet, path+"?limit=5&cursor=offset%3A10", token), 200)
	if spy.fields["offset"] != int64(10) || spy.fields["limit"] != 5 {
		t.Fatal("email update cursor not forwarded")
	}
	before := len(spy.calls)
	for _, suffix := range []string{
		"?limit=0", "?limit=101", "?limit=true", "?limit=2.5", "?cursor=abc",
		"?cursor=offset%3A-1", "?cursor=offset%3A99999999999999999999",
		"?limit=1&limit=2", "?private=1", "?cursor=%ZZ",
	} {
		got := response(t, h, request(http.MethodGet, path+suffix, token), 400)
		if got["error_code"] != "EMAIL_APPLICATION_UPDATE_INVALID" || got["items"] != nil {
			t.Fatal("invalid email page returned data", suffix, got)
		}
	}
	response(t, h, request(http.MethodPost, path, token), 404)
	response(t, h, request(http.MethodGet, strings.Replace(path, "email-updates", "email%2Dupdates", 1), token), 404)
	response(t, h, request(http.MethodGet, path, ""), 403)
	denied := request(http.MethodGet, path, token)
	denied.Header.Set("Origin", "https://example.invalid")
	response(t, h, denied, 403)
	if len(spy.calls) != before {
		t.Fatal("denied email page reached reader")
	}
	spy.err = &ApplicationError{Code: "EMAIL_APPLICATION_UPDATE_READ_UNAVAILABLE", Category: "email_application_updates_read"}
	spy.out = &ApplicationOutcome{}
	failed := response(t, h, request(http.MethodGet, path, token), 503)
	if failed["error_code"] != "EMAIL_APPLICATION_UPDATE_READ_UNAVAILABLE" || failed["items"] != nil || failed["diagnostic_category"] != "email_application_updates_read" {
		t.Fatal("failed read returned partial/unsafe page", failed)
	}
}
