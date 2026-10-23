//go:build windows

package readonly

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/intake/intakeextract"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestNativeAgentDigestObservation(t *testing.T) {
	for _, mode := range []string{"unused", "missing", "matched", "approval", "stale", "unselected", "leaf_drift", "membership_drift", "missing_drift", "catalog_drift", "canceled", "invalid_selection", "closed_transaction"} {
		t.Run(mode, func(t *testing.T) {
			base, start := fixture(t)
			managementDatabase(t, start)
			snapshot, e := catalogstore.Load(start.ResourceRoot, start.DataRoot)
			if e != nil {
				t.Fatal(e)
			}
			if mode != "missing" && mode != "missing_drift" {
				report := managementReport(t, "migration_intake_extraction_cases.json")
				report["registry_revision"], report["extractor_version"] = snapshot.CatalogRevision(), intakeextract.ExtractorVersion
				if mode == "stale" {
					report["registry_revision"] = strings.Repeat("b", 64)
				}
				if mode == "unselected" {
					report["source"].(map[string]any)["sha256"] = strings.Repeat("c", 64)
				}
				writeManagementReport(t, start, "src_fictional_1234567890", "extracted.yaml", report)
				if mode == "approval" {
					writeManagementReport(t, start, "src_fictional_1234567890", "approval_result.yaml", managementReport(t, "migration_intake_approval_cases.json"))
				}
				if mode == "unselected" || mode == "unused" {
					// Reading an unrelated workflow would fail. The lazy selector
					// must neither digest it nor retain its private response.
					writeFixture(t, start.DataRoot, resumeRunsPath+"/src_fictional_1234567890/review.yaml", strings.Repeat("x", (2<<20)+1))
				}
			}
			before := tree(t, base)
			raw, _ := json.Marshal(struct {
				Start protocol.StartRecord
				Mode  string
			}{start, mode})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAgentDigestProcessHelper$")
			cmd.Env = append(os.Environ(), "CAREERHUB_AGENT_DIGEST_HELPER=1")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.Stdin = bytes.NewReader(raw)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("native %s: %s", mode, out)
			}
			if !strings.HasSuffix(mode, "_drift") && !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("digest changed fixture")
			}
		})
	}
}

func TestAgentDigestProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_AGENT_DIGEST_HELPER") != "1" {
		return
	}
	var req struct {
		Start protocol.StartRecord
		Mode  string
	}
	if json.NewDecoder(io.LimitReader(os.Stdin, 16<<10)).Decode(&req) != nil {
		t.Fatal("invalid request")
	}
	w, err := OpenForProcess(req.Start)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (bool, error) {
		db, e := sql.Open("sqlite", filepath.Join(req.Start.DataRoot, filepath.FromSlash(applicationDatabasePath)))
		if e != nil {
			t.Fatal(e)
		}
		defer db.Close()
		tx, e := db.BeginTx(context.Background(), nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if _, e := tx.Exec("PRAGMA query_only=ON"); e != nil {
			t.Fatal(e)
		}
		o := &agentDigestObservation{dataRoot: req.Start.DataRoot, data: data, resources: resources, tx: tx}
		defer o.Close()
		if req.Mode == "unused" {
			if o.Check(ctx) != nil || o.runs != nil || o.catalogData != nil {
				t.Fatal("unused observer accessed data")
			}
			return true, nil
		}
		selection := json.RawMessage(`[{"id":"r1","display_name":"Alpha"}]`)
		if req.Mode == "invalid_selection" {
			selection = json.RawMessage(`[{"id":"r1","display_name":"Alpha","path":"PRIVATE"}]`)
		}
		if req.Mode == "closed_transaction" {
			if tx.Rollback() != nil {
				t.Fatal("rollback")
			}
		}
		if req.Mode == "canceled" {
			cancel()
		}
		raw, warning, de := o.Read(ctx, selection)
		if req.Mode == "invalid_selection" || req.Mode == "closed_transaction" || req.Mode == "canceled" {
			if de == nil || raw != nil || warning != nil {
				t.Fatal("failure returned partial success")
			}
			return true, nil
		}
		if de != nil || warning != nil {
			t.Fatal("digest failed", de)
		}
		var rows []map[string]any
		if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 || len(rows[0]) != 6 || rows[0]["id"] != "r1" || rows[0]["digest_supported"] != true {
			t.Fatal("selected metadata shape")
		}
		want := "in_progress"
		switch req.Mode {
		case "missing", "missing_drift", "unselected":
			want = "not_digested"
		case "approval":
			want = "digested"
		case "stale":
			want = "outdated"
		}
		if rows[0]["digest_status"] != want || bytes.Contains(raw, []byte(managementSHA)) || bytes.Contains(raw, []byte("r2")) {
			t.Fatal("wrong status/private identity leaked")
		}
		if o.runs != nil {
			if o.runs.Check("src_fictional_1234567890/extracted.yaml") != nil {
				t.Fatal("run closed before owner verification")
			}
		}
		switch req.Mode {
		case "leaf_drift":
			writeFixture(t, req.Start.DataRoot, resumeRunsPath+"/src_fictional_1234567890/extracted.yaml", "invalid: [")
		case "membership_drift":
			if os.Mkdir(filepath.Join(req.Start.DataRoot, filepath.FromSlash(resumeRunsPath), "src_extra"), 0700) != nil {
				t.Fatal("mkdir")
			}
		case "missing_drift":
			if os.MkdirAll(filepath.Join(req.Start.DataRoot, filepath.FromSlash(resumeRunsPath)), 0700) != nil {
				t.Fatal("mkdir")
			}
		case "catalog_drift":
			writeFixture(t, req.Start.DataRoot, catalogstore.CustomGroupsPath, "[]\n")
		}
		check := o.Check(ctx)
		if strings.HasSuffix(req.Mode, "_drift") {
			if check == nil {
				t.Fatal("late drift accepted")
			}
		} else if check != nil {
			t.Fatal(check)
		}
		if repeat, _, e := o.Read(ctx, selection); repeat != nil || e == nil {
			t.Fatal("observer reused")
		}
		if o.Close() != nil || o.Close() != nil || o.Check(ctx) == nil {
			t.Fatal("closed observer lifetime")
		}
		if req.Mode != "closed_transaction" {
			var n int
			if tx.QueryRow("SELECT count(*) FROM resume_library").Scan(&n) != nil || n != 2 {
				t.Fatal("digest ended shared SQL snapshot")
			}
		}
		return true, nil
	})
	if req.Mode != "canceled" && err != nil {
		t.Fatal(err)
	}
}
