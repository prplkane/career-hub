//go:build windows

package readonly

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
)

type applicationFixtureRequest struct {
	Start    protocol.StartRecord
	Mode     string
	WantCode string
	WantRows int
}

// The writer is exclusively fixture setup, never a service dependency. Finish
// each native write and connection close before a read-side assertion begins.
func writeApplicationSQL(t *testing.T, root string, statements ...string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(applicationDatabasePath))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal("synthetic database setup failed")
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal("synthetic database setup did not finish")
	}
}

func populatedApplicationFixture(t *testing.T, root string) {
	t.Helper()
	writeApplicationSQL(t, root,
		"PRAGMA user_version=28",
		`CREATE TABLE job_applications(job_id TEXT PRIMARY KEY,company TEXT,title TEXT,source_url TEXT,content_fingerprint TEXT,decision TEXT,current_status TEXT,notes TEXT,created_at TEXT,updated_at TEXT,applied_resume_id TEXT,applied_resume_recorded INTEGER,revision INTEGER NOT NULL DEFAULT 1)`,
		`INSERT INTO job_applications VALUES('one','Société','Engineer 🚀','https://example.invalid/job','fp',NULL,'reviewed','','created','updated','old',2,3)`,
		`CREATE TABLE application_resume_attachments(job_id TEXT,resume_id TEXT,relative_path TEXT,file_name TEXT,label TEXT,sha256 TEXT,size_bytes INTEGER,attached_at TEXT)`,
		`INSERT INTO application_resume_attachments VALUES('one','r','resumes/example.pdf','example.pdf','Selected label','hash',120,'attached')`,
		`CREATE TABLE application_resume_plans(job_id TEXT,plan_json TEXT,status TEXT,revision INTEGER,created_at TEXT,updated_at TEXT)`,
		`INSERT INTO application_resume_plans VALUES('one','not parsed','ready',1.25,'created','updated')`,
		`CREATE TABLE application_applied_resume_events(event_id INTEGER,job_id TEXT,resume_id TEXT)`,
		`INSERT INTO application_applied_resume_events VALUES(2,'one','r'),(1,'one','old')`,
		`CREATE TABLE resume_library(resume_id TEXT,display_name TEXT)`,
		`INSERT INTO resume_library VALUES('r','Résumé <chosen>')`)
}

// Each test fixture owns a separate process because the real ownership claim
// is intentionally irreversible. There is no public test-only release/bypass.
func runApplicationFixture(t *testing.T, req applicationFixtureRequest) {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestApplicationSummariesProcessHelper$")
	cmd.Env = append(os.Environ(), "CAREERHUB_APPLICATION_READ_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.CombinedOutput()
	// Run waits for actual OS process exit before t.TempDir cleanup releases
	// the fixture; an expired observation does not imply child quiescence.
	if ctx.Err() != nil || err != nil || cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		t.Fatalf("native application service fixture %s failed: %s", req.Mode, out)
	}
}

func TestNativeApplicationSummarySchemasAndOwnership(t *testing.T) {
	for _, tc := range []struct {
		mode, code string
		rows       int
	}{
		{"missing_parent", "", 0},
		{"missing_database", "", 0},
		{"schema0", "APPLICATION_SCHEMA_PREPARATION_REQUIRED", 0},
		{"schema27", "APPLICATION_SCHEMA_PREPARATION_REQUIRED", 0},
		{"schema29", "APPLICATION_SCHEMA_TOO_NEW", 0},
		{"schema28", "APPLICATION_READ_FAILED", 0},
		{"corrupt", "APPLICATION_READ_FAILED", 0},
		{"current", "", 1},
		{"unowned", "DATA_HOME_OWNERSHIP_REQUIRED", 0},
		{"unowned_missing", "DATA_HOME_OWNERSHIP_REQUIRED", 0},
		{"main_directory", "APPLICATION_PATH_REJECTED", 0},
		{"main_hardlink", "APPLICATION_PATH_REJECTED", 0},
		{"aux_journal", "APPLICATION_PATH_REJECTED", 0},
		{"aux_wal", "APPLICATION_PATH_REJECTED", 0},
		{"aux_shm", "APPLICATION_PATH_REJECTED", 0},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			base, start := fixture(t)
			path := filepath.Join(start.DataRoot, filepath.FromSlash(applicationDatabasePath))
			switch {
			case tc.mode == "missing_parent", tc.mode == "unowned_missing":
			case tc.mode == "missing_database":
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
			case strings.HasPrefix(tc.mode, "schema"):
				writeApplicationSQL(t, start.DataRoot, "PRAGMA user_version="+strings.TrimPrefix(tc.mode, "schema"))
			case tc.mode == "corrupt":
				writeFixture(t, start.DataRoot, applicationDatabasePath, "PRIVATE synthetic corrupt database")
			case tc.mode == "main_directory":
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			default:
				populatedApplicationFixture(t, start.DataRoot)
			}
			if tc.mode == "main_hardlink" {
				if err := os.Link(path, filepath.Join(base, "synthetic-main-alias")); err != nil {
					t.Fatal("native hardlink fixture setup failed")
				}
			}
			if strings.HasPrefix(tc.mode, "aux_") {
				foreign := filepath.Join(base, "synthetic-auxiliary")
				if err := os.WriteFile(foreign, []byte("synthetic auxiliary"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(foreign, path+"-"+strings.TrimPrefix(tc.mode, "aux_")); err != nil {
					t.Fatal("native companion hardlink fixture setup failed")
				}
			}
			before := tree(t, base)
			runApplicationFixture(t, applicationFixtureRequest{start, tc.mode, tc.code, tc.rows})
			if !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("read changed synthetic file membership/content/mtime/mode")
			}
		})
	}
}

