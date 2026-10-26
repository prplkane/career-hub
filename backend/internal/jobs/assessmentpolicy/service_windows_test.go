//go:build windows

package assessmentpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestPolicyNativeOwnedLifecycle(t *testing.T) {
	path := os.Getenv("CAREERHUB_POLICY_TEST_ROOT")
	if path == "" {
		path = filepath.Join(t.TempDir(), "home")
		if e := os.MkdirAll(filepath.Join(path, "career_vault"), 0700); e != nil {
			t.Fatal(e)
		}
		marker := `{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`
		if e := os.WriteFile(filepath.Join(path, datahome.MarkerPath), []byte(marker), 0600); e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPolicyNativeOwnedLifecycle$", "-test.count=1")
		cmd.WaitDelay = time.Second
		cmd.Env = append(os.Environ(), "CAREERHUB_POLICY_TEST_ROOT="+path)
		if raw, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("native policy child: %v %.1600s", e, raw)
		}
		return
	}
	ctx := context.Background()
	s := New(path)
	if out, e := s.Run(ctx, false, nil, ""); e == nil || out.Committed {
		t.Fatal("unowned access")
	}
	root, err := runtimepaths.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	full := filepath.Join(path, filepath.FromSlash(Path))
	out, e := s.Run(ctx, false, nil, "")
	if e != nil || out.Committed {
		t.Fatal("default read failed", e)
	}
	if _, e := os.Stat(full); !os.IsNotExist(e) {
		t.Fatal("read created policy")
	}
	var before map[string]any
	if json.Unmarshal(out.Result, &before) != nil || before["configured"] != false {
		t.Fatal("default response")
	}
	flags := map[string]any{classes[0]: true, classes[1]: false, classes[2]: true}
	out, e = s.Run(ctx, true, flags, before["revision"].(string))
	if e != nil || !out.Committed || out.CleanupFailed {
		t.Fatal("native create failed", e)
	}
	saved, _ := os.ReadFile(full)
	if out, e := s.Run(ctx, true, nil, "stale"); e == nil || e.Code != "HARD_BLOCKER_POLICY_REVISION_CONFLICT" || out.Committed {
		t.Fatal("revision before validation")
	}
	if out, e := s.Run(ctx, true, nil, revision(saved)); e == nil || e.Code != "HARD_BLOCKER_POLICY_INVALID" || out.Committed {
		t.Fatal("invalid save")
	}
	if raw, _ := os.ReadFile(full); string(raw) != string(saved) {
		t.Fatal("failed request wrote policy")
	}
	flags[classes[1]] = true
	out, e = s.Run(ctx, true, flags, revision(saved))
	if e != nil || !out.Committed {
		t.Fatal("native replace failed", e)
	}
	saved, _ = os.ReadFile(full)
	// Published bytes survive a later reported native failure; never imply rollback.
	s.replace = func(r *runtimepaths.Root, name string, old, raw []byte) (bool, *runtimepaths.Error) {
		done, e := r.ReplaceFile(name, old, raw)
		if e != nil {
			return done, e
		}
		return done, &runtimepaths.Error{Code: "FILE_CLOSE"}
	}
	flags[classes[0]] = false
	out, e = s.Run(ctx, true, flags, revision(saved))
	if e == nil || !out.Committed || !out.CleanupFailed || len(out.Result) != 0 {
		t.Fatal("lost publication facts")
	}
	s = New(path)
	s.close = func(r *runtimepaths.Root) error { _ = r.Close(); return errors.New("private native detail") }
	out, e = s.Run(ctx, false, nil, "")
	if e == nil || !out.CleanupFailed || out.Committed || len(out.Result) != 0 || strings.Contains(e.Error(), "private") {
		t.Fatal("cleanup failure leak")
	}
	s = New(path)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if out, e := s.Run(canceled, true, flags, "stale"); e == nil || out.Committed {
		t.Fatal("canceled mutation")
	}
	if err := os.WriteFile(filepath.Join(path, datahome.RegistryRecoveryPath), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, e := s.Run(ctx, false, nil, ""); e == nil || out.Committed {
		t.Fatal("pending recovery admitted")
	}
}
