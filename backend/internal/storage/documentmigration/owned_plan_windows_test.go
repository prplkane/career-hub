//go:build windows

package documentmigration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

const ownedPlanChild = "CAREERHUB_DOCUMENT_MIGRATION_PLAN_CHILD"

func TestBuildPlanOwnedWindowsChild(t *testing.T) {
	if os.Getenv(ownedPlanChild) == "1" {
		root, problem := runtimepaths.Open(os.Getenv("CAREERHUB_DOCUMENT_MIGRATION_ROOT"))
		if problem != nil {
			t.Fatal(problem.Code)
		}
		if problem := root.ClaimForProcess(); problem != nil {
			t.Fatal(problem.Code)
		}
		plan, migrationProblem := buildPlanOwned(root, true, unprotect)
		if migrationProblem != nil || plan.DocumentCount != 1 || plan.CiphertextFileCount != 2 {
			t.Fatalf("unexpected owned plan: %+v / %#v", plan, migrationProblem)
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}

	source := sourceFixture(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".career_hub_data_home.json"), source.Marker, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "documents", "files"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(legacyLedgerPath)), source.LedgerCiphertext, 0600); err != nil {
		t.Fatal(err)
	}
	for name, content := range source.Files {
		if err := os.WriteFile(filepath.Join(root, "documents", "files", name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	beforeMarker := append([]byte(nil), source.Marker...)
	command := exec.Command(os.Args[0], "-test.run=^TestBuildPlanOwnedWindowsChild$")
	command.Env = append(os.Environ(), ownedPlanChild+"=1", "CAREERHUB_DOCUMENT_MIGRATION_ROOT="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("owned plan child failed: %v: %.1000s", err, output)
	}
	afterMarker, err := os.ReadFile(filepath.Join(root, ".career_hub_data_home.json"))
	if err != nil || string(afterMarker) != string(beforeMarker) {
		t.Fatal("owned plan changed the legacy marker")
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 2 {
		t.Fatalf("owned plan created data: %v / %v", entries, err)
	}
}
