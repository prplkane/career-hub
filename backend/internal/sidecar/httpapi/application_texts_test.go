package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications/applicationtexts"
	"careerhub.local/backend/internal/materials/materialwrite"
)

const textDraftBody = `{"id":"answer","kind":"short_answer","source":"Employer form","exact_prompt":"Why this role?","response":"Supported answer."}`

func textHTTPFixture(t *testing.T) (http.Handler, string, *materialSpy, *applicationtexts.Draft) {
	t.Helper()
	s := &materialSpy{out: &materialwrite.Outcome{Result: json.RawMessage(`{"schema_version":1,"result":"created"}`), Committed: true}}
	var got applicationtexts.Draft
	actions := s.actions()
	actions.ApplicationText = func(_ context.Context, job string, draft applicationtexts.Draft) (*materialwrite.Outcome, *materialwrite.Error) {
		if job != "Fréd Job" {
			t.Error("wrong job selection")
		}
		got = draft
		return s.finish("application-text")
	}
	h, e := NewWithMaterials(port, "0.1.7-dev", func() (map[string]any, error) { return map[string]any{}, nil }, emptyProfile, emptyDocuments, actions)
	if e != nil {
		t.Fatal(e)
	}
	token := sessionTokenOf(h)
	return h, token, s, &got
}

const textRoute = "/api/jobs/Fr%C3%A9d%20Job/application-texts"

func TestApplicationTextHTTPAdmissionAndNormalization(t *testing.T) {
	h, token, s, got := textHTTPFixture(t)
	caps := response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)
	if caps["application_texts"] != float64(1) {
		t.Fatal("missing capability")
	}
	body := `{"id":"answer","kind":"short_answer","source":"Employer form","exact_prompt":"Why this role?","response":"  Supported answer.  ","limits":{"characters":123456789012345678901234567890},"evidence_refs":["one","one"]}`
	response(t, h, materialPost(textRoute, token, body), 200)
	if got.State != "draft" || got.Response != "Supported answer." || len(got.EvidenceRefs) != 1 || string(*got.Limits.Characters) != "123456789012345678901234567890" {
		t.Fatal("normalization or number loss")
	}
	for _, body := range []string{
		`{"id":"answer","kind":"short_answer","response":"\ud800","response":"valid"}`,
		`{"id":"answer","kind":"short_answer","response":"valid","limits":{"words":1.0}}`,
		`{"id":"answer","kind":"short_answer","response":"valid","state":"submitted"}`,
		`{"id":"answer","kind":"short_answer","response":"valid","path":"private-marker"}`,
		`null`,
	} {
		if strings.HasPrefix(body, "{") {
			body = `{"source":"Employer form","exact_prompt":"Why this role?",` + body[1:]
		}
		before := len(s.calls)
		response(t, h, materialPost(textRoute, token, body), 400)
		if len(s.calls) != before {
			t.Fatal("invalid request reached owner")
		}
	}
	response(t, h, materialPost(textRoute, token, strings.Repeat("x", MaxMaterialRequestBytes+1)), 413)
	for _, method := range []string{"GET", "DELETE", "PATCH"} {
		response(t, h, request(method, textRoute, token), 404)
	}
	for _, path := range []string{"/api/jobs/..%2Fprivate/application-texts", textRoute + "/", textRoute + "/approve"} {
		response(t, h, materialPost(path, token, textDraftBody), 404)
	}
	legacy, oldToken, _ := materialFixture(t)
	oldCaps := response(t, legacy, request("GET", "/api/session", sessionTokenOf(legacy)), 200)["capabilities"].(map[string]any)
	if _, exists := oldCaps["application_texts"]; exists {
		t.Fatal("unwired capability advertised")
	}
	response(t, legacy, materialPost(textRoute, oldToken, textDraftBody), 404)
}

func TestApplicationTextHTTPAuthorizationAndFailureFacts(t *testing.T) {
	h, token, s, _ := textHTTPFixture(t)
	for _, mode := range []string{"token", "origin", "fetch", "extension"} {
		r := materialPost(textRoute, token, textDraftBody)
		body := &materialBodySpy{}
		r.Body = body
		switch mode {
		case "token":
			r.Header.Del(CapabilityHeader)
		case "origin":
			r.Header.Set("Origin", "https://example.invalid")
		case "fetch":
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		case "extension":
			r.Header.Set("Origin", "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		}
		response(t, h, r, 403)
		if body.reads != 0 || len(s.calls) != 0 {
			t.Fatal("authorization happened after input or storage")
		}
	}
	s.out = &materialwrite.Outcome{Committed: true, CleanupFailed: true}
	s.problem = &materialwrite.Error{Code: "APPLICATION_TEXT_STORE_UNAVAILABLE", Stage: "application_text"}
	body := response(t, h, materialPost(textRoute, token, textDraftBody), 503)
	state := body["operation_state"].(map[string]any)
	if body["error_code"] != "APPLICATION_TEXT_STORE_UNAVAILABLE" || state["known"] != true || state["committed"] != true || state["cleanup_failed"] != true {
		t.Fatal("lost late-publication facts")
	}
	s.panicCall = true
	body = response(t, h, materialPost(textRoute, token, textDraftBody), 500)
	state = body["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != nil {
		t.Fatal("panic fabricated no-write certainty")
	}
}
