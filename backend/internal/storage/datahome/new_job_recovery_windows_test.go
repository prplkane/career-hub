//go:build windows

package datahome

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestNewJobRecoveryOwnedProcess(t *testing.T) {
	const fixtureEnv = "CAREERHUB_TEST_NEW_JOB_RECOVERY_ROOT"
	if home := os.Getenv(fixtureEnv); home != "" {
		root, err := runtimepaths.Open(home)
		if err != nil {
			t.Fatal(err.Code)
		}
		if err := root.ClaimForProcess(); err != nil {
			t.Fatal(err.Code)
		}
		if info, err := InspectOwnedForNewJobRecovery(root, nil); err != nil || info == nil || info.DataHomeID != homeID {
			t.Fatal("owned recovery could not bypass its exact sentinel")
		}
		if info, err := Inspect(home, nil); info != nil || err == nil || err.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatal("ordinary admission bypassed pending new-job intent")
		}
		if created, err := root.CreateFile(IntakeRecoveryPath, []byte("foreign synthetic intent")); err != nil || !created {
			t.Fatal("foreign sentinel fixture unavailable")
		}
		if info, err := InspectOwnedForNewJobRecovery(root, nil); info != nil || err == nil || err.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatal("new-job recovery bypassed another pending transaction")
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	home := t.TempDir()
	for _, item := range []struct{ name, value string }{
		{MarkerPath, validMarker}, {NewJobRecoveryPath, "opaque synthetic intent"},
	} {
		if err := os.WriteFile(filepath.Join(home, item.name), []byte(item.value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNewJobRecoveryOwnedProcess$")
	cmd.Env = append(os.Environ(), fixtureEnv+"="+home)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned new-job recovery process failed: %v: %s", err, output)
	}
}
