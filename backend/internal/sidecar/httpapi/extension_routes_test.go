package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func capturePayload() map[string]any {
	return map[string]any{"contract_version": 1, "request_id": "00000000-0000-4000-8000-000000000001", "capture_kind": "page", "destination": "feed", "observed_url": "https://www.linkedin.com/jobs/view/123", "page_title": " Fictional role ", "captured_at_client": "2026-09-21T00:00:00.123Z", "source_text": "\r\n Fictional Python role.\r\nApply online. \r\n", "structured": map[string]any{"title": "Engineer", "company": "Fictional Labs", "location": "Boston, MA", "posting_id": "ABC"}, "extractor": map[string]any{"version": 2, "strategy": "provider_container"}}
}

func extensionJSONRequest(path, token string, payload any) *http.Request {
	r := extensionRequest("POST", path, token)
	if payload != nil {
		raw, _ := json.Marshal(payload)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func TestExtensionBusinessRoutesAndEnvelope(t *testing.T) {
	h := extensionFixture(t)
	var got []ExtensionRequest
	h.attended = func(_ context.Context, r ExtensionRequest) (*ApplicationOutcome, *ApplicationError) {
		got = append(got, r)
		return &ApplicationOutcome{Result: json.RawMessage(`{"outcome":"created"}`)}, nil
	}
	for _, tc := range []struct {
		path, op string
		payload  any
		status   int
	}{
		{"/api/discoveries/captures", "capture", capturePayload(), 201},
		{"/api/application-sessions/claim", "claim", map[string]any{"contract_version": 1, "request_id": "req", "current_url": "https://example.test/jobs/1", "tab_id": 3}, 200},
		{"/api/application-sessions/submission-signal", "signal", map[string]any{"contract_version": 1, "session_id": "session", "session_token": "secret", "tab_id": 3, "page_origin": "https://example.test", "event_key": "event", "success_rule": "rule", "submit_observed_at": "t0", "success_observed_at": "t1"}, 200},
		{"/api/application-sessions/interrupt", "interrupt", map[string]any{"contract_version": 1, "session_id": "session", "session_token": "secret", "tab_id": 3, "page_origin": "https://example.test", "reason": "closed"}, 200},
		{"/api/application-helper/fields", "projection", nil, 200},
		{"/api/application-helper/answers/save", "answers.save", map[string]any{"contract_version": 2, "expected_revision": "sha256:r", "question": "Favorite stack?", "value_kind": "text", "value": "Go"}, 200},
		{"/api/application-helper/answers/delete", "answers.delete", map[string]any{"contract_version": 2, "expected_revision": "sha256:r", "answer_id": "answer"}, 200},
	} {
		token := h.extensions.capture
		if extensionPurpose(tc.path) == "helper" {
			token = h.extensions.helper
		}
		before := len(got)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, extensionJSONRequest(tc.path, token, tc.payload))
		if w.Code != tc.status || len(got) != before+1 || got[before].Operation != tc.op {
			t.Fatalf("closed route %s got %d", tc.path, w.Code)
		}
		if _, found := got[before].Fields["contract_version"]; found {
			t.Fatal("version leaked into domain fields")
		}
		if tc.payload == nil {
			continue
		}
		input := tc.payload.(map[string]any)
		for _, bad := range []map[string]any{func() map[string]any { v := clonePayload(input); v["extra"] = true; return v }(), func() map[string]any { v := clonePayload(input); delete(v, "contract_version"); return v }(), func() map[string]any { v := clonePayload(input); v["contract_version"] = true; return v }(), func() map[string]any { v := clonePayload(input); v["contract_version"] = 99; return v }()} {
			w = httptest.NewRecorder()
			h.ServeHTTP(w, extensionJSONRequest(tc.path, token, bad))
			if w.Code != 400 || len(got) != before+1 {
				t.Fatalf("invalid envelope reached %s", tc.path)
			}
		}
	}
	if got[0].Capture["title"] != "Engineer" || got[0].Capture["source_kind"] != "extension" || got[0].Scope != "page_visible" || got[0].Readiness != "needs_review" || got[0].ContentChars != 36 {
		t.Fatalf("capture projection changed: count %d", got[0].ContentChars)
	}
	if got[1].Fields["tab_id"] != json.Number("3") {
		t.Fatal("wire integer type changed")
	}
}

func clonePayload(p map[string]any) map[string]any {
	raw, _ := json.Marshal(p)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	return result
}

func TestExtensionBusinessRefusalBeforeCallbacks(t *testing.T) {
	h := extensionFixture(t)
	calls := 0
	h.attended = func(context.Context, ExtensionRequest) (*ApplicationOutcome, *ApplicationError) {
		calls++
		return &ApplicationOutcome{Result: json.RawMessage(`{}`)}, nil
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set(CapabilityHeader, h.extensions.helper) },
		func(r *http.Request) { r.Header.Set(CapabilityHeader, h.token) },
		func(r *http.Request) { r.ContentLength = 16385 },
		func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
		func(r *http.Request) { r.TransferEncoding = []string{"chunked"} },
		func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(`{"contract_version":1,"contract_version":1}`))
			r.ContentLength = 43
		},
		func(r *http.Request) {
			raw := `{"contract_version":1,"request_id":"\ud800","current_url":"x","tab_id":3}`
			r.Body = io.NopCloser(strings.NewReader(raw))
			r.ContentLength = int64(len(raw))
		},
	} {
		r := extensionJSONRequest("/api/application-sessions/claim", h.extensions.capture, map[string]any{"contract_version": 1, "request_id": "req", "current_url": "https://example.test", "tab_id": 3})
		mutate(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code < 400 || calls != 0 {
			t.Fatal("bad transport reached owner")
		}
	}
	r := extensionJSONRequest("/api/application-helper/fields", h.extensions.helper, map[string]any{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || calls != 0 {
		t.Fatal("projection accepted an input selector")
	}
	for _, tc := range []struct {
		outcome string
		status  int
	}{{"created", 201}, {"updated", 200}, {"duplicate", 200}, {"applied", 500}} {
		h.attended = func(context.Context, ExtensionRequest) (*ApplicationOutcome, *ApplicationError) {
			raw, _ := json.Marshal(map[string]any{"outcome": tc.outcome})
			return &ApplicationOutcome{Result: raw}, nil
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, extensionJSONRequest("/api/discoveries/captures", h.extensions.capture, capturePayload()))
		if w.Code != tc.status {
			t.Fatal("capture response status changed")
		}
	}
}
