//go:build windows

package catalogtransaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func nativeApprovalAction(t *testing.T, root *runtimepaths.Root, action string) (Report, error) {
	t.Helper()
	_, data := approvalFixture(false, false)
	switch action {
	case "begin":
		return BeginApprovalOwned(context.Background(), root, testHomeID, data.Changes, data.Guards, accept)
	case "recover":
		return RecoverApprovalOwned(context.Background(), root, testHomeID, accept)
	case "conflict":
		report, err := RecoverApprovalOwned(context.Background(), root, testHomeID, accept)
		if err != ErrConflict || !report.IntentRecorded || len(report.AppliedFiles) != 0 {
			t.Fatal("changed source replayed")
		}
		return report, nil
	case "probe":
		for _, inspect := range []func(*runtimepaths.Root, *string) (*datahome.Info, *datahome.Error){datahome.InspectOwnedForRegistryRecovery, datahome.InspectOwnedForEvidenceRecovery, datahome.InspectOwnedForIntakeRecovery, datahome.InspectOwnedForResumeRecovery, datahome.InspectOwnedForEmailCredentialRecovery} {
			if _, err := inspect(root, nil); err == nil || err.Code != "DATA_HOME_RECOVERY_REQUIRED" {
				t.Fatal("foreign owner bypassed approval")
			}
		}
		return Report{}, nil
	case "foreign":
		if created, err := root.CreateFile(datahome.ResumeRecoveryPath, []byte("another intent")); err != nil || !created {
			t.Fatal("foreign sentinel fixture")
		}
		if _, err := RecoverApprovalOwned(context.Background(), root, testHomeID, accept); err != ErrInput {
			t.Fatal("approval bypassed other recovery")
		}
		return Report{}, nil
	default:
		return beginIntent(context.Background(), nativeCrashRoot{root, action}, data, accept)
	}
}

func nativeApprovalFixture(t *testing.T) (string, intent) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, data := approvalFixture(false, false)
	m.files[datahome.MarkerPath] = []byte(fmt.Sprintf(`{"application":"CareerHub","channel":"dev","data_home_id":"%s","format_version":2}`, testHomeID))
	for path, raw := range m.files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if os.MkdirAll(filepath.Dir(full), 0700) != nil || os.WriteFile(full, raw, 0600) != nil {
			t.Fatal("fixture")
		}
	}
	return root, data
}

func TestNativeApprovalProcessExitAndRestart(t *testing.T) {
	_, fixture := approvalFixture(false, false)
	stops := []string{"intent"}
	for _, change := range fixture.Changes {
		stops = append(stops, change.Path)
	}
	stops = append(stops, "cleanup")
	for _, stop := range stops {
		t.Run(stop, func(t *testing.T) {
			root, data := nativeApprovalFixture(t)
			runNativeTransaction(t, root, "approval:"+stop, true)
			_, err := datahome.Inspect(root, nil)
			if stop == "cleanup" {
				if err != nil {
					t.Fatal("cleaned transaction blocked")
				}
			} else {
				if err == nil || err.Code != "DATA_HOME_RECOVERY_REQUIRED" {
					t.Fatal("partial approval admitted")
				}
				runNativeTransaction(t, root, "approval:probe", false)
			}
			report := runNativeTransaction(t, root, "approval:recover", false)
			if stop == "cleanup" {
				if report.IntentRecorded || report.Completed {
					t.Fatal("absent intent fabricated")
				}
			} else if !report.Completed || !report.JournalRemoved {
				t.Fatal("recovery incomplete")
			}
			for _, c := range data.Changes {
				raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.Path)))
				if err != nil || !bytes.Equal(raw, c.After) {
					t.Fatal("after-state not retained")
				}
			}
			for _, guard := range data.Guards {
				raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(guard.Path)))
				if err != nil || fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) != guard.Revision {
					t.Fatal("source evidence changed")
				}
			}
			again := runNativeTransaction(t, root, "approval:recover", false)
			if again.IntentRecorded || again.Completed || len(again.AppliedFiles) != 0 {
				t.Fatal("recovery not idempotent")
			}
			if _, err := datahome.Inspect(root, nil); err != nil {
				t.Fatal("recovered Home blocked")
			}
		})
	}
}

func TestNativeApprovalOwnedBeginAndRefusals(t *testing.T) {
	root, _ := nativeApprovalFixture(t)
	if report := runNativeTransaction(t, root, "approval:begin", false); !report.Completed || len(report.AppliedFiles) != 8 {
		t.Fatal("owned begin failed")
	}
	root, data := nativeApprovalFixture(t)
	runNativeTransaction(t, root, "approval:"+data.Changes[0].Path, true)
	path := filepath.Join(root, filepath.FromSlash(data.Guards[1].Path))
	if os.WriteFile(path, []byte("external review edit"), 0600) != nil {
		t.Fatal("fixture edit")
	}
	runNativeTransaction(t, root, "approval:conflict", false)
	for _, change := range data.Changes[1:7] {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(change.Path)))
		if err != nil || !bytes.Equal(raw, change.Before) {
			t.Fatal("unverified work continued")
		}
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(data.Changes[7].Path))); !os.IsNotExist(err) {
		t.Fatal("result created despite changed review")
	}
	root, _ = nativeApprovalFixture(t)
	runNativeTransaction(t, root, "approval:intent", true)
	runNativeTransaction(t, root, "approval:foreign", false)
}
