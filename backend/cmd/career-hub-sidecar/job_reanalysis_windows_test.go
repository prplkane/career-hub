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
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/jobdiagnostics"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

const reanalysisCoordinatorChild = "CAREERHUB_TEST_REANALYSIS_COORDINATOR_CHILD"
const reanalysisCoordinatorRoot = "CAREERHUB_TEST_REANALYSIS_COORDINATOR_ROOT"
const reanalysisCoordinatorSource = "Company: Fictional Labs\nRole: Backend Engineer\nResponsibilities\nBuild services.\nRequirements\nPython experience required.\n"

func TestReanalysisCoordinatorOwnedProcess(t *testing.T) {
	if os.Getenv(reanalysisCoordinatorChild) == "1" {
		runReanalysisCoordinatorOwned(t, os.Getenv(reanalysisCoordinatorRoot))
		return
	}
	base := t.TempDir()
	data, resources := filepath.Join(base, "data"), filepath.Join(base, "resources")
	materialFixtureFile(t, filepath.Join(data, datahome.MarkerPath), []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`))
	materialFixtureFile(t, filepath.Join(resources, "VERSION"), []byte("1.2.3-dev.01+build.7\n"))
	materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreGroupsPath)), []byte("[]\n"))
	materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreSkillsPath)), []byte("- {id: python, name: Python, category: programming_language}\n"))
	materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(catalogstore.CustomGroupsPath)), []byte("[]\n"))
	materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(catalogstore.CustomSkillsPath)), []byte("[]\n"))
	materialFixtureFile(t, filepath.Join(data, "career_vault", "visa_profile.yaml"), []byte("current_status: citizen\nrequires_sponsorship_now: false\n"))
	materialFixtureFile(t, filepath.Join(data, "jobs", "incoming", "fictional_job.txt"), []byte(reanalysisCoordinatorSource))
	materialFixtureFile(t, filepath.Join(data, "jobs", "normalized", "fictional_job.yaml"), []byte(`id: fictional_job
status: draft
company: Old company
title: Old title
source_url: https://example.test/jobs/1
location: ''
remote_policy: ''
seniority: ''
required_skills: []
required_platforms: []
required_capabilities: []
preferred_skills: []
experience_requirements: []
education_requirements: []
responsibilities: []
visa_signals: {citizenship_required: null, clearance_required: null, sponsorship_mentioned: null, no_visa_or_opt: null}
raw_jd_path: jobs/incoming/fictional_job.txt
`))
	if err := os.MkdirAll(filepath.Join(resources, "src", "intake_ui_static"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReanalysisCoordinatorOwnedProcess$")
	child.Env = append(os.Environ(), reanalysisCoordinatorChild+"=1", reanalysisCoordinatorRoot+"="+base)
	child.WaitDelay = time.Second
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("fictional owned coordinator: %v %.1500s", err, output)
	}
}

func runReanalysisCoordinatorOwned(t *testing.T, base string) {
	t.Helper()
	data, resources := filepath.Join(base, "data"), filepath.Join(base, "resources")
	start := protocol.StartRecord{DataRoot: data, ResourceRoot: resources, Channel: "dev"}
	w, err := readonly.OpenForProcess(start)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := newReanalysisCoordinator(w, start)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	saved, err := w.SavedJob(ctx, "fictional_job")
	if err != nil {
		t.Fatal(err)
	}
	revision, ok := saved.Card["card_revision"].(string)
	if !ok {
		t.Fatal("saved card did not expose a revision")
	}
	out, problem := coordinator.Reanalyze(ctx, "fictional_job", revision)
	if problem != nil || out == nil || !out.Committed || out.CommitUnknown || out.RunID == "" {
		t.Fatalf("owned coordinator did not publish: %#v %#v", out, problem)
	}
	var result struct {
		Job       map[string]any `json:"job"`
		RunID     string         `json:"run_id"`
		Unchanged bool           `json:"unchanged"`
	}
	if json.Unmarshal(out.Result, &result) != nil || result.Job["id"] != "fictional_job" || result.RunID != out.RunID || result.Unchanged {
		t.Fatal("published card did not read back", result)
	}
	home, err := w.StartupHome()
	if err != nil {
		t.Fatal(err)
	}
	page, readErr := jobdiagnostics.ReadPage(ctx, data, home, "0")
	if readErr != nil || len(page.Events) != 3 {
		t.Fatalf("planned run not durably visible: %+v %v", page, readErr)
	}
	second, secondProblem := coordinator.Reanalyze(ctx, "fictional_job", result.Job["card_revision"].(string))
	if secondProblem != nil || second == nil || second.Committed || second.CommitUnknown {
		t.Fatalf("already-current card was republished: %#v %#v", second, secondProblem)
	}
	page, readErr = jobdiagnostics.ReadPage(ctx, data, home, "0")
	if readErr != nil || len(page.Events) != 5 {
		t.Fatalf("no-op journal sequence missing: %+v %v", page, readErr)
	}
	last, lastErr := jobdiagnostics.ReadLastEvent(ctx, data, home)
	if lastErr != nil || last == nil || last.Event != "completed" || last.Sequence != 2 || last.CommitAttempted || last.Committed {
		t.Fatalf("no-op journal invented a write: %+v %v", last, lastErr)
	}
	raw, err := os.ReadFile(filepath.Join(data, "jobs", "incoming", "fictional_job.txt"))
	if err != nil || !bytes.Equal(raw, []byte(reanalysisCoordinatorSource)) {
		t.Fatal("coordinator changed retained raw JD", err)
	}
	cardPath := filepath.Join(data, "jobs", "normalized", "fictional_job.yaml")
	cardBefore, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	start.SessionToken = testSessionToken
	h, err := preparedApplication(w, start).Handler(8766)
	if err != nil {
		t.Fatal(err)
	}
	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	sessionRequest.Header.Set(httpapi.CapabilityHeader, testSessionToken)
	sessionRequest.Host = "127.0.0.1:8766"
	session := httptest.NewRecorder()
	h.ServeHTTP(session, sessionRequest)
	var sessionValue struct {
		Token        string         `json:"token"`
		Capabilities map[string]any `json:"capabilities"`
	}
	sessionValue.Token = testSessionToken // the response no longer carries it
	if session.Code != http.StatusOK || json.Unmarshal(session.Body.Bytes(), &sessionValue) != nil ||
		sessionValue.Token == "" || sessionValue.Capabilities["job_reanalysis_revision"] != true {
		t.Fatalf("native session did not advertise revisioned reanalysis: %d %.300s", session.Code, session.Body.String())
	}
	post := func(expected string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"expected_card_revision": expected})
		r := httptest.NewRequest(http.MethodPost, "/api/jobs/fictional_job/reanalyze", bytes.NewReader(body))
		r.Host = "127.0.0.1:8766"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(httpapi.CapabilityHeader, sessionValue.Token)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, r)
		return response
	}
	currentRevision := result.Job["card_revision"].(string)
	throughHTTP := post(currentRevision)
	var httpResult struct {
		Job       map[string]any `json:"job"`
		Unchanged bool           `json:"unchanged"`
	}
	if throughHTTP.Code != http.StatusOK || json.Unmarshal(throughHTTP.Body.Bytes(), &httpResult) != nil ||
		httpResult.Job["card_revision"] != currentRevision || !httpResult.Unchanged {
		t.Fatalf("owned HTTP route failed checked no-op: %d %.300s", throughHTTP.Code, throughHTTP.Body.String())
	}
	stale := post("sha256:" + strings.Repeat("0", 64))
	var conflict map[string]any
	if stale.Code != http.StatusConflict || json.Unmarshal(stale.Body.Bytes(), &conflict) != nil ||
		conflict["error_code"] != "JOB_REANALYSIS_REVISION_CONFLICT" {
		t.Fatalf("stale HTTP revision was not refused: %d %.300s", stale.Code, stale.Body.String())
	}
	cardAfter, err := os.ReadFile(cardPath)
	if err != nil || !bytes.Equal(cardBefore, cardAfter) {
		t.Fatal("HTTP no-op or stale revision changed the saved card", err)
	}
}
