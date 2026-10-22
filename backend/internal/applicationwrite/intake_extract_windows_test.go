//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/intakeextract"
	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/runtimepaths"
)

func TestSourceExtractionOwnedFaults(t *testing.T) {
	if home := os.Getenv("CAREERHUB_TEST_EXTRACT_HOME"); home != "" {
		runSourceExtraction(t, home, os.Getenv("CAREERHUB_TEST_EXTRACT_CASE"))
		return
	}
	for _, scenario := range []string{"success", "raw-drift", "catalog-drift", "approval-drift", "report-effect", "report-no-effect", "replace-effect"} {
		t.Run(scenario, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			for _, dir := range []string{"career_vault/raw", "resources/src/data"} {
				if e := os.MkdirAll(filepath.Join(home, dir), 0700); e != nil {
					t.Fatal(e)
				}
			}
			for _, name := range []string{catalogstore.CoreGroupsPath, catalogstore.CoreSkillsPath} {
				b, e := os.ReadFile(filepath.Join("../../..", name))
				if e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(filepath.Join(home, "resources", name), b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			for name, content := range map[string][]byte{datahome.MarkerPath: []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`), "career_vault/raw/Straße.txt": []byte("Experience\n- Built Python APIs with SQL.\n2020 - 2024\n")} {
				if e := os.WriteFile(filepath.Join(home, name), content, 0600); e != nil {
					t.Fatal(e)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSourceExtractionOwnedFaults$")
			cmd.Env = append(os.Environ(), "CAREERHUB_TEST_EXTRACT_HOME="+home, "CAREERHUB_TEST_EXTRACT_CASE="+scenario)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.WaitDelay = time.Second
			if raw, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("native child: %v %.1700s", e, raw)
			}
		})
	}
}

func runSourceExtraction(t *testing.T, home, scenario string) {
	root, e := runtimepaths.Open(home)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e)
	}
	info, e2 := datahome.Inspect(home, nil)
	if e2 != nil {
		t.Fatal(e2)
	}
	owner := New(home).PinnedToHome(*info).PinnedToResources(filepath.Join(home, "resources"))
	content, _ := os.ReadFile(filepath.Join(home, "career_vault/raw/Straße.txt"))
	id := "src_strasse_" + digestBytes(content)[:10]
	if scenario == "success" {
		resources, e := runtimepaths.Open(filepath.Join(home, "resources"))
		if e != nil {
			t.Fatal(e)
		}
		defer resources.Close()
		catalog, problem := readIntakeCatalog(context.Background(), resources, root)
		if problem != nil {
			t.Fatal(problem)
		}
		report, e2 := intakeextract.Extract(context.Background(), content, intakeextract.Source{ID: id, Path: "career_vault/raw/Straße.txt", FileType: "txt", SHA256: digestBytes(content)}, catalog.revision, catalog.terms)
		if e2 != nil {
			t.Fatal(e2)
		}
		serialized, _ := json.Marshal(report)
		if _, e := intakeread.ParseExtraction(serialized); e != nil {
			t.Fatalf("synthetic report roundtrip %s: %.4500s", e.Code, serialized)
		}
	}
	reportPath, _, approvalPath := intakeReviewPaths(id)
	if scenario == "replace-effect" {
		out, e := owner.ExtractIntakeSource(context.Background(), "Straße.txt", false)
		if e != nil {
			t.Fatalf("initial extraction: %s/%s", e.Stage, e.Code)
		}
		if !out.Committed {
			t.Fatal("initial report missing")
		}
		owner.intakeMutations.replace = func(r *runtimepaths.Root, p string, a, b []byte) (bool, *runtimepaths.Error) {
			changed, e := r.ReplaceFile(p, a, b)
			if p == reportPath && e == nil && changed {
				return false, &runtimepaths.Error{Code: "TEST_AMBIGUOUS"}
			}
			return changed, e
		}
	}
	if strings.HasPrefix(scenario, "report-") {
		owner.intakeMutations.create = func(r *runtimepaths.Root, p string, b []byte) (bool, *runtimepaths.Error) {
			if p == reportPath {
				if scenario == "report-effect" {
					if changed, e := r.CreateFile(p, b); e != nil || !changed {
						return changed, e
					}
				}
				return false, &runtimepaths.Error{Code: "TEST_AMBIGUOUS"}
			}
			return r.CreateFile(p, b)
		}
	}
	owner.checkpoint = func(stage string) *Error {
		if stage != "source_extraction_prepared" {
			return nil
		}
		switch scenario {
		case "raw-drift":
			if e := os.WriteFile(filepath.Join(home, "career_vault/raw/Straße.txt"), []byte("changed"), 0600); e != nil {
				t.Fatal(e)
			}
		case "catalog-drift":
			p := filepath.Join(home, "resources", catalogstore.CoreSkillsPath)
			b, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(p, append(b, '\n'), 0600); e != nil {
				t.Fatal(e)
			}
		case "approval-drift":
			p := filepath.Join(home, approvalPath)
			if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(p, []byte("approved"), 0600); e != nil {
				t.Fatal(e)
			}
		}
		return nil
	}
	out, err := owner.ExtractIntakeSource(context.Background(), "Straße.txt", scenario == "replace-effect")
	if scenario == "success" {
		if err != nil {
			t.Fatalf("extraction: %s/%s", err.Stage, err.Code)
		}
		if !out.Committed || len(out.Result) == 0 {
			t.Fatal("success missing")
		}
	} else {
		if err == nil || len(out.Result) != 0 {
			t.Fatal("fault masked")
		}
		if strings.HasSuffix(scenario, "drift") {
			if err.Code != "SOURCE_EXTRACTION_CHANGED" || out.CommitAttempted {
				t.Fatalf("drift: %s/%s", err.Stage, err.Code)
			}
		} else if err.Code != "SOURCE_EXTRACTION_WRITE_UNCERTAIN" || !out.CommitAttempted || out.Committed {
			t.Fatalf("ambiguity: %s/%s", err.Stage, err.Code)
		}
	}
	if scenario != "raw-drift" {
		after, _ := os.ReadFile(filepath.Join(home, "career_vault/raw/Straße.txt"))
		if !bytes.Equal(after, content) {
			t.Fatal("raw overwritten")
		}
	}
}
