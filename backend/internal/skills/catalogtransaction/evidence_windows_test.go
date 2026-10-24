//go:build windows

package catalogtransaction

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
)

func nativeEvidenceFixture(t *testing.T) (string, intent) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	m, data := evidenceFixture(false, false)
	m.files[datahome.MarkerPath] = []byte(fmt.Sprintf(`{"application":"CareerHub","channel":"dev","data_home_id":"%s","format_version":2}`, testHomeID))
	for path, raw := range m.files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if os.MkdirAll(filepath.Dir(full), 0700) != nil || os.WriteFile(full, raw, 0600) != nil {
			t.Fatal("fixture")
		}
	}
	return root, data
}

func TestNativeEvidenceTransactionProcessExitAndRestart(t *testing.T) {
	for _, stop := range []string{"intent", "record", "groups", "skills", "cleanup"} {
		t.Run(stop, func(t *testing.T) {
			root, data := nativeEvidenceFixture(t)
			runNativeTransaction(t, root, "evidence:"+stop, true)
			_, e := datahome.Inspect(root, nil)
			if stop == "cleanup" {
				if e != nil {
					t.Fatal("completed cleanup blocked")
				}
			} else {
				if e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" {
					t.Fatal("partial evidence admitted")
				}
				runNativeTransaction(t, root, "evidence:probe", false)
			}
			report := runNativeTransaction(t, root, "evidence:recover", false)
			if stop == "cleanup" {
				if report.IntentRecorded || report.Completed {
					t.Fatal("invented journal")
				}
			} else if !report.Completed || !report.JournalRemoved {
				t.Fatal("recovery incomplete")
			}
			for _, change := range data.Changes {
				raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(change.Path)))
				if e != nil || !bytes.Equal(raw, change.After) {
					t.Fatal("after-state")
				}
			}
			for _, guard := range data.Guards {
				raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(guard.Path)))
				if e != nil || guard.Revision != fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) {
					t.Fatal("companion changed")
				}
			}
			again := runNativeTransaction(t, root, "evidence:recover", false)
			if again.IntentRecorded || again.Completed || len(again.AppliedFiles) > 0 {
				t.Fatal("non-idempotent recovery")
			}
			if _, e := datahome.Inspect(root, nil); e != nil {
				t.Fatal("home still blocked")
			}
		})
	}
}

func TestNativeEvidenceOwnedBeginAndChangedGuardRefusal(t *testing.T) {
	root, _ := nativeEvidenceFixture(t)
	report := runNativeTransaction(t, root, "evidence:begin", false)
	if !report.Completed || len(report.AppliedFiles) != 3 {
		t.Fatal("owned begin")
	}
	root, data := nativeEvidenceFixture(t)
	runNativeTransaction(t, root, "evidence:record", true)
	// After process exit a user could edit a companion. Explicit recovery must
	// stop before performing any remaining write; no rollback guesses.
	path := filepath.Join(root, filepath.FromSlash(data.Guards[0].Path))
	if os.WriteFile(path, []byte("external user edit"), 0600) != nil {
		t.Fatal("edit")
	}
	// The lower-level recovery refusal is exhaustively covered in memory. A
	// native child checks the exact failure without dumping journal contents.
	runNativeTransaction(t, root, "evidence:conflict", false)
	raw, e := os.ReadFile(path)
	if e != nil || string(raw) != "external user edit" {
		t.Fatal("user edit overwritten")
	}
	for _, change := range data.Changes[1:] {
		raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(change.Path)))
		if e != nil || !bytes.Equal(raw, change.Before) {
			t.Fatal("catalog changed before guard validation")
		}
	}
}
