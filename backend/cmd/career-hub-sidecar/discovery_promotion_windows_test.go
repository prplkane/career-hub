//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/diagnosticslog"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/discovery/promotiondiagnostics"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/skills/cataloginput"
)

func TestDiscoveryPromotionNative(t *testing.T) {
	if base := os.Getenv("CAREERHUB_PROMOTION_TEST_ROOT"); base != "" {
		runDiscoveryPromotionNative(t, base, os.Getenv("CAREERHUB_PROMOTION_TEST_ACTION"))
		return
	}
	for _, action := range []string{"save_job", "prepare_application", "record_as_applied", "retry_link", "retry_changed_source", "split_pane", "missing_namespaces"} {
		t.Run(action, func(t *testing.T) {
			base := newJobHostFixture(t)
			installFreshJobAssessmentFixture(t, base)
			if action == "missing_namespaces" {
				// Reproduce first-use Bootstrap: it creates jobs/, but not the
				// intake namespaces. Remove only these empty test directories.
				for _, name := range []string{"incoming", "normalized"} {
					if err := os.Remove(filepath.Join(base, "data", "jobs", name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDiscoveryPromotionNative$")
			child.Env = append(os.Environ(), "CAREERHUB_PROMOTION_TEST_ROOT="+base, "CAREERHUB_PROMOTION_TEST_ACTION="+action)
			child.WaitDelay = time.Second
			if output, err := child.CombinedOutput(); err != nil {
				t.Fatalf("native promotion %s: %v %.3000s", action, err, output)
			}
		})
	}
}

func runDiscoveryPromotionNative(t *testing.T, base, action string) {
	ctx := context.Background()
	start := protocol.StartRecord{DataRoot: filepath.Join(base, "data"), ResourceRoot: filepath.Join(base, "resources"), Channel: "dev"}
	if action == "split_pane" {
		_, file, _, _ := runtime.Caller(0)
		start.ResourceRoot = filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	}
	w, err := readonly.OpenForProcess(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schema.PrepareOwned(ctx, start.DataRoot); err != nil {
		t.Fatal(err)
	}
	home, err := w.StartupHome()
	if err != nil {
		t.Fatal(err)
	}
	owner := applicationwrite.New(start.DataRoot).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
	source := reanalysisCoordinatorSource
	if action == "retry_changed_source" {
		source += "\nA changed posting requires PostgreSQL database administration."
	}
	if action == "split_pane" {
		source = "Integration Engineer\nAbout the role\nGENERIC DETAIL ONLY. Build reliable integrations used by partner and operations teams across the company.\nResponsibilities\nOwn API integrations, production monitoring, incident follow-up, automated testing, and concise technical documentation.\nQualifications\nExperience with Python, REST APIs, SQL, debugging distributed systems, and collaborative software delivery."
		inspected, err := w.InspectNewJob(ctx, readonly.NewJobRequest{SourceName: "split.txt", RawText: source, SourceURL: "http://127.0.0.1:45678/job/generic-split", IdempotencyKey: "split-fixture"}, "Northstar", "Integration Engineer")
		if err != nil || inspected == nil || inspected.Prepared == nil {
			t.Fatalf("split inspection: %v", err)
		}
		value, parseErr := cataloginput.ParseYAML(inspected.Prepared.CardBytes)
		report, validationErr := jobcard.Validate(ctx, value, func(context.Context, string) (bool, *jobcard.Error) { return true, nil })
		if parseErr != nil || validationErr != nil || report == nil || !report.SchemaValid() {
			t.Fatalf("split draft schema: %v %v %+v", parseErr, validationErr, report)
		}
		action = "save_job"
	}
	captured, problem := owner.CaptureDiscovery(ctx, discovery.CaptureRequest{
		"source_kind": "extension", "source_channel": "page", "request_fingerprint": "promotion-fixture", "observed_url": "https://example.test/jobs/promote",
		"title": "Backend Engineer", "company": "Fictional Labs", "content_scope": "page_visible", "source_text": source,
	})
	if problem != nil {
		t.Fatal(problem)
	}
	var capture struct {
		Discovery discovery.Record `json:"discovery"`
	}
	if json.Unmarshal(captured.Result, &capture) != nil || capture.Discovery.DiscoveryID == "" {
		t.Fatalf("capture unavailable: %.300s", captured.Result)
	}
	id := capture.Discovery.DiscoveryID
	start.SessionToken = testSessionToken
	h, err := preparedApplication(w, start).Handler(8766)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/session", nil)
	r.Header.Set(httpapi.CapabilityHeader, testSessionToken)
	r.Host = "127.0.0.1:8766"
	response := httptest.NewRecorder()
	h.ServeHTTP(response, r)
	var session struct {
		Token string `json:"token"`
	}
	session.Token = testSessionToken // the response no longer carries it
	if json.Unmarshal(response.Body.Bytes(), &session) != nil || session.Token == "" {
		t.Fatal("session unavailable")
	}
	post := func(action string, revision int64, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"contract_version": 1, "action": action, "expected_revision": revision})
		r := httptest.NewRequest(http.MethodPost, "/api/discoveries/"+id+"/promote", bytes.NewReader(raw))
		r.Host = "127.0.0.1:8766"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(httpapi.CapabilityHeader, session.Token)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, r)
		var value map[string]any
		if response.Code != want || json.Unmarshal(response.Body.Bytes(), &value) != nil {
			t.Fatalf("promotion response: %d %.900s", response.Code, response.Body.String())
		}
		runID, _ := value["run_id"].(string)
		if runID == "" {
			t.Fatal("promotion has no parent diagnostic identity")
		}
		cursor, found := "", false
		for pages := 0; pages < 8; pages++ {
			page, err := diagnosticslog.ReadPage(ctx, start.DataRoot, home, cursor)
			if err != nil {
				t.Fatal("parent not readable through Diagnostics", err)
			}
			for _, raw := range page.Events {
				var event map[string]any
				if json.Unmarshal(raw, &event) == nil && event["run_id"] == runID {
					wantState := "success"
					if want != 200 {
						wantState = "error"
					}
					if event["component"] != "discovery_promotion" || event["status"] != wantState {
						t.Fatal("child or wrong status returned as parent", event)
					}
					found = true
				}
			}
			if !page.HasMore {
				break
			}
			cursor = page.Cursor
		}
		if !found {
			t.Fatal("parent missing from paged diagnostics")
		}
		return value
	}
	post("save_job", capture.Discovery.Revision, 409) // Never promotes an unconfirmed page.
	confirmed, problem := owner.EditDiscovery(ctx, discovery.EditRequest{Operation: "confirm", DiscoveryID: id, ExpectedRevision: capture.Discovery.Revision})
	if problem != nil {
		t.Fatal(problem)
	}
	var detail discovery.Record
	if json.Unmarshal(confirmed.Result, &detail) != nil {
		t.Fatal("confirmation unavailable")
	}
	post("save_job", capture.Discovery.Revision, 409) // Stale confirmation cannot create a job.
	if action == "missing_namespaces" {
		// First use must succeed without pre-seeding either namespace.
		// The two earlier rejected requests must not have created them.
		for _, name := range []string{"incoming", "normalized"} {
			if _, err := os.Stat(filepath.Join(start.DataRoot, "jobs", name)); !os.IsNotExist(err) {
				t.Fatal("unconfirmed or stale request created a namespace", err)
			}
		}
		action = "save_job"
	}
	entries, err := os.ReadDir(filepath.Join(start.DataRoot, "jobs/incoming"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	before := len(entries)
	wantDuplicate := action == "retry_link"
	if wantDuplicate || action == "retry_changed_source" {
		// Reproduce an interrupted composition after successful intake but
		// before linking. Retrying must reuse the durable discovery key.
		out, problem := jobIntakeAction(w, start)(ctx, httpapi.JobIntakeRequest{SourceText: reanalysisCoordinatorSource, SourceName: id + ".txt", Company: "Fictional Labs", Title: "Backend Engineer", SourceURL: "https://example.test/jobs/promote", IdempotencyKey: "discovery:" + id, InitialStatus: "captured"})
		if problem != nil || out == nil || !out.Committed {
			t.Fatal("pre-link intake failed", problem)
		}
		if action == "retry_changed_source" {
			refused := post("record_as_applied", detail.Revision, 409)
			if refused["error_code"] != "JOB_INTAKE_RETRY_CONFLICT" {
				t.Fatal("changed source retry did not refuse", refused)
			}
			entries, err := os.ReadDir(filepath.Join(start.DataRoot, "jobs/incoming"))
			if err != nil || len(entries) != before+1 {
				t.Fatal("changed-input retry created another job", err)
			}
			var capturedJob struct {
				Job map[string]any `json:"job"`
			}
			if json.Unmarshal(out.Result, &capturedJob) != nil {
				t.Fatal("missing original job")
			}
			jobID, _ := capturedJob.Job["id"].(string)
			application, err := w.Application(ctx, jobID)
			encoded, _ := json.Marshal(application)
			if err != nil || !bytes.Contains(encoded, []byte(`"current_status":"captured"`)) {
				t.Fatal("changed retry marked original job applied", err)
			}
			prepared, problem := owner.EditDiscovery(ctx, discovery.EditRequest{Operation: "prepare", DiscoveryID: id, ExpectedRevision: detail.Revision})
			var still discovery.Detail
			if problem != nil || json.Unmarshal(prepared.Result, &still) != nil || still.PromotedJobID != "" || still.SourceText != source {
				t.Fatal("changed retry linked or replaced discovery", problem)
			}
			return
		}
		action = "record_as_applied"
	}
	fresh := post(action, detail.Revision, 200)
	if fresh["outcome"] != "promoted" || fresh["duplicate"] != wantDuplicate {
		t.Fatal("promotion identity", fresh)
	}
	job := fresh["job"].(map[string]any)
	jobID := job["id"].(string)
	wantStatus := map[string]string{"save_job": "captured", "prepare_application": "preparing", "record_as_applied": "applied"}[action]
	if job["application"].(map[string]any)["current_status"] != wantStatus {
		t.Fatal("action status", job)
	}
	if fresh["discovery"].(map[string]any)["promoted_job_id"] != jobID {
		t.Fatal("missing link")
	}
	repeated := post(action, detail.Revision, 200) // Old revision is allowed only for the same existing link.
	if repeated["outcome"] != "already_promoted" || repeated["job"].(map[string]any)["id"] != jobID || repeated["duplicate"] != true {
		t.Fatal("repeat changed identity")
	}
	entries, err = os.ReadDir(filepath.Join(start.DataRoot, "jobs/incoming"))
	if err != nil || len(entries) != before+1 {
		t.Fatal("retry created duplicate raw job", err, len(entries), before)
	}
	if _, problem := owner.SetStatus(ctx, jobID, "interview", nil, "", nil); problem != nil {
		t.Fatal(problem)
	}
	terminal := post("record_as_applied", detail.Revision, 200)
	if terminal["job"].(map[string]any)["application"].(map[string]any)["current_status"] != "interview" {
		t.Fatal("promotion downgraded later status")
	}
	entries, err = os.ReadDir(filepath.Join(start.DataRoot, "career_vault/logs/native-intake"))
	if err != nil || len(entries) != 5 {
		t.Fatal("one parent required per request", err, len(entries))
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(start.DataRoot, "career_vault/logs/native-intake", entry.Name()))
		diagnostic, de := promotiondiagnostics.Decode(raw)
		if err != nil || de != nil {
			t.Fatal("invalid native diagnostic", err, de)
		}
		if diagnostic.RunID == fresh["run_id"] && (diagnostic.ChildRunID == "" || !diagnostic.Committed) {
			t.Fatal("fresh promotion lost child intake or commit")
		}
		if diagnostic.RunID == repeated["run_id"] && diagnostic.ChildRunID != "" {
			t.Fatal("repeat invented child intake")
		}
	}
}
