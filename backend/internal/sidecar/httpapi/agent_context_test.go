package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAgentContextHTTPBoundary(t *testing.T) {
	app, token, _ := applicationFixture(t)
	h := app.(*handler)
	path := "/api/jobs/job-one/agent-context"
	response(t, h, materialPost(path, token, `{"purpose":"assessment"}`), 404)
	if _, exists := response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)["agent_context"]; exists {
		t.Fatal("unwired capability")
	}
	calls := 0
	h.application.AgentContext = func(_ context.Context, r AgentContextRequest) (*ApplicationOutcome, *ApplicationError) {
		calls++
		if r.JobID != "job-one" || r.Purpose != "application_text" || r.RegistryMode != "exact-auto" || r.Task == nil {
			t.Fatal("intent lost")
		}
		return &ApplicationOutcome{Result: json.RawMessage(`{"read":"complete"}`)}, nil
	}
	if response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)["agent_context"] != float64(1) {
		t.Fatal("capability missing")
	}
	body := `{"purpose":"application_text","registry_mode":"exact-auto","task_request":{"purpose":"application_text","kind":"short_answer","exact_prompt":"Why?","limits":{"words":12345678901234567890}}}`
	response(t, h, materialPost(path, token, body), 200)
	for _, bad := range []string{`{}`, `null`, `{"purpose":"assessment","registry_mode":null}`, `{"purpose":"assessment","force":true}`, `{"purpose":"assessment","profile":"PRIVATE"}`, `{"purpose":"assessment","task_request":null}`, `{"purpose":"assessment","purpose":"resume_delta"}`, `{"purpose":"application_text","task_request":{"purpose":"application_text","kind":"short_answer","exact_prompt":"\ud800"}}`} {
		response(t, h, materialPost(path, token, bad), 400)
	}
	response(t, h, materialPost(path+"?x=1", token, body), 400)
	response(t, h, materialPost(path, token, strings.Repeat("x", 66000)), 413)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		response(t, h, request(method, path, token), 404)
	}
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Del(CapabilityHeader) }, func(r *http.Request) { r.Host = "evil.test" }, func(r *http.Request) { r.Header.Set("Origin", "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") }, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }} {
		r := materialPost(path, token, body)
		spy := &materialBodySpy{}
		r.Body = spy
		change(r)
		response(t, h, r, 403)
		if spy.reads != 0 {
			t.Fatal("read body before auth")
		}
	}
	if calls != 1 {
		t.Fatal("invalid request reached owner")
	}
	h.application.AgentContext = func(context.Context, AgentContextRequest) (*ApplicationOutcome, *ApplicationError) {
		return &ApplicationOutcome{Result: json.RawMessage(`{"PRIVATE":"partial"}`)}, &ApplicationError{Code: "AGENT_CONTEXT_UNAVAILABLE"}
	}
	result := response(t, h, materialPost(path, token, body), 503)
	if _, ok := result["PRIVATE"]; ok {
		t.Fatal("partial payload escaped")
	}
	for code, status := range map[string]int{"AGENT_CONTEXT_SOURCE_CHANGED": 409, "AGENT_CONTEXT_CLEANUP_FAILED": 503, "AGENT_CONTEXT_SCHEMA_UNSUPPORTED": 409, "AGENT_CONTEXT_JOB_CARD_NOT_FOUND": 404, "AGENT_CONTEXT_APPLICATION_TEXT_STORE_IDENTITY_INVALID": 409} {
		h.application.AgentContext = func(context.Context, AgentContextRequest) (*ApplicationOutcome, *ApplicationError) {
			return &ApplicationOutcome{}, &ApplicationError{Code: code}
		}
		got := response(t, h, materialPost(path, token, body), status)
		raw, _ := json.Marshal(got)
		if !strings.Contains(string(raw), code) || strings.Contains(string(raw), "does not prove") {
			t.Fatalf("read refusal lost its category: %s", raw)
		}
	}
}