func TestNativeApplicationSummaryFreshReadsAndAdmission(t *testing.T) {
	for _, tc := range []struct{ mode, code string }{
		{"fresh", ""},
		{"identity", "DATA_HOME_IDENTITY_CHANGED"},
		{"recovery", "DATA_HOME_RECOVERY_REQUIRED"},
		{"resource", "SIDECAR_RESOURCE_INVALID"},
		{"canceled", "APPLICATION_READ_CANCELED"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			_, start := fixture(t)
			populatedApplicationFixture(t, start.DataRoot)
			// The child explicitly changes only this synthetic fixture after
			// startup, and snapshots immediately before/after each service read.
			runApplicationFixture(t, applicationFixtureRequest{start, tc.mode, tc.code, 1})
		})
	}
}

func decodedApplicationSummaries(t *testing.T, payload map[string]applications.Summary) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestApplicationSummariesProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_APPLICATION_READ_HELPER") != "1" {
		return
	}
	var req applicationFixtureRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 16<<10)).Decode(&req) != nil {
		t.Fatal("invalid synthetic helper request")
	}
	var w *Workspace
	var err error
	if strings.HasPrefix(req.Mode, "unowned") {
		w, err = Open(req.Start)
	} else {
		w, err = OpenForProcess(req.Start)
	}
	if err != nil {
		t.Fatal("synthetic initialized workspace admission failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	switch req.Mode {
	case "identity":
		writeFixture(t, req.Start.DataRoot, datahome.MarkerPath, marker("dev", "dh_22222222222222222222222222222222"))
	case "recovery":
		writeFixture(t, req.Start.DataRoot, datahome.DocumentRecoveryPath, "synthetic recovery")
	case "resource":
		writeFixture(t, req.Start.ResourceRoot, "VERSION", "invalid")
	case "canceled":
		cancel()
	}
	base := filepath.Dir(req.Start.DataRoot)
	read := func() (map[string]applications.Summary, error) {
		before := tree(t, base)
		result, failure := w.ApplicationSummaries(ctx, []string{"missing", "one", "one", " one ", "ONE", "nul\x00id", "'; DROP TABLE job_applications;--"})
		if !reflect.DeepEqual(before, tree(t, base)) {
			t.Fatal("service read changed synthetic files")
		}
		if failure != nil && errors.Unwrap(failure) != nil {
			t.Fatal("service exposed a native query cause")
		}
		return result, failure
	}
	payload, err := read()
	if req.WantCode != "" {
		requireCode(t, err, req.WantCode)
		if payload != nil {
			t.Fatal("failed read returned partial data")
		}
	} else {
		if err != nil || payload == nil || len(payload) != req.WantRows {
			t.Fatalf("unexpected summary rows/error: rows=%d error=%v", len(payload), err)
		}
		if req.WantRows == 1 {
			one := decodedApplicationSummaries(t, payload)["one"].(map[string]any)
			if len(one) != 14 || one["company"] != "Société" || one["title"] != "Engineer 🚀" || one["current_status"] != "reviewed" {
				t.Fatal("selected scalar fields changed")
			}
			if !reflect.DeepEqual(one["applied_resume"], map[string]any{"recorded": true, "resume": map[string]any{"id": "r", "display_name": "Résumé <chosen>"}}) {
				t.Fatal("submitted-resume event identity changed")
			}
			if one["resume_selection"].(map[string]any)["resume"].(map[string]any)["display_name"] != "Selected label" || one["resume_plan"].(map[string]any)["revision"] != 1.25 {
				t.Fatal("selected resume label or REAL plan revision changed")
			}
		}
	}
	if req.Mode == "fresh" {
		first := decodedApplicationSummaries(t, payload)
		delete(payload, "one")
		again, err := read()
		if err != nil || !reflect.DeepEqual(first, decodedApplicationSummaries(t, again)) {
			t.Fatal("caller mutation affected a later result")
		}
		writeApplicationSQL(t, req.Start.DataRoot, "UPDATE job_applications SET title='Second title',current_status='applied' WHERE job_id='one'")
		updated, err := read()
		if err != nil {
			t.Fatal(err)
		}
		one := decodedApplicationSummaries(t, updated)["one"].(map[string]any)
		if one["title"] != "Second title" || one["current_status"] != "applied" {
			t.Fatal("application service returned stale cached fields")
		}
	}
	// Verify file/parent handles really finished BEFORE the process exits, even
	// after a schema/query/path failure. The Data Home ownership itself stays.
	for _, path := range []string{
		filepath.Join(req.Start.DataRoot, filepath.FromSlash(applicationDatabasePath)),
		filepath.Join(req.Start.DataRoot, "applications"),
	} {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		}
		moved := path + ".synthetic-cleanup-check"
		if err := os.Rename(path, moved); err != nil {
			t.Fatal("service left a native database/parent handle live after returning")
		}
		if err := os.Rename(moved, path); err != nil {
			t.Fatal("synthetic cleanup check could not restore its path")
		}
	}
	// All assertions complete through normal testing return. Actual child exit
	// releases the irreversible claim; parent Run then permits fixture cleanup.
}
