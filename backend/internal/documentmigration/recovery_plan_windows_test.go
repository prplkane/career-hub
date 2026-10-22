//go:build windows

package documentmigration

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

const recoveryPlanChild = "CAREERHUB_DOCUMENT_MIGRATION_RECOVERY_PLAN_CHILD"

func TestBuildRecoveryPlanOwnedWindowsChild(t *testing.T) {
	if os.Getenv(recoveryPlanChild) == "1" {
		root, problem := runtimepaths.Open(os.Getenv("CAREERHUB_DOCUMENT_MIGRATION_RECOVERY_ROOT"))
		if problem != nil {
			t.Fatal(problem.Code)
		}
		if problem := root.ClaimForProcess(); problem != nil {
			t.Fatal(problem.Code)
		}
		plan, present, migrationProblem := BuildRecoveryPlanOwned(root)
		want := []RecoveryAction{
			RecoveryPreserveCandidate, RecoveryRestoreOriginal, RecoveryPreserveStaging,
			RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal,
		}
		if migrationProblem != nil || !present || !reflect.DeepEqual(plan.Actions, want) {
			t.Fatalf("unexpected owned recovery plan: %+v / %v / %#v", plan, present, migrationProblem)
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}

	fixture := recoveryTreeFixture(t, true)
	rootPath := t.TempDir()
	for relative := range fixture.dirs {
		if err := os.MkdirAll(filepath.Join(rootPath, filepath.FromSlash(relative)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture.files["documents/key_state.json"] = []byte("fictional active candidate")
	fixture.files[stageRelative(recoveryMigrationID)+"/key_state.json"] = []byte("fictional stage")
	for relative, content := range fixture.files {
		path := filepath.Join(rootPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(datahome.DocumentRecoveryPath)))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestBuildRecoveryPlanOwnedWindowsChild$")
	command.Env = append(os.Environ(), recoveryPlanChild+"=1", "CAREERHUB_DOCUMENT_MIGRATION_RECOVERY_ROOT="+rootPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("owned recovery-plan child failed: %v: %.1500s", err, output)
	}
	after, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(datahome.DocumentRecoveryPath)))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("recovery plan changed the journal")
	}
	if _, err := os.Stat(filepath.Join(rootPath, "documents", "key_state.json")); err != nil {
		t.Fatal("recovery plan changed the active candidate")
	}
}
