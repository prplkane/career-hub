package httpapi

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentRouteScopeMatchesTheAgentClient(t *testing.T) {
	allowed := []struct{ method, path string }{
		{"GET", "/api/session"},
		{"GET", "/api/registry"},
		{"POST", "/api/agent/analyze"},
		{"POST", "/api/agent/prepare"},
		{"POST", "/api/jobs/job_1/agent-context"},
		{"POST", "/api/jobs/job_1/application-texts"},
		{"POST", "/api/jobs/job_1/application/record-submission"},
		{"POST", "/api/jobs/job_1/vocabulary/exact"},
		{"GET", "/api/jobs/job_1/materials/resume"},
		{"POST", "/api/jobs/job_1/materials/cover-letter"},
		{"POST", "/api/jobs/job_1/materials/resume/exports"},
	}
	for _, c := range allowed {
		if !agentRoute(c.method, c.path) {
			t.Errorf("%s %s should be inside the agent scope", c.method, c.path)
		}
	}
	refused := []struct{ method, path string }{
		{"POST", "/api/session"},
		{"PUT", "/api/registry"},
		{"GET", "/api/agent/analyze"},
		{"GET", "/api/vault/profile"},
		{"PUT", "/api/vault/profile"},
		{"GET", "/api/demand-gaps"},
		{"POST", "/api/email/sync"},
		{"GET", "/api/jobs/job_1/application-texts"},
		{"DELETE", "/api/jobs/job_1/materials/resume"},
		{"GET", "/api/jobs/job_1/materials/resume/exports"},
		{"POST", "/api/jobs//agent-context"},
		{"POST", "/api/jobs/job_1/agent-context/extra"},
		{"POST", "/api/jobs/job_1"},
		{"GET", "/"},
	}
	for _, c := range refused {
		if agentRoute(c.method, c.path) {
			t.Errorf("%s %s must be outside the agent scope", c.method, c.path)
		}
	}
}

func TestAgentTokenIsSeparateScopedAndNeverDisclosed(t *testing.T) {
	h, uiToken, spy := applicationFixture(t)
	agent, ok := AgentToken(h)
	if !ok || agent == uiToken {
		t.Fatal("application handler must carry a distinct agent token")
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(agent); err != nil || len(decoded) != 32 {
		t.Fatal("agent token must encode 32 random bytes")
	}

	// In scope: admitted (the session route answers; the action may still
	// validate its own body, but admission must not refuse it).
	response(t, h, request("GET", "/api/session", agent), 200)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("POST", "/api/agent/analyze", agent))
	if w.Code == http.StatusForbidden {
		t.Fatal("agent token was refused on an agent route")
	}

	// Out of scope: refused before any owner runs.
	before := len(spy.calls)
	for _, target := range []string{"/api/demand-gaps", "/api/vault/profile"} {
		result := response(t, h, request("GET", target, agent), 403)
		if result["error_code"] != "LOCAL_API_ACCESS_DENIED" {
			t.Fatal(result)
		}
	}
	if len(spy.calls) != before {
		t.Fatal("an out-of-scope agent request reached an owner")
	}

	// Replayed from a browser: refused even on an agent route.
	withOrigin := request("POST", "/api/agent/analyze", agent)
	withOrigin.Header.Set("Origin", "http://127.0.0.1:8766")
	response(t, h, withOrigin, 403)
	withFetch := request("POST", "/api/agent/analyze", agent)
	withFetch.Header.Set("Sec-Fetch-Site", "same-origin")
	response(t, h, withFetch, 403)

	// No route ever returns the agent token.
	for _, token := range []string{"", uiToken, agent} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("GET", "/api/session", token))
		if strings.Contains(w.Body.String(), agent) {
			t.Fatal("the session route disclosed the agent token")
		}
	}
}

func TestReadOnlyHandlerHasNoAgentToken(t *testing.T) {
	h, err := New(8766, "0.1.7-dev", func() (map[string]any, error) { return map[string]any{}, nil }, emptyProfile, emptyDocuments)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := AgentToken(h); ok {
		t.Fatal("the read-only diagnostics handler must not admit the agent client")
	}
}
