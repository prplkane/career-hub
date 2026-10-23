//go:build windows

package readonly

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/intake/intakeextract"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const managementSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type managementRequest struct {
	Start    protocol.StartRecord
	Mode     string
	Revision string
}

func managementReport(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Base map[string]any `json:"base"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture.Base
}

func writeManagementReport(t *testing.T, start protocol.StartRecord, id, leaf string, report map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(report) // JSON is valid YAML.
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(start.DataRoot, "career_vault", "intake_runs", id, leaf)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func managementDatabase(t *testing.T, start protocol.StartRecord) {
	t.Helper()
	writeApplicationSQL(t, start.DataRoot,
		"PRAGMA user_version=28",
		`CREATE TABLE resume_library(resume_id TEXT PRIMARY KEY,display_name TEXT,relative_path TEXT,file_name TEXT,sha256 TEXT,size_bytes INTEGER,added_at TEXT,archived_at TEXT)`,
		`CREATE TABLE application_resume_attachments(resume_id TEXT)`,
		`CREATE TABLE job_applications(applied_resume_id TEXT)`,
		`CREATE TABLE application_applied_resume_events(resume_id TEXT,action TEXT)`,
		`INSERT INTO resume_library VALUES('r1','Alpha','applications/resumes/r1.txt','r1.txt','`+managementSHA+`',12,'2026-01-01',NULL)`,
		`INSERT INTO resume_library VALUES('r2','Beta','applications/resumes/r2.PDF','r2.PDF','`+managementSHA+`',12,'2026-01-02','2026-02-01')`)
}

func TestNativeResumeManagementOwner(t *testing.T) {
	for _, mode := range []string{"missing", "matched", "newer", "tie", "approval", "mismatch", "malformed_review", "malformed_extraction", "stale", "legacy_extractor", "stale_extractor", "large_report", "canceled", "sql_drift", "catalog_drift", "leaf_drift", "membership_drift", "close_failure", "missing_root_drift", "run_file", "run_junction", "leaf_directory", "child_128", "child_129", "run_01000", "run_02500", "run_05000", "run_10000", "run_10001", "response_limit", "aggregate_limit"} {
		t.Run(mode, func(t *testing.T) {
			base, start := fixture(t)
			managementDatabase(t, start)
			snapshot, catalogErr := catalogstore.Load(start.ResourceRoot, start.DataRoot)
			if catalogErr != nil {
				t.Fatal(catalogErr)
			}
			revision := snapshot.CatalogRevision()
			if mode != "missing" && mode != "missing_root_drift" && mode != "run_file" && mode != "response_limit" && mode != "aggregate_limit" {
				report := managementReport(t, "migration_intake_extraction_cases.json")
				report["registry_revision"] = revision
				report["extractor_version"] = intakeextract.ExtractorVersion
				if mode == "stale" {
					report["registry_revision"] = strings.Repeat("b", 64)
				}
				if mode == "stale_extractor" {
					report["extractor_version"] = "vault-intake-old"
				}
				if mode == "legacy_extractor" {
					report["extractor_version"] = "vault-intake-0.2"
				}
				id := "src_fictional_1234567890"
				path := writeManagementReport(t, start, id, "extracted.yaml", report)
				if err := os.Chtimes(path, time.Unix(1700000000, 123456000), time.Unix(1700000000, 123456000)); err != nil {
					t.Fatal(err)
				}
				if mode == "newer" || mode == "tie" {
					newID := "src_newer_fictional_1234567890"
					modified := time.Unix(1700000001, 0)
					if mode == "tie" {
						newID = "src_aaa_fictional_1234567890"
						modified = time.Unix(1700000000, 123456000)
					}
					second := managementReport(t, "migration_intake_extraction_cases.json")
					second["registry_revision"], second["extractor_version"] = revision, intakeextract.ExtractorVersion
					second["source"].(map[string]any)["id"] = newID
					newPath := writeManagementReport(t, start, newID, "extracted.yaml", second)
					if err := os.Chtimes(newPath, modified, modified); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "approval" || mode == "mismatch" {
					approval := managementReport(t, "migration_intake_approval_cases.json")
					if mode == "mismatch" {
						approval["source_id"] = "src_other_fictional_1234567890"
					}
					writeManagementReport(t, start, id, "approval_result.yaml", approval)
				}
				if mode == "malformed_review" {
					writeFixture(t, start.DataRoot, "career_vault/intake_runs/"+id+"/review.yaml", "invalid: [")
				}
				if mode == "malformed_extraction" {
					writeFixture(t, start.DataRoot, "career_vault/intake_runs/"+id+"/extracted.yaml", "invalid: [")
				}
				if mode == "large_report" {
					writeFixture(t, start.DataRoot, "career_vault/intake_runs/"+id+"/review.yaml", strings.Repeat("x", (2<<20)+1))
				}
				if mode == "leaf_directory" {
					if err := os.Mkdir(filepath.Join(start.DataRoot, "career_vault", "intake_runs", id, "review.yaml"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "child_128" || mode == "child_129" {
					count := 127
					if mode == "child_129" {
						count = 128
					}
					for i := 0; i < count; i++ {
						writeFixture(t, start.DataRoot, fmt.Sprintf("career_vault/intake_runs/%s/extra_%03d", id, i), "")
					}
				}
			}
			if mode == "run_file" {
				writeFixture(t, start.DataRoot, "career_vault/intake_runs/not_a_directory", "fictional")
			}
			if mode == "response_limit" {
				writeApplicationSQL(t, start.DataRoot, `WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<5000)
