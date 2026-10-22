//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sourceupload"
)

func TestSourceUploadOwnedPublicationFacts(t *testing.T) {
	if home := os.Getenv("CAREERHUB_TEST_SOURCE_UPLOAD_HOME"); home != "" {
		runSourceUpload(t, home, os.Getenv("CAREERHUB_TEST_SOURCE_UPLOAD_CASE"))
		return
	}
	for _, scenario := range []string{"success", "invalid", "reimport-missing", "raw-effect", "raw-no-effect", "raw-true-error", "verification", "terminal-diagnostic", "initial-diagnostic", "cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			if e := os.MkdirAll(home, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(home, datahome.MarkerPath), []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`), 0600); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSourceUploadOwnedPublicationFacts$")
			cmd.Env = append(os.Environ(), "CAREERHUB_TEST_SOURCE_UPLOAD_HOME="+home, "CAREERHUB_TEST_SOURCE_UPLOAD_CASE="+scenario)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.WaitDelay = time.Second
			if raw, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("child failed: %v %.1800s", e, raw)
			}
		})
	}
}

func runSourceUpload(t *testing.T, home, scenario string) {
	t.Helper()
	root, e := runtimepaths.Open(home)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	info, problem := datahome.Inspect(home, nil)
	if problem != nil {
		t.Fatal(problem)
	}
	owner := New(home).PinnedToHome(*info)
	if out, e := owner.UploadIntakeSource(context.Background(), "fictional.txt", []byte("text"), false); e == nil || out.CommitAttempted {
		t.Fatal("unowned write accepted")
	}
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e)
	}
	content := []byte("\xef\xbb\xbfFictional resume\n")
	if scenario == "invalid" {
		content = []byte{0xff}
	}
	if strings.HasPrefix(scenario, "raw-") || scenario == "initial-diagnostic" {
		owner.intakeMutations.create = func(r *runtimepaths.Root, p string, b []byte) (bool, *runtimepaths.Error) {
			if strings.HasPrefix(p, "career_vault/raw/") || scenario == "initial-diagnostic" {
				if scenario != "raw-no-effect" {
					if changed, e := r.CreateFile(p, b); e != nil || !changed {
						return changed, e
					}
				}
				return scenario == "raw-true-error", &runtimepaths.Error{Code: "TEST_AMBIGUOUS"}
			}
			return r.CreateFile(p, b)
		}
	}
	if scenario == "verification" {
		owner.checkpoint = func(string) *Error { return failed("test", "TEST_VERIFY") }
	}
	if scenario == "terminal-diagnostic" {
		owner.intakeMutations.replace = func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error) {
			return false, &runtimepaths.Error{Code: "TEST_TERMINAL"}
		}
	}
	if scenario == "cleanup" {
		owner.closeRoot = func(r *runtimepaths.Root) error { r.Close(); return errors.New("test cleanup") }
	}
	out, err := owner.UploadIntakeSource(context.Background(), "fictional.txt", content, scenario == "reimport-missing")
	if out == nil {
		t.Fatal("missing facts")
	}
	exists := scenario != "invalid" && scenario != "reimport-missing" && scenario != "raw-no-effect" && scenario != "initial-diagnostic"
	raw, readErr := os.ReadFile(filepath.Join(home, "career_vault/raw/fictional.txt"))
	if exists && (readErr != nil || !bytes.Equal(raw, content)) || !exists && !os.IsNotExist(readErr) {
		t.Fatal("wrong disk state", scenario, readErr)
	}
	committed := exists && scenario != "raw-effect"
	if out.Committed != committed {
		t.Fatal("wrong commit fact", scenario, out.Committed)
	}
	if scenario == "success" {
		if err != nil || len(out.Result) == 0 {
			t.Fatal("success missing", err)
		}
	} else if err == nil || len(out.Result) != 0 {
		t.Fatal("failure advertised success", scenario)
	}
	if scenario == "raw-effect" || scenario == "raw-no-effect" {
		if !out.CommitAttempted || out.Committed || err.Code != "SOURCE_UPLOAD_WRITE_UNCERTAIN" {
			t.Fatal("ambiguity lost", out, err)
		}
	}
	if scenario == "cleanup" && !out.CleanupFailed {
		t.Fatal("cleanup lost")
	}
	files, e2 := os.ReadDir(filepath.Join(home, "career_vault/logs/native-intake"))
	if e2 != nil || len(files) != 1 {
		t.Fatal("missing diagnostic", e2)
	}
	diagnosticRaw, e2 := os.ReadFile(filepath.Join(home, "career_vault/logs/native-intake", files[0].Name()))
	if e2 != nil {
		t.Fatal(e2)
	}
	d, e2 := sourceupload.Decode(diagnosticRaw)
	if e2 != nil {
		t.Fatal(e2)
	}
	want := "failed"
	switch scenario {
	case "success", "cleanup":
		want = "success"
	case "raw-effect", "raw-no-effect":
		want = "unknown"
	case "initial-diagnostic", "terminal-diagnostic":
		want = "started"
	}
	if d.State != want {
		t.Fatal("wrong retained diagnostic", scenario, d.State)
	}
	if bytes.Contains(diagnosticRaw, []byte("fictional.txt")) || bytes.Contains(diagnosticRaw, content) {
		t.Fatal("private content entered diagnostic")
	}
}
