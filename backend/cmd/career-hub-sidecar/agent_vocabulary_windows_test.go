//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/catalogwrite"
	"careerhub.local/backend/internal/storage/datahome"
)

func TestExactVocabularyOwnedProcess(t *testing.T) {
	const childFlag, rootFlag = "CAREERHUB_TEST_VOCABULARY_CHILD", "CAREERHUB_TEST_VOCABULARY_ROOT"
	if os.Getenv(childFlag) == "1" {
		base := os.Getenv(rootFlag)
		start := protocol.StartRecord{DataRoot: filepath.Join(base, "data"), ResourceRoot: filepath.Join(base, "resources"), Channel: "dev"}
		w, err := readonly.OpenForProcess(start)
		if err != nil {
			t.Fatal(err)
		}
		c := newExactVocabularyCoordinator(w, start, newReanalysisCoordinator(w, start))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		request := catalogwrite.ExactVocabularyRequest{JobID: "fictional_job", Name: "NebulaDB", PrimaryGroupID: "databases_storage", ConfirmedExact: true}
		out, problem := c.Run(ctx, request)
		var receipt map[string]any
		if problem != nil || out == nil || json.Unmarshal(out.Receipt, &receipt) != nil {
			t.Fatalf("owned vocabulary failed: %v", problem)
		}
		mode := os.Getenv("CAREERHUB_TEST_VOCABULARY_MODE")
		if mode == "restart" {
			if receipt["result"] != "no_change_needed" || out.Registry.Committed || out.Reanalysis != nil {
				t.Fatal("restart repeated write/reanalysis")
			}
			return
		}
		if receipt["result"] != "created" || receipt["job_reanalyzed"] != true || receipt["reanalysis_warning"] != nil || !out.Registry.Write.Completed || out.Reanalysis == nil || !out.Reanalysis.Committed || out.Reanalysis.RunID == "" {
			t.Fatalf("two-stage success not confirmed: %s %+v", out.Receipt, out.ReanalysisError)
		}
		card, err := os.ReadFile(filepath.Join(start.DataRoot, "jobs/normalized/fictional_job.yaml"))
		if err != nil || !bytes.Contains(card, []byte("user_nebuladb")) {
			t.Fatal("new skill missing from JD digest", err)
		}
		start.SessionToken = testSessionToken
		h, err := preparedApplication(w, start).Handler(8766)
		if err != nil {
			t.Fatal(err)
		}
		session := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		r.Header.Set(httpapi.CapabilityHeader, testSessionToken)
		r.Host = "127.0.0.1:8766"
		h.ServeHTTP(session, r)
		var bootstrap struct {
			Token        string         `json:"token"`
			Capabilities map[string]any `json:"capabilities"`
		}
		bootstrap.Token = testSessionToken // the response no longer carries it
		if session.Code != 200 || json.Unmarshal(session.Body.Bytes(), &bootstrap) != nil || bootstrap.Capabilities["exact_vocabulary"] != float64(1) {
			t.Fatal("native capability unavailable")
		}
		post := func() map[string]any {
			r := httptest.NewRequest(http.MethodPost, "/api/jobs/fictional_job/vocabulary/exact", strings.NewReader(`{"name":"NebulaQueue","primary_group_id":"databases_storage","confirmed_exact":true}`))
			r.Host = "127.0.0.1:8766"
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Career-Hub-Token", bootstrap.Token)
			reply := httptest.NewRecorder()
			h.ServeHTTP(reply, r)
			var value map[string]any
			if reply.Code != 200 || json.Unmarshal(reply.Body.Bytes(), &value) != nil {
				t.Fatalf("native exact HTTP: %d %.600s", reply.Code, reply.Body.String())
			}
			return value
		}
		captured := post()
		if captured["result"] != "created" || captured["job_reanalyzed"] != true || captured["candidate_evidence_changed"] != false {
			t.Fatal("HTTP composition lost source semantics")
		}
		stages := captured["stages"].(map[string]any)
		if stages["registry"].(map[string]any)["committed"] != true || stages["reanalysis"].(map[string]any)["committed"] != true {
			t.Fatal("HTTP stage facts missing")
		}
		reused := post()
		if reused["result"] != "no_change_needed" || reused["stages"].(map[string]any)["reanalysis"] != nil {
			t.Fatal("HTTP reuse reanalyzed")
		}
		card, err = os.ReadFile(filepath.Join(start.DataRoot, "jobs/normalized/fictional_job.yaml"))
		if err != nil || !bytes.Contains(card, []byte("user_nebulaqueue")) {
			t.Fatal("HTTP skill not recognized in saved JD")
		}
		before, err := os.Stat(filepath.Join(start.DataRoot, catalogstore.CustomSkillsPath))
		if err != nil {
			t.Fatal(err)
		}
		request.PrimaryGroupID = "missing-group"
		second, e := c.Run(ctx, request)
		if e != nil || second.Reanalysis != nil || second.Registry.Committed {
			t.Fatal("reuse changed a saved job")
		}
		after, err := os.Stat(filepath.Join(start.DataRoot, catalogstore.CustomSkillsPath))
		if err != nil || before.ModTime() != after.ModTime() {
			t.Fatal("no-op changed catalog mtime")
		}
		return
	}
	base := t.TempDir()
	data, resources := filepath.Join(base, "data"), filepath.Join(base, "resources")
	fixture := map[string]string{
		filepath.Join(data, datahome.MarkerPath):               `{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`,
		filepath.Join(resources, "VERSION"):                    "1.2.3-dev.01+build.7\n",
		filepath.Join(resources, catalogstore.CoreGroupsPath):  "- {id: databases_storage, name: Databases}\n",
		filepath.Join(resources, catalogstore.CoreSkillsPath):  "[]\n",
		filepath.Join(data, catalogstore.CustomGroupsPath):     "[]\n",
		filepath.Join(data, catalogstore.CustomSkillsPath):     "[]\n",
		filepath.Join(data, "career_vault/visa_profile.yaml"):  "current_status: citizen\nrequires_sponsorship_now: false\n",
		filepath.Join(data, "jobs/incoming/fictional_job.txt"): strings.ReplaceAll(reanalysisCoordinatorSource, "Python", "NebulaDB and NebulaQueue"),
		filepath.Join(data, "jobs/normalized/fictional_job.yaml"): `id: fictional_job
status: draft
company: Fictional Labs
title: Backend Engineer
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
`,
	}
	for path, raw := range fixture {
		materialFixtureFile(t, path, []byte(raw))
	}
	if err := os.MkdirAll(filepath.Join(resources, "src/intake_ui_static"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"1", "restart"} {
		// Each process receives its own claim; no runtime survives the fixture.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExactVocabularyOwnedProcess$")
		// Both values must enter the child branch.
		child.Env = append(os.Environ(), childFlag+"=1", rootFlag+"="+base, "CAREERHUB_TEST_VOCABULARY_MODE="+mode)
		child.WaitDelay = time.Second
		output, err := child.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("owned vocabulary %s: %v %.1600s", mode, err, output)
		}
	}
	for path, raw := range fixture {
		if path == filepath.Join(data, catalogstore.CustomSkillsPath) || path == filepath.Join(data, "jobs/normalized/fictional_job.yaml") {
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != raw {
			t.Fatal("changed raw JD, candidate data or core catalog", err)
		}
	}
	if err := filepath.WalkDir(data, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			switch strings.ToLower(filepath.Ext(path)) {
			case ".db", ".sqlite", ".sqlite3":
				t.Fatal("unexpected database creation")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
