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

func TestEmailCredentialRecoveryOwnedProcess(t *testing.T) {
	const envName = "CAREERHUB_TEST_EMAIL_CREDENTIAL_RECOVERY_ROOT"
	if home := os.Getenv(envName); home != "" {
		root, e := runtimepaths.Open(home)
		if e != nil {
			t.Fatal(e.Code)
		}
		if info, e := InspectOwnedForEmailCredentialRecovery(root, nil); info != nil || e == nil || e.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
			t.Fatal("unowned process bypassed email credential recovery")
		}
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e.Code)
		}
		if info, e := InspectOwnedForEmailCredentialRecovery(root, nil); e != nil || info == nil || info.DataHomeID != homeID {
			t.Fatal("owned recovery could not bypass its exact sentinel")
		}
		if info, e := Inspect(home, nil); info != nil || e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatal("ordinary admission bypassed pending email credential recovery")
		}
		if created, e := root.CreateFile(ResumeRecoveryPath, []byte("foreign synthetic intent")); e != nil || !created {
			t.Fatal("foreign sentinel fixture unavailable")
		}
		if info, e := InspectOwnedForEmailCredentialRecovery(root, nil); info != nil || e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatal("email credential recovery bypassed a foreign intent")
		}
		if e := root.Close(); e != nil {
			t.Fatal(e)
		}
		return
	}
	home := t.TempDir()
	for _, item := range []struct{ name, value string }{
		{MarkerPath, validMarker}, {EmailCredentialRecoveryPath, "opaque synthetic intent"},
	} {
		if err := os.WriteFile(filepath.Join(home, item.name), []byte(item.value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmailCredentialRecoveryOwnedProcess$")
	command.Env = append(os.Environ(), envName+"="+home)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("owned email credential recovery process failed: %v: %s", err, output)
	}
}
