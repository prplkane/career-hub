//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	applicationschema "careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/datahome/privacy"
	"careerhub.local/backend/internal/intakeextract"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sidecar/protocol"
	_ "modernc.org/sqlite"
)

type resumeManagementHTTPFixture struct{ Root, Resources, Mode string }

func resumeManagementSQL(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "applications", "career_hub.sqlite3")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, q := range []string{
		"PRAGMA user_version=27",
		`CREATE TABLE resume_library(resume_id TEXT PRIMARY KEY,display_name TEXT,relative_path TEXT,file_name TEXT,sha256 TEXT,size_bytes INTEGER,added_at TEXT,archived_at TEXT)`,
		`CREATE TABLE application_resume_attachments(resume_id TEXT)`,
		`CREATE TABLE job_applications(applied_resume_id TEXT)`,
		`CREATE TABLE application_applied_resume_events(resume_id TEXT,action TEXT)`,
		`INSERT INTO resume_library VALUES('r1','Fictional Alpha','applications/resumes/r1.txt','r1.txt','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',12,'2026-01-01',NULL)`,
		`INSERT INTO resume_library VALUES('r2','Fictional Beta','applications/resumes/r2.PDF','r2.PDF','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',12,'2026-01-02','2026-02-01')`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if e := db.Close(); e != nil {
		t.Fatal(e)
	}
}

func resumeManagementReport(t *testing.T, repo, name string) map[string]any {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(repo, "tests", "fixtures", name))
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Base map[string]any `json:"base"`
	}
	if e := json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	return v.Base
}

func TestResumeDigestRealHTTPAndRestartReadback(t *testing.T) {
	root := t.TempDir()
	data, resources := filepath.Join(root, "data"), filepath.Join(root, "resources")
	materialFixtureFile(t, filepath.Join(resources, "VERSION"), []byte("1.2.3-dev.01+build.7\n"))
	materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreGroupsPath)), []byte("[]\n"))
	materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreSkillsPath)), []byte("- {id: core, name: Core, category: tool}\n"))
	if e := os.MkdirAll(filepath.Join(resources, "src", "intake_ui_static"), 0700); e != nil {
		t.Fatal(e)
	}
	materialFixtureFile(t, filepath.Join(data, datahome.MarkerPath), []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`))
	materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(catalogstore.CustomGroupsPath)), []byte("- id: user_my_custom_skills\n  name: My custom skills\n  archived: false\n"))
	materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(catalogstore.CustomSkillsPath)), []byte("[]\n"))
	if _, e := privacy.ApplyPrivateTree(context.Background(), data); e != nil {
		t.Fatalf("fixture privacy: %#v", e)
	}
	if _, e := datahome.Bootstrap(context.Background(), data, "dev"); e != nil {
		t.Fatalf("fixture bootstrap: %#v", e)
	}
	resumeID := "resume-digest-one"
	relative := resumelibrary.LibraryPath + "/" + resumeID + ".txt"
	source := []byte("Core\n- Built a fictional local workflow.\n")
	for _, mode := range []string{"prepare_digest", "digest", "suggest_digest", "verify_digest"} {
		input, _ := json.Marshal(resumeManagementHTTPFixture{Root: data, Resources: resources, Mode: mode})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResumeManagementRealHTTPHelper$")
		cmd.Env = append(os.Environ(), "CAREERHUB_RESUME_MANAGEMENT_HTTP_HELPER=1")
		cmd.Stdin = bytes.NewReader(input)
		materialRunTool(t, cmd)
		cancel()
	}
	current, e := os.ReadFile(filepath.Join(data, filepath.FromSlash(relative)))
	if e != nil || !bytes.Equal(current, source) {
		t.Fatal("digest changed the immutable Resume Version")
	}
	runs, e := os.ReadDir(filepath.Join(data, "career_vault", "intake_runs"))
	if e != nil || len(runs) != 1 || !runs[0].IsDir() {
		t.Fatal("digest did not publish exactly one restart-readable run")
	}
	if _, e = os.Stat(filepath.Join(data, filepath.FromSlash(datahome.IntakeRecoveryPath))); !os.IsNotExist(e) {
		t.Fatal("clean digest left a recovery sentinel")
	}
}

func prepareResumeDigestHTTPFixture(t *testing.T, data string) {
	t.Helper()
	owned, pathErr := runtimepaths.Open(data)
	if pathErr != nil {
		t.Fatal(pathErr.Code)
	}
	if pathErr = owned.ClaimForProcess(); pathErr != nil {
		t.Fatal(pathErr.Code)
	}
	defer owned.Close()
	if _, e := applicationschema.PrepareOwned(context.Background(), data); e != nil {
		t.Fatal(e)
	}
	if _, pathErr = owned.EnsureDirectory(resumelibrary.LibraryPath); pathErr != nil {
		t.Fatal(pathErr.Code)
	}
	resumeID := "resume-digest-one"
	relative := resumelibrary.LibraryPath + "/" + resumeID + ".txt"
	source := []byte("Core\n- Built a fictional local workflow.\n")
	if created, createErr := owned.CreateFile(relative, source); createErr != nil || !created {
		t.Fatal("resume source fixture unavailable")
	}
	sum := sha256.Sum256(source)
	db, e := sql.Open("sqlite", filepath.Join(data, "applications", "career_hub.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO resume_library(resume_id,display_name,relative_path,file_name,sha256,size_bytes,added_at) VALUES(?,?,?,?,?,?,?)`,
		resumeID, "Fictional Digest Resume", relative, resumeID+".txt", hex.EncodeToString(sum[:]), len(source), "2026-09-20T00:00:00Z"); e != nil {
		t.Fatal(e)
	}
}

