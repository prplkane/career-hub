package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAgentPrepareHTTPBoundary(t *testing.T) {
	app, token, _ := applicationFixture(t)
	h := app.(*handler)
	path := "/api/agent/prepare"
	response(t, h, materialPost(path, token, `{"job_id":"synthetic"}`), 404)
	if _, ok := response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)["agent_prepare"]; ok {
		t.Fatal("unwired capability")
	}
	var captured []AgentPrepareRequest
	h.application.AgentPrepare = func(ctx context.Context, r AgentPrepareRequest) (*ApplicationOutcome, *ApplicationError) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Second || r.Source != nil && r.JobID != "" {
			t.Fatal("unbounded or mixed intent")
		}
		captured = append(captured, r)
		return &ApplicationOutcome{Result: json.RawMessage(`{"prepared":true}`)}, nil
	}
	if response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)["agent_prepare"] != float64(1) {
		t.Fatal("capability missing")
	}
	response(t, h, materialPost(path, token, `{"job_id":"synthetic"}`), 200)
	response(t, h, materialPost(path, token, `{"source_text":"Python\nSQL","company":"Synthetic","title":"Engineer","source_url":"https://example.test/jobs/1","source_name":"synthetic.txt","posting_id":"posting-1","purpose":"resume_delta","registry_mode":"exact-auto"}`), 200)
	response(t, h, materialPost(path, token, `{"job_id":"synthetic","purpose":"application_text","task_request":{"purpose":"application_text","kind":"short_answer","exact_prompt":"Why?","limits":{"words":12345678901234567890}}}`), 200)
	if captured[0].Purpose != "assessment" || captured[0].RegistryMode != "off" || captured[0].Task != nil || captured[1].SourceName != "synthetic.txt" || captured[1].PostingID != "posting-1" || captured[1].Purpose != "resume_delta" || captured[1].RegistryMode != "exact-auto" || captured[1].Source.Company != "Synthetic" || captured[2].Task == nil {
		t.Fatal("preparation intent lost")
	}
	for _, body := range []string{
		`{}`, `null`, `{"job_id":"../bad"}`, `{"job_id":"synthetic","source_text":"Python"}`,
		`{"job_id":"synthetic","source_name":""}`, `{"job_id":"synthetic","posting_id":""}`,
		`{"job_id":"synthetic","company":""}`, `{"source_text":""}`, `{"source_text":null}`,
		`{"source_text":"Python","purpose":null}`, `{"job_id":"synthetic","task_request":null}`,
		`{"source_text":"Python","source_name":"../private"}`, `{"source_text":"Python","registry_mode":"auto"}`,
		`{"source_text":"Python","source_text":"SQL"}`, `{"source_text":"\ud800"}`,
		`{"source_text":"Python","write_receipt":{}}`, `{"source_text":"Python","initial_status":"applied"}`,
		`{"source_text":"Python","data_root":"PRIVATE"}`, `{"source_text":"Python","recovery":true}`,
		`{"job_id":"synthetic","purpose":"application_text","task_request":{"purpose":"resume_delta"}}`,
	} {
		response(t, h, materialPost(path, token, body), 400)
	}
	response(t, h, materialPost(path+"?x=1", token, `{"job_id":"synthetic"}`), 400)
	response(t, h, materialPost(path+"?", token, `{"job_id":"synthetic"}`), 400)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		response(t, h, request(method, path, token), 404)
	}
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
	if len(captured) != 3 {
		t.Fatal("invalid input reached writer")
	}
	for code, status := range map[string]int{"AGENT_PREPARE_SOURCE_CHANGED": 409, "AGENT_PREPARE_CONTEXT_CHANGED": 409, "AGENT_PREPARE_CONTEXT_UNAVAILABLE": 503, "AGENT_PREPARE_CONTEXT_CLEANUP_FAILED": 503, "AGENT_PREPARE_INTAKE_UNCONFIRMED": 409, "JOB_INTAKE_REQUEST_INVALID": 400, "AGENT_CONTEXT_UNAVAILABLE": 503} {
		h.application.AgentPrepare = func(context.Context, AgentPrepareRequest) (*ApplicationOutcome, *ApplicationError) {
			return &ApplicationOutcome{Result: json.RawMessage(`{"PRIVATE":"partial"}`), Committed: true, Partial: true, PreparationStages: &PreparationStages{IntakeComplete: true, PreparationComplete: true}}, &ApplicationError{Code: code}
		}
		got := response(t, h, materialPost(path, token, `{"job_id":"synthetic"}`), status)
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "does not update applications") || strings.Contains(string(raw), "retry key") || !strings.Contains(string(raw), code) || got["diagnostic_category"] != "agent_prepare" {
			t.Fatal("unsafe preparation failure projection")
		}
		if got["preparation_stages"].(map[string]any)["preparation_complete"] != true || got["operation_state"].(map[string]any)["committed"] != true {
			t.Fatal("completed writes concealed")
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", AgentPrepareRequest{SourceName: "PRIVATE", PostingID: "PRIVATE"}), "PRIVATE") {
		t.Fatal("request formatting leaked private metadata")
	}
}
