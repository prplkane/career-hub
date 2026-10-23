package agentmaterials

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const textPayload = `{"id":" answer ","kind":" short_answer ","source":"Employer form","exact_prompt":"Why?","response":"Supported answer."}`
const textReceipt = `{"schema_version":1,"result":"created","job_id":"plan_job","item_id":"answer","kind":"short_answer","state":"draft","artifact_path":"outputs/application_texts/plan_job/application_texts.yaml"}`

func TestApplicationTextClientTransportAndReceipt(t *testing.T) {
	for _, mode := range []string{"created", "updated", "no_change_needed", "unavailable", "mismatch", "late-failure", "redirect", "raw-unicode"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			payload := textPayload
			if mode == "raw-unicode" {
				payload = strings.Replace(payload, `"response":"Supported answer."`, `"response":"\ud800","response":"valid"`, 1)
			}
			c, _ := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/session" {
					body := sessionBody()
					if mode != "unavailable" {
						body = strings.Replace(body, `"material_integrity":1`, `"application_texts":1`, 1)
					}
					io.WriteString(w, body)
					return
				}
				calls++
				raw, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.Path != "/api/jobs/plan_job/application-texts" || string(raw) != payload || r.Header.Get("X-Career-Hub-Token") != syntheticToken {
					t.Error("request bytes/authority changed")
				}
				switch mode {
				case "late-failure":
					w.WriteHeader(500)
					io.WriteString(w, `{"error_code":"APPLICATION_TEXT_STORE_UNAVAILABLE","error":"private-marker","operation_state":{"known":true,"committed":true,"partial":false,"cleanup_failed":true}}`)
				case "redirect":
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(302)
					io.WriteString(w, `{}`)
				case "raw-unicode":
					w.WriteHeader(400)
					io.WriteString(w, `{"error_code":"APPLICATION_TEXT_DRAFT_INVALID"}`)
				case "mismatch":
					io.WriteString(w, strings.Replace(textReceipt, `"item_id":"answer"`, `"item_id":"wrong"`, 1))
				default:
					io.WriteString(w, strings.Replace(textReceipt, `"result":"created"`, `"result":"`+mode+`"`, 1))
				}
			})
			out, e := c.Run(context.Background(), Request{Command: "save-draft", JobID: "plan_job", Payload: []byte(payload)})
			good := mode == "created" || mode == "updated" || mode == "no_change_needed"
			if good {
				if e != nil {
					t.Fatal(e.Code)
				}
				var receipt map[string]any
				json.Unmarshal(out, &receipt)
				if len(receipt) != 7 || receipt["result"] != mode {
					t.Fatal("source receipt changed")
				}
			} else if e == nil || len(out) != 0 {
				t.Fatal("invalid success")
			}
			if mode == "unavailable" {
				if calls != 0 {
					t.Fatal("called unsupported owner")
				}
			} else if calls != 1 {
				t.Fatal("mutation repeated")
			}
			if mode == "late-failure" && (!e.State.Known || e.State.Committed == nil || !*e.State.Committed || !*e.State.CleanupFailed) {
				t.Fatal("lost publication state")
			}
		})
	}
}

func TestApplicationTextClientRejectsExtraAuthority(t *testing.T) {
	c, _ := clientAt(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid request used network") })
	for _, change := range []func(*Request){
		func(r *Request) { r.Approve = true }, func(r *Request) { r.ExpectedRevision = 1 }, func(r *Request) { r.Kind = "cover_letter" },
		func(r *Request) { r.FileFormat = "pdf" }, func(r *Request) { r.JobID = "../escape" }, func(r *Request) { r.Payload = []byte(`null`) },
	} {
		r := Request{Command: "save-draft", JobID: "plan_job", Payload: []byte(textPayload)}
		change(&r)
		if _, e := c.Run(context.Background(), r); e == nil {
			t.Fatal("invalid authority accepted")
		}
	}
}
