//go:build windows

package datahome

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/runtimepaths"
)

func TestIntakeRecoveryOwnedProcess(t *testing.T) {
	const envName = "CAREERHUB_TEST_INTAKE_RECOVERY_ROOT"
	if home := os.Getenv(envName); home != "" {
		root, e := runtimepaths.Open(home)
		if e != nil {
			t.Fatal(e.Code)
		}
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e.Code)
		}
		if info, e := InspectOwnedForIntakeRecovery(root, nil); e != nil || info == nil || info.DataHomeID != homeID {
			t.Fatal("owned intake recovery could not bypass its exact sentinel")
		}
		if info, e := Inspect(home, nil); info != nil || e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatal("ordinary read bypassed pending intake recovery")
		}
		if created, e := root.CreateFile(ResumeRecoveryPath, []byte("foreign synthetic intent")); e != nil || !created {
			t.Fatal("foreign sentinel fixture unavailable")
		}
		if info, e := InspectOwnedForIntakeRecovery(root, nil); info != nil || e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatal("intake recovery bypassed a different pending transaction")
		}
		if e := root.Close(); e != nil {
			t.Fatal(e)
		}
		return
	}
	home := t.TempDir()
	for _, item := range []struct{ name, value string }{
		{MarkerPath, validMarker}, {IntakeRecoveryPath, "opaque synthetic intent"},
	} {
		if err := os.WriteFile(filepath.Join(home, item.name), []byte(item.value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIntakeRecoveryOwnedProcess$")
	cmd.Env = append(os.Environ(), envName+"="+home)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned intake recovery process failed: %v: %s", err, output)
	}
}
