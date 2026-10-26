package agentmaterials

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"careerhub.local/backend/internal/agent/agentcontext"
)

func TestPrepareClientIntentAndFailureFacts(t *testing.T) {
	for _, mode := range []string{"saved", "source", "unconfirmed", "bad-response", "transport-failure"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			c, _ := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/session" {
					fmt.Fprint(w, strings.Replace(sessionBody(), "material_integrity", "agent_prepare", 1))
					return
				}
				posts++
				if r.Method != "POST" || r.URL.Path != "/api/agent/prepare" || r.Header.Get("X-Career-Hub-Token") != syntheticToken {
					t.Fatal("wrong owner or missing authorization")
				}
				raw, _ := io.ReadAll(r.Body)
				var body map[string]json.RawMessage
				if json.Unmarshal(raw, &body) != nil || body["write_receipt"] != nil || body["initial_status"] != nil || body["task_request"] == nil {
					t.Fatal("invalid intent wire")
				}
				if mode == "source" {
					if body["job_id"] != nil || string(body["source_name"]) != `"role.txt"` || string(body["posting_id"]) != `"posting-1"` {
						t.Fatal("lost source metadata")
					}
				} else if string(body["job_id"]) != `"synthetic"` || body["source_text"] != nil {
					t.Fatal("lost saved identity")
				}
				if mode == "transport-failure" {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Fatal(err)
					}
					_ = connection.Close()
					return
				}
				if mode == "bad-response" {
					fmt.Fprint(w, `{"PRIVATE":"not a verified packet"}`)
					return
				}
				w.WriteHeader(503)
				known, committed := "true", "true"
				if mode == "unconfirmed" {
					known, committed = "false", "null"
				}
				fmt.Fprintf(w, `{"error_code":"JOB_INTAKE_RECOVERY_REQUIRED","error":"PRIVATE","run_id":"20260927T010203.123456Z_0123abcd","operation_state":{"known":%s,"committed":%s,"partial":true,"cleanup_failed":false,"recovery_required":true,"write":{"intent_recorded":true,"applied_files":2,"files_complete":true,"journal_removed":false,"completed":false}},"preparation_stages":{"intake_phase":"files","intake_complete":false,"preparation_complete":false,"context_complete":false}}`, known, committed)
			})
			request := Request{Command: "prepare", JobID: "synthetic", Preparation: &PrepareInput{ContextInput: ContextInput{Purpose: "application_text", RegistryMode: "off", TaskRequest: json.RawMessage(`{"purpose":"application_text","kind":"short_answer","exact_prompt":"Why <this>?"}`)}}}
			if mode == "source" {
				request.JobID = ""
				request.Preparation.Source = &agentcontext.SourceRequest{Text: "Python services"}
				request.Preparation.SourceName, request.Preparation.PostingID = "role.txt", "posting-1"
			}
			raw, e := c.Run(context.Background(), request)
			if e == nil || raw != nil || posts != 1 {
				t.Fatal("failure returned success or replayed the write")
			}
			if mode == "bad-response" {
				if e.Code != "AGENT_PREPARE_RESPONSE_INVALID" || e.State.Known || e.State.Committed != nil {
					t.Fatal("invalid response claimed rollback")
				}
				return
			}
			if mode == "transport-failure" {
				if e.Code != "AGENT_MATERIAL_TRANSPORT_FAILED" || e.State.Known || e.State.Committed != nil {
					t.Fatal("lost response inferred rollback")
				}
				return
			}
			encoded, _ := json.Marshal(e)
			if e.Code != "JOB_INTAKE_RECOVERY_REQUIRED" || e.PreparationStages == nil || e.State.Write == nil || e.State.Write.AppliedFiles != 2 || e.RunID == "" || strings.Contains(string(encoded), "PRIVATE") {
				t.Fatal("stage facts lost or private error copied")
			}
			if mode == "unconfirmed" && (e.State.Known || e.State.Committed != nil) {
				t.Fatal("unknown commit became rollback")
			}
		})
	}
}

func TestPrepareRejectsMixedIntentBeforeNetwork(t *testing.T) {
	c, _ := clientAt(t, func(http.ResponseWriter, *http.Request) { t.Fatal("invalid intent reached network") })
	for _, change := range []string{"analysis", "context", "submission", "vocabulary", "kind", "source-and-job", "source-name", "task", "purpose", "registry", "other-command"} {
		r := Request{Command: "prepare", JobID: "synthetic", Preparation: &PrepareInput{ContextInput: ContextInput{Purpose: "assessment", RegistryMode: "off"}}}
		switch change {
		case "analysis":
			r.Analysis = &AnalyzeInput{}
		case "context":
			r.Context = &ContextInput{}
		case "submission":
			r.Submission = &SubmissionInput{}
		case "vocabulary":
			r.Vocabulary = &VocabularyInput{}
		case "kind":
			r.Kind = "resume"
		case "source-and-job":
			r.Preparation.Source = &agentcontext.SourceRequest{Text: "Python"}
		case "source-name":
			r.Preparation.SourceName = "PRIVATE"
		case "task":
			r.Preparation.TaskRequest = []byte(`null`)
		case "purpose":
			r.Preparation.Purpose = "submit"
		case "registry":
			r.Preparation.RegistryMode = "auto"
		case "other-command":
			r.Command = "analyze"
		}
		if _, e := c.Run(context.Background(), r); e == nil {
			t.Fatal("accepted", change)
		}
	}
}

func TestPreparationFailureProjectionRejectsMalformedFacts(t *testing.T) {
	for _, stages := range []string{`{}`, `{"intake_phase":"PRIVATE","intake_complete":true,"preparation_complete":true,"context_complete":true}`, `{"intake_phase":"files","intake_complete":false,"preparation_complete":true,"context_complete":false}`, `{"intake_phase":"files","intake_complete":true,"preparation_complete":false,"context_complete":true}`} {
		e := apiFailure([]byte(`{"error_code":"AGENT_PREPARE_CONTEXT_UNAVAILABLE","run_id":"PRIVATE","preparation_stages":` + stages + `}`))
		if e.PreparationStages != nil || e.RunID != "" {
			t.Fatal("malformed facts exposed")
		}
	}
}