INSERT INTO resume_library(resume_id,display_name,relative_path,file_name,sha256,size_bytes,added_at)
SELECT 'bulk-'||n,printf('%0900d',n),'applications/resumes/bulk-'||n||'.txt','bulk.txt','`+managementSHA+`',12,'2026-01-01' FROM seq`)
			}
			if mode == "aggregate_limit" {
				for i := 0; i < 17; i++ {
					content := "x"
					if i < 16 {
						content = strings.Repeat(" ", 2<<20)
					}
					writeFixture(t, start.DataRoot, fmt.Sprintf("career_vault/intake_runs/run_%03d/extracted.yaml", i), content)
				}
			}
			if mode == "run_junction" {
				target := filepath.Join(base, "synthetic-junction-target")
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(start.DataRoot, "career_vault", "intake_runs", "run_junction")
				cmd := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("disposable junction setup failed: %s", out)
				}
			}
			if count := managementRunCount(mode); count != 0 {
				for i := 1; i < count; i++ {
					if err := os.Mkdir(filepath.Join(start.DataRoot, "career_vault", "intake_runs", fmt.Sprintf("run_%05d", i)), 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			var before map[string]string
			if managementRunCount(mode) == 0 && mode != "aggregate_limit" {
				before = tree(t, base)
			}
			req, _ := json.Marshal(managementRequest{start, mode, revision})
			parentLimit := 20 * time.Second
			if managementRunCount(mode) != 0 {
				parentLimit = 45 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), parentLimit)
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResumeManagementProcessHelper$")
			cmd.Env = append(os.Environ(), "CAREERHUB_MANAGEMENT_HELPER=1")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.Stdin = bytes.NewReader(req)
			started := time.Now()
			out, err := cmd.CombinedOutput()
			if managementRunCount(mode) != 0 {
				t.Logf("owner child duration: %s", time.Since(started))
			}
			cancel()
			if err != nil {
				t.Fatalf("native mode %s: %s", mode, out)
			}
			if before != nil && !strings.HasSuffix(mode, "_drift") && mode != "close_failure" && !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("read changed disposable fixture")
			}
		})
	}
}

func managementRunCount(mode string) int {
	switch mode {
	case "run_01000":
		return 1000
	case "run_02500":
		return 2500
	case "run_05000":
		return 5000
	case "run_10000":
		return 10000
	case "run_10001":
		return 10001
	default:
		return 0
	}
}

func TestResumeManagementProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_MANAGEMENT_HELPER") != "1" {
		return
	}
	var req managementRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 16<<10)).Decode(&req) != nil {
		t.Fatal("invalid helper request")
	}
	w, err := OpenForProcess(req.Start)
	if err != nil {
		t.Fatal(err)
	}
	childLimit := 10 * time.Second
	if managementRunCount(req.Mode) != 0 {
		childLimit = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), childLimit)
	defer cancel()
	zero, zeroErr := w.ResumeManagement(ctx, applications.ResumeFilters{IncludeArchived: true, Search: "No fictional matching resume"})
	if zeroErr != nil || len(zero) != 0 {
		t.Fatal("empty filtered request must skip run index and return no rows")
	}
	if req.Mode == "canceled" {
		cancel()
	}
	var rows []ManagedResumeEntry
	if strings.HasSuffix(req.Mode, "_drift") || req.Mode == "close_failure" {
		faults := &managementFaults{}
		switch req.Mode {
		case "sql_drift":
			faults.afterFirst = func() {
				writeApplicationSQL(t, req.Start.DataRoot, "UPDATE resume_library SET display_name='Changed' WHERE resume_id='r1'")
			}
		case "catalog_drift":
			faults.afterCatalog = func() { writeFixture(t, req.Start.DataRoot, catalogstore.CustomGroupsPath, "[]\n") }
		case "leaf_drift":
			faults.afterSecond = func() {
				writeFixture(t, req.Start.DataRoot, "career_vault/intake_runs/src_fictional_1234567890/extracted.yaml", "invalid: [")
			}
		case "membership_drift":
			faults.afterSecond = func() {
				if e := os.Mkdir(filepath.Join(req.Start.DataRoot, "career_vault", "intake_runs", "src_new_fictional_1234567890"), 0700); e != nil {
					t.Fatal(e)
				}
			}
		case "close_failure":
			faults.closeRun = func() error { return errors.New("synthetic close failure") }
		case "missing_root_drift":
			faults.afterSecond = func() {
				if e := os.MkdirAll(filepath.Join(req.Start.DataRoot, "career_vault", "intake_runs"), 0700); e != nil {
					t.Fatal(e)
				}
			}
		}
		rows, err = readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) ([]ManagedResumeEntry, error) {
			return readResumeManagementWithFaults(ctx, req.Start.DataRoot, data, resources, applications.ResumeFilters{IncludeArchived: true}, faults)
		})
	} else {
		rows, err = w.ResumeManagement(ctx, applications.ResumeFilters{IncludeArchived: true})
	}
	if req.Mode == "large_report" || req.Mode == "canceled" || req.Mode == "run_file" || req.Mode == "run_junction" || req.Mode == "leaf_directory" || req.Mode == "child_129" || req.Mode == "run_10001" || req.Mode == "response_limit" || req.Mode == "aggregate_limit" || strings.HasSuffix(req.Mode, "_drift") || req.Mode == "close_failure" {
		if rows != nil || err == nil {
			t.Fatal("failed read returned a partial success")
		}
		wantCode := map[string]string{
			"large_report":       "RESUME_MANAGEMENT_REPORT_REJECTED",
			"canceled":           "ASSESSMENT_INPUT_CANCELED",
			"sql_drift":          "RESUME_MANAGEMENT_DATABASE_CHANGED",
			"catalog_drift":      "RESUME_MANAGEMENT_CATALOG_CHANGED",
			"leaf_drift":         "RESUME_MANAGEMENT_RUNS_CHANGED",
			"membership_drift":   "RESUME_MANAGEMENT_RUNS_CHANGED",
			"close_failure":      "RESUME_MANAGEMENT_CLEANUP_FAILED",
			"missing_root_drift": "RESUME_MANAGEMENT_RUNS_CHANGED",
			"run_file":           "RESUME_MANAGEMENT_RUNS_REJECTED",
			"run_junction":       "RESUME_MANAGEMENT_RUNS_REJECTED",
			"leaf_directory":     "RESUME_MANAGEMENT_REPORT_REJECTED",
			"child_129":          "RESUME_MANAGEMENT_REPORT_REJECTED",
			"run_10001":          "RESUME_MANAGEMENT_RUNS_REJECTED",
			"response_limit":     "RESUME_MANAGEMENT_RESULT_LIMIT",
			"aggregate_limit":    "RESUME_MANAGEMENT_REPORT_LIMIT",
		}[req.Mode]
		requireCode(t, err, wantCode)
		return
	}
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	want := map[string]string{"missing": "not_digested", "matched": "in_progress", "newer": "in_progress", "tie": "in_progress", "approval": "digested", "mismatch": "error", "malformed_review": "error", "malformed_extraction": "not_digested", "stale": "outdated", "legacy_extractor": "outdated", "stale_extractor": "outdated", "child_128": "in_progress", "run_01000": "in_progress", "run_02500": "in_progress", "run_05000": "in_progress", "run_10000": "in_progress"}[req.Mode]
	if rows[0].DigestStatus != want || rows[1].DigestStatus != want {
		t.Fatalf("status %s/%s want %s", rows[0].DigestStatus, rows[1].DigestStatus, want)
	}
	if !rows[0].DigestSupported || rows[1].DigestSupported || rows[0].ResumeManagement == nil || !rows[1].ResumeManagement.Archived {
		t.Fatal("management metadata differs")
	}
	if req.Mode != "missing" && req.Mode != "malformed_extraction" && req.Mode != "newer" && req.Mode != "tie" && (rows[0].DigestSourceID == nil || *rows[0].DigestSourceID != "src_fictional_1234567890" || rows[0].DigestProcessedAt == nil || *rows[0].DigestProcessedAt != "2023-11-14T22:13:20.123456+00:00") {
		t.Fatal("run identity/observed mtime differs")
	}
	if req.Mode == "tie" && (rows[0].DigestSourceID == nil || *rows[0].DigestSourceID != "src_aaa_fictional_1234567890" || rows[0].DigestProcessedAt == nil || *rows[0].DigestProcessedAt != "2023-11-14T22:13:20.123456+00:00") {
		t.Fatal("equal-mtime run did not use the documented lexical tie break")
	}
	if req.Mode == "newer" && (rows[0].DigestSourceID == nil || *rows[0].DigestSourceID != "src_newer_fictional_1234567890" || rows[0].DigestProcessedAt == nil || *rows[0].DigestProcessedAt != "2023-11-14T22:13:21+00:00") {
		t.Fatal("newest run was not selected")
	}
	if managementRunCount(req.Mode) != 0 {
		return
	} // One full bounded owner request; no second scan on its context.
	filtered, err := w.ResumeManagement(ctx, applications.ResumeFilters{IncludeArchived: true, Search: "Beta"})
	if err != nil || len(filtered) != 1 || !filtered[0].ResumeManagement.Archived {
		t.Fatal("search/archive order differs")
	}
}
