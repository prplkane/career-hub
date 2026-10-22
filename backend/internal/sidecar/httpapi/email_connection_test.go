package httpapi

import (
	"net/http"
	"runtime"
	"strings"
	"testing"
)

func TestEmailConnectionReadRouteClosedAndBounded(t *testing.T) {
	bare, bareToken := fixture(t, func() (map[string]any, error) { return map[string]any{}, nil })
	bareRead := response(t, bare, request(http.MethodGet, "/api/email-connection", bareToken), 404)
	if bareRead["error_code"] != "RESOURCE_NOT_FOUND" {
		t.Fatalf("read-only default sidecar exposed email route: %#v", bareRead)
	}
	h, token, spy := applicationFixture(t)
	session := response(t, h, request(http.MethodGet, "/api/session", token), 200)
	if session["capabilities"].(map[string]any)["email_connection"] != (runtime.GOOS == "windows") {
		t.Fatal("email capability does not match the qualified native platform")
	}
	got := response(t, h, request(http.MethodGet, "/api/email-connection", token), 200)
	if got["saved"] != true || len(spy.calls) != 1 || spy.calls[0] != "email.connection" {
		t.Fatalf("email route did not invoke only the owned callback: %#v %#v", got, spy.calls)
	}
	for _, target := range []string{
		"/api/email-connection?all=true", "/api/email-connection?", "/api/email-connection/", "/api/email-connection%2F",
		"/api//email-connection", "/api/email%2Dconnection", "/api/email-connection%5Cother",
	} {
		got = response(t, h, request(http.MethodGet, target, token), 404)
		if got["error_code"] != "RESOURCE_NOT_FOUND" {
			t.Fatalf("alternate target %q accepted: %#v", target, got)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodOptions} {
		status := 404
		if method == http.MethodOptions {
			status = 403
		}
		got = response(t, h, request(method, "/api/email-connection", token), status)
		if method == http.MethodOptions {
			// Browser preflight is rejected at the authorization boundary.
			if got["error_code"] != "LOCAL_API_ACCESS_DENIED" {
				t.Fatalf("preflight accepted: %#v", got)
			}
		} else if got["error_code"] != "RESOURCE_NOT_FOUND" {
			t.Fatalf("mutation method %s accepted: %#v", method, got)
		}
	}
	got = response(t, h, request(http.MethodGet, "/api/email-connection", "wrong"), 403)
	if got["error_code"] != "LOCAL_API_ACCESS_DENIED" {
		t.Fatalf("missing capability accepted: %#v", got)
	}
	wrongOrigin := request(http.MethodGet, "/api/email-connection", token)
	wrongOrigin.Header.Set("Origin", "https://other.example.test")
	got = response(t, h, wrongOrigin, 403)
	if got["error_code"] != "LOCAL_API_ACCESS_DENIED" || len(spy.calls) != 1 {
		t.Fatalf("cross-origin request reached callback: %#v %#v", got, spy.calls)
	}
	spy.out = &ApplicationOutcome{Result: []byte(`{"value":"` + strings.Repeat("x", MaxResponseBytes) + `"}`)}
	got = response(t, h, request(http.MethodGet, "/api/email-connection", token), 500)
	if got["error_code"] != "LOCAL_RESPONSE_INVALID" || strings.Contains(got["error"].(string), strings.Repeat("x", 100)) {
		t.Fatalf("oversized payload leaked as partial result: %#v", got)
	}
}

func TestEmailConnectionReadErrorsStayFixedAndNonPartial(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"EMAIL_CONNECTION_CANCELED", 408},
		{"EMAIL_CONNECTION_INVALID", 409},
		{"EMAIL_CONNECTION_SCHEMA_TOO_NEW", 409},
		{"EMAIL_CONNECTION_PATH_CHANGED", 409},
		{"DATA_HOME_CHANGED", 409},
		{"EMAIL_CONNECTION_DATABASE_UNAVAILABLE", 503},
		{"EMAIL_CONNECTION_SCHEMA_PREPARATION_REQUIRED", 503},
		{"DATA_HOME_OWNERSHIP_REQUIRED", 503},
		{"DATA_HOME_RECOVERY_REQUIRED", 503},
		{"EMAIL_SECRET_STORE_FAILED", 503},
		{"EMAIL_CONNECTION_CLEANUP_FAILED", 500},
	} {
		spy.err = &ApplicationError{Code: tc.code, Category: "email_connection_read"}
		spy.out = nil
		got := response(t, h, request(http.MethodGet, "/api/email-connection", token), tc.status)
		if got["error_code"] != tc.code || got["diagnostic_category"] != "email_connection_read" || got["operation_state"].(map[string]any)["known"] != false ||
			!strings.Contains(got["suggestion"].(string), "No email or settings change") {
			t.Fatalf("error %s was not one fixed failure: %#v", tc.code, got)
		}
	}
}