func TestResumeManagementRealHTTP(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	for _, mode := range []string{"missing", "matched", "approval", "stale", "malformed_review", "malformed_extraction", "large_report", "run_file", "catalog_corrupt", "sql_schema_corrupt", "sql_value_unsupported", "child_129"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			data, resources := filepath.Join(root, "data"), filepath.Join(root, "resources")
			materialFixtureFile(t, filepath.Join(resources, "VERSION"), []byte("1.2.3-dev.01+build.7\n"))
			materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreGroupsPath)), []byte("[]\n"))
			materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreSkillsPath)), []byte("- {id: core, name: Core, category: tool}\n"))
			if e := os.MkdirAll(filepath.Join(resources, "src", "intake_ui_static"), 0700); e != nil {
				t.Fatal(e)
			}
			materialFixtureFile(t, filepath.Join(data, datahome.MarkerPath), []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`))
			materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(catalogstore.CustomGroupsPath)), []byte("- id: user_my_custom_skills\n  name: My custom skills\n  archived: false\n"))
			materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(catalogstore.CustomSkillsPath)), []byte("[]\n"))
			// A real initialized Home carries the private DACL that startup now
			// re-verifies (ADR SEC-250); a hand-written marker alone does not.
			if _, e := privacy.ApplyPrivateTree(context.Background(), data); e != nil {
				t.Fatalf("fixture privacy: %#v", e)
			}
			if _, e := datahome.Bootstrap(context.Background(), data, "dev"); e != nil {
				t.Fatalf("fixture bootstrap: %#v", e)
			}
			resumeManagementSQL(t, data)
			if mode != "missing" && mode != "run_file" && mode != "catalog_corrupt" && mode != "sql_schema_corrupt" && mode != "sql_value_unsupported" {
				snapshot, catalogErr := catalogstore.Load(resources, data)
				if catalogErr != nil {
					t.Fatalf("catalog: %#v", catalogErr)
				}
				report := resumeManagementReport(t, repo, "migration_intake_extraction_cases.json")
				report["registry_revision"] = snapshot.CatalogRevision()
				report["extractor_version"] = intakeextract.ExtractorVersion
				if mode == "stale" {
					report["registry_revision"] = strings.Repeat("b", 64)
				}
				raw, e := json.Marshal(report)
				if e != nil {
					t.Fatal(e)
				}
				run := filepath.Join(data, "career_vault", "intake_runs", "src_fictional_1234567890")
				path := filepath.Join(run, "extracted.yaml")
				materialFixtureFile(t, path, raw)
				stamp := time.Unix(1700000000, 123456000)
				if e := os.Chtimes(path, stamp, stamp); e != nil {
					t.Fatal(e)
				}
				if mode == "approval" {
					raw, e = json.Marshal(resumeManagementReport(t, repo, "migration_intake_approval_cases.json"))
					if e != nil {
						t.Fatal(e)
					}
					materialFixtureFile(t, filepath.Join(run, "approval_result.yaml"), raw)
				}
				if mode == "malformed_review" {
					materialFixtureFile(t, filepath.Join(run, "review.yaml"), []byte("invalid: ["))
				}
				if mode == "malformed_extraction" {
					materialFixtureFile(t, path, []byte("invalid: ["))
				}
				if mode == "large_report" {
					materialFixtureFile(t, filepath.Join(run, "review.yaml"), []byte(strings.Repeat("x", (2<<20)+1)))
				}
				if mode == "child_129" {
					for i := 0; i < 128; i++ {
						materialFixtureFile(t, filepath.Join(run, fmt.Sprintf("extra_%03d", i)), []byte(""))
					}
				}
			}
			if mode == "sql_schema_corrupt" {
				db, e := sql.Open("sqlite", filepath.Join(data, "applications", "career_hub.sqlite3"))
				if e != nil {
					t.Fatal(e)
				}
				if _, e := db.Exec(`ALTER TABLE resume_library RENAME COLUMN sha256 TO private_bad_column`); e != nil {
					t.Fatal(e)
				}
				if e := db.Close(); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "sql_value_unsupported" {
				db, e := sql.Open("sqlite", filepath.Join(data, "applications", "career_hub.sqlite3"))
				if e != nil {
					t.Fatal(e)
				}
				if _, e := db.Exec(`UPDATE resume_library SET sha256=NULL WHERE resume_id='r1'`); e != nil {
					t.Fatal(e)
				}
				if e := db.Close(); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "run_file" {
				materialFixtureFile(t, filepath.Join(data, "career_vault", "intake_runs", "not_a_directory"), []byte("fictional"))
			}
			before := materialTree(t, data)
			input, _ := json.Marshal(resumeManagementHTTPFixture{Root: data, Resources: resources, Mode: mode})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResumeManagementRealHTTPHelper$")
			cmd.Env = append(os.Environ(), "CAREERHUB_RESUME_MANAGEMENT_HTTP_HELPER=1")
			cmd.Stdin = bytes.NewReader(input)
			materialRunTool(t, cmd)
			if mode == "matched" {
				// The second runtime must reconstruct the same read without a fixture write.
				again := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResumeManagementRealHTTPHelper$")
				again.Env = append(os.Environ(), "CAREERHUB_RESUME_MANAGEMENT_HTTP_HELPER=1")
				again.Stdin = bytes.NewReader(input)
				materialRunTool(t, again)
			}
			if !reflect.DeepEqual(before, materialTree(t, data)) {
				t.Fatal("management HTTP mutated source database or files")
			}
		})
	}
}

func TestResumeManagementRealHTTPHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_RESUME_MANAGEMENT_HTTP_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var f resumeManagementHTTPFixture
	if e := json.NewDecoder(io.LimitReader(os.Stdin, 16<<10)).Decode(&f); e != nil {
		t.Fatal(e)
	}
	if f.Mode == "prepare_digest" {
		prepareResumeDigestHTTPFixture(t, f.Root)
		return
	}
	if f.Mode == "prepare_attachments" {
		prepareResumeDigestHTTPFixture(t, f.Root)
		seedAttachmentFixture(t, f.Root)
		return
	}
	input, parentInput, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	parentOutput, output, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	exited := make(chan int, 1)
	go func() { exited <- execute([]string{"--sidecar-bootstrap"}, input, output) }()
	defer func() {
		_ = parentInput.Close()
		select {
		case code := <-exited:
			if code != 0 {
				t.Errorf("runtime exit %d", code)
			}
		case <-time.After(5 * time.Second):
			t.Error("runtime did not stop")
		}
		_ = parentOutput.Close()
	}()
	if e := protocol.WriteRecord(parentInput, map[string]any{"command": "start", "protocol_version": 1, "port": 0, "channel": "dev", "resource_root": f.Resources, "data_root": f.Root, "session_token": testSessionToken}); e != nil {
		t.Fatal(e)
	}
	ready, e := protocol.ReadRecord(parentOutput)
	if e != nil || ready["event"] != "ready" {
		t.Fatalf("startup: %v %v", e, ready)
	}
	port, ok := ready["port"].(json.Number)
	if !ok {
		t.Fatal("readiness port")
	}
	c := materialWireClient{url: "http://127.0.0.1:" + string(port)}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	c.client = &http.Client{Transport: transport, Timeout: 20 * time.Second}
	c.token = testSessionToken
	session, _ := c.call(t, "GET", "/api/session", nil, 200)
	var auth struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(session, &auth) != nil || auth.Token != "" {
		t.Fatal("session unavailable or disclosed a token")
	}
	if f.Mode == "attachments" {
		for _, job := range []string{"registered", "legacy"} {
			raw, headers := c.call(t, "GET", "/api/jobs/"+job+"/application/resume/file", nil, 200)
			if string(raw) != "Core\n- Built a fictional local workflow.\n" || headers.Get("Content-Disposition") != "inline; filename*=UTF-8''resume-digest-one.txt" {
				t.Fatal("native attachment bytes changed", string(raw), headers)
			}
		}
		c.call(t, "GET", "/api/jobs/missing/application/resume/file", nil, 404)
		c.call(t, "GET", "/api/jobs/invalid/application/resume/file", nil, 409)
		return
	}
	if f.Mode == "digest" || f.Mode == "suggest_digest" || f.Mode == "verify_digest" {
		if f.Mode == "digest" {
			raw, _ := c.call(t, "POST", "/api/resumes/resume-digest-one/digest", nil, 201)
			var result struct {
				ResumeID        string `json:"resume_id"`
				SourceID        string `json:"source_id"`
				TechnologyCount int    `json:"technology_count"`
				BulletCount     int    `json:"bullet_count"`
			}
			if json.Unmarshal(raw, &result) != nil || result.ResumeID != "resume-digest-one" || !strings.HasPrefix(result.SourceID, "src_") || result.TechnologyCount != 1 || result.BulletCount != 1 {
				t.Fatal("invalid native digest result", string(raw))
			}
			logsRaw, _ := c.call(t, "GET", "/api/logs?after=0", nil, 200)
			var logs struct {
				Cursor string           `json:"cursor"`
				Events []map[string]any `json:"events"`
			}
			if json.Unmarshal(logsRaw, &logs) != nil || len(logs.Events) != 1 || logs.Events[0]["source"] != "Resume digest" ||
				logs.Events[0]["status"] != "success" || len(logs.Cursor) > 128 || bytes.Contains(logsRaw, []byte(result.SourceID)) {
				t.Fatal("native digest diagnostic was not safely visible", string(logsRaw))
			}
		}
		raw, _ := c.call(t, "GET", "/api/resumes?include_archived=true", nil, 200)
		var payload struct {
			Resumes []struct {
				ID             string  `json:"id"`
				DigestStatus   string  `json:"digest_status"`
				DigestSourceID *string `json:"digest_source_id"`
			} `json:"resumes"`
		}
		if json.Unmarshal(raw, &payload) != nil || len(payload.Resumes) != 1 || payload.Resumes[0].ID != "resume-digest-one" || payload.Resumes[0].DigestStatus != "in_progress" || payload.Resumes[0].DigestSourceID == nil || !strings.HasPrefix(*payload.Resumes[0].DigestSourceID, "src_") {
			t.Fatal("native digest was not restart-readable", string(raw))
		}
		sourceID := *payload.Resumes[0].DigestSourceID
		extractionRaw, _ := c.call(t, "GET", "/api/extractions/"+sourceID, nil, 200)
		var extraction struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			ReviewExists bool   `json:"review_exists"`
		}
		if json.Unmarshal(extractionRaw, &extraction) != nil || extraction.ID != sourceID || extraction.Status != "extracted" || extraction.ReviewExists != (f.Mode != "digest") {
			t.Fatal("native extraction read changed", string(extractionRaw))
		}
		if f.Mode == "digest" {
			builtRaw, _ := c.call(t, "POST", "/api/reviews/build", map[string]any{"source_id": sourceID}, 200)
			var built struct {
				SourceID       string `json:"source_id"`
				ReviewRevision string `json:"review_revision"`
				Operation      struct {
					Status string `json:"status"`
				} `json:"operation"`
			}
			if json.Unmarshal(builtRaw, &built) != nil || built.SourceID != sourceID || !strings.HasPrefix(built.ReviewRevision, "sha256:") || built.Operation.Status == "" {
				t.Fatal("native review build changed", string(builtRaw))
			}
		}
		reviewRaw, _ := c.call(t, "GET", "/api/reviews/"+sourceID, nil, 200)
		var review struct {
			SourceID       string `json:"source_id"`
			ReviewRevision string `json:"review_revision"`
			Review         struct {
				Entities []any `json:"entities"`
			} `json:"review"`
		}
		if json.Unmarshal(reviewRaw, &review) != nil || review.SourceID != sourceID || !strings.HasPrefix(review.ReviewRevision, "sha256:") {
			t.Fatal("native review read changed", string(reviewRaw))
		}
		if f.Mode == "suggest_digest" {
			suggestedRaw, _ := c.call(t, "POST", "/api/reviews/"+sourceID+"/suggest", map[string]any{"expected_review_revision": review.ReviewRevision}, 200)
			var suggested struct {
				SourceID       string `json:"source_id"`
				ReviewRevision string `json:"review_revision"`
				Review         struct {
					Entities []any `json:"entities"`
				} `json:"review"`
				Suggestion struct {
					CardCount    int `json:"card_count"`
					NewCardCount int `json:"new_card_count"`
				} `json:"suggestion"`
			}
			if json.Unmarshal(suggestedRaw, &suggested) != nil || suggested.SourceID != sourceID || suggested.ReviewRevision == review.ReviewRevision ||
				len(suggested.Review.Entities) == 0 || suggested.Suggestion.CardCount == 0 || suggested.Suggestion.NewCardCount == 0 {
				t.Fatal("native review suggestion changed", string(suggestedRaw))
			}
		}
		if f.Mode == "verify_digest" && len(review.Review.Entities) == 0 {
			t.Fatal("suggested cards were not restart-readable", string(reviewRaw))
		}
		return
	}
	var restoreCatalog func()
	if f.Mode == "catalog_corrupt" {
		path := filepath.Join(f.Root, filepath.FromSlash(catalogstore.CustomGroupsPath))
		original, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		info, e := os.Stat(path)
		if e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte("private-catalog-marker: ["), 0600); e != nil {
			t.Fatal(e)
		}
		restoreCatalog = func() {
			if e := os.WriteFile(path, original, 0600); e != nil {
				t.Error(e)
			}
			if e := os.Chtimes(path, info.ModTime(), info.ModTime()); e != nil {
				t.Error(e)
			}
		}
		defer restoreCatalog()
	}
	wantFailure := map[string]struct {
		status         int
		code, category string
	}{
		"large_report":          {422, "RESUME_MANAGEMENT_UNSAFE", "unsafe"},
		"run_file":              {422, "RESUME_MANAGEMENT_UNSAFE", "unsafe"},
		"catalog_corrupt":       {503, "RESUME_MANAGEMENT_UNAVAILABLE", "storage"},
		"sql_schema_corrupt":    {503, "RESUME_MANAGEMENT_UNAVAILABLE", "storage"},
		"sql_value_unsupported": {503, "RESUME_MANAGEMENT_UNAVAILABLE", "storage"},
		"child_129":             {422, "RESUME_MANAGEMENT_UNSAFE", "unsafe"},
	}
	failure, isFailure := wantFailure[f.Mode]
	status := 200
	if isFailure {
		status = failure.status
	}
	raw, _ := c.call(t, "GET", "/api/resumes?include_archived=true", nil, status)
	if isFailure {
		var problem map[string]any
		if json.Unmarshal(raw, &problem) != nil || problem["resumes"] != nil || problem["diagnostic_category"] != failure.category || problem["error_code"] != failure.code || len(raw) > 4096 || bytes.Contains(raw, []byte(f.Root)) || bytes.Contains(raw, []byte("private-catalog-marker")) || bytes.Contains(raw, []byte("private_bad_column")) || bytes.Contains(raw, []byte("aaaaaaaaaaaaaaaa")) || bytes.Contains(raw, []byte("r1.txt")) || bytes.Contains(raw, []byte("invalid: [")) {
			t.Fatal("unsafe partial response", string(raw))
		}
		return
	}
	var payload struct {
		Resumes []struct {
			ID                string  `json:"id"`
			DigestStatus      string  `json:"digest_status"`
			DigestSupported   bool    `json:"digest_supported"`
			DigestSourceID    *string `json:"digest_source_id"`
			DigestProcessedAt *string `json:"digest_processed_at"`
			Archived          bool    `json:"archived"`
		} `json:"resumes"`
	}
	if e := json.Unmarshal(raw, &payload); e != nil || len(payload.Resumes) != 2 {
		t.Fatal("management rows", e, string(raw))
	}
	want := map[string]string{"missing": "not_digested", "matched": "in_progress", "approval": "digested", "stale": "outdated", "malformed_review": "error", "malformed_extraction": "not_digested"}[f.Mode]
	if payload.Resumes[0].DigestStatus != want || payload.Resumes[1].DigestStatus != want || !payload.Resumes[0].DigestSupported || payload.Resumes[1].DigestSupported || !payload.Resumes[1].Archived {
		t.Fatal("status/support/archive projection", string(raw))
	}
	if f.Mode != "missing" && f.Mode != "malformed_extraction" && (payload.Resumes[0].DigestSourceID == nil || *payload.Resumes[0].DigestSourceID != "src_fictional_1234567890" || payload.Resumes[0].DigestProcessedAt == nil || *payload.Resumes[0].DigestProcessedAt != "2023-11-14T22:13:20.123456+00:00") {
		t.Fatal("source identity or processing time", string(raw))
	}
	if bytes.Contains(raw, []byte("aaaaaaaaaaaaaaaa")) || bytes.Contains(raw, []byte("r1.txt")) || bytes.Contains(raw, []byte(f.Root)) {
		t.Fatal("private field disclosed")
	}
	filtered, _ := c.call(t, "GET", "/api/resumes?include_archived=true&search=Beta", nil, 200)
	if !bytes.Contains(filtered, []byte(`"id":"r2"`)) || bytes.Contains(filtered, []byte(`"id":"r1"`)) {
		t.Fatal("search failed", string(filtered))
	}
	// This listing-only fixture has noncanonical/missing Resume Version bytes.
	// Digest is now implemented (positive lifecycle coverage above), so the
	// source-integrity boundary must reject it, not pretend the route is absent.
	runNames := func() []string {
		t.Helper()
		entries, err := os.ReadDir(filepath.Join(f.Root, "career_vault", "intake_runs"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return names
	}
	beforeRuns := runNames()
	refused, _ := c.call(t, "POST", "/api/resumes/r1/digest", nil, 409)
	var refusal struct {
		Code  string `json:"error_code"`
		State struct {
			Committed, Partial bool
			Known              bool
		} `json:"operation_state"`
	}
	if err := json.Unmarshal(refused, &refusal); err != nil || refusal.Code != "RESUME_DIGEST_SOURCE_INVALID" || refusal.State.Committed || refusal.State.Partial || !refusal.State.Known {
		t.Fatal("invalid source was not rejected before publication", err, string(refused))
	}
	if !reflect.DeepEqual(beforeRuns, runNames()) {
		t.Fatal("refused digest created an intake run")
	}
	if _, err := os.Stat(filepath.Join(f.Root, datahome.IntakeRecoveryPath)); !os.IsNotExist(err) {
		t.Fatal("refused digest created an intent", err)
	}
}
