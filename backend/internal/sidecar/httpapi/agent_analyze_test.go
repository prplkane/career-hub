package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAgentAnalyzeHTTPBoundary(t *testing.T) {
	app, token, _ := applicationFixture(t)
	h := app.(*handler)
	path := "/api/agent/analyze"
	response(t, h, materialPost(path, token, `{"source_text":"Python"}`), 404)
	if _, ok := response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)["agent_analyze"]; ok {
		t.Fatal("unwired capability")
	}
	calls := 0
	h.application.AgentAnalyze = func(_ context.Context, r AgentContextRequest) (*ApplicationOutcome, *ApplicationError) {
		calls++
		if r.Purpose != "assessment" || r.Task != nil || r.Source != nil && r.JobID != "" {
			t.Fatal("mixed intent")
		}
		return &ApplicationOutcome{Result: json.RawMessage(`{"read":true}`)}, nil
	}
	response(t, h, materialPost(path, token, `{"job_id":"synthetic"}`), 200)
	response(t, h, materialPost(path, token, `{"source_text":"Python\nSQL","company":"Synthetic","registry_mode":"exact-auto"}`), 200)
	for _, body := range []string{`{}`, `null`, `{"job_id":"../bad"}`, `{"job_id":"synthetic","source_text":"Python"}`, `{"job_id":"synthetic","company":""}`, `{"source_text":""}`, `{"source_text":null}`, `{"source_text":"Python","purpose":"application_text"}`, `{"source_text":"Python","source_text":"SQL"}`, `{"source_text":"Python","profile":"PRIVATE"}`, `{"source_text":"Python","registry_mode":"auto"}`, `{"source_text":"\ud800"}`, `{"source_text":"Python","company":null}`} {
		response(t, h, materialPost(path, token, body), 400)
	}
	response(t, h, materialPost(path+"?x=1", token, `{"source_text":"Python"}`), 400)
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Del(CapabilityHeader) }, func(r *http.Request) { r.Host = "evil.test" }, func(r *http.Request) { r.Header.Set("Origin", "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") }, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }} {
		r := materialPost(path, token, `{"source_text":"Python"}`)
		spy := &materialBodySpy{}
		r.Body = spy
		change(r)
		response(t, h, r, 403)
		if spy.reads != 0 {
			t.Fatal("body read before auth")
		}
	}
	if calls != 2 {
		t.Fatal("invalid input reached owner")
	}
	h.application.AgentAnalyze = func(context.Context, AgentContextRequest) (*ApplicationOutcome, *ApplicationError) {
		return &ApplicationOutcome{Result: json.RawMessage(`{"PRIVATE":"partial"}`)}, &ApplicationError{Code: "AGENT_CONTEXT_CLEANUP_FAILED"}
	}
	raw, _ := json.Marshal(response(t, h, materialPost(path, token, `{"source_text":"Python"}`), 503))
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("partial response escaped")
	}
}
