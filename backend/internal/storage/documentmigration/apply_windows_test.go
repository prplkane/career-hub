//go:build windows

package documentmigration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const applyCoordinatorChild = "CAREERHUB_DOCUMENT_MIGRATION_APPLY_COORDINATOR_CHILD"

func TestApplyOwnedWindowsChild(t *testing.T) {
	if os.Getenv(applyCoordinatorChild) == "1" {
		root, pathProblem := runtimepaths.Open(os.Getenv("CAREERHUB_DOCUMENT_MIGRATION_APPLY_ROOT"))
		if pathProblem != nil {
			t.Fatal(pathProblem.Code)
		}
		if pathProblem = root.ClaimForProcess(); pathProblem != nil {
			t.Fatal(pathProblem.Code)
		}
		plan, problem := buildPlanOwned(root, true, unprotect)
		if problem != nil {
			t.Fatal(problem.Code)
		}
		random, _ := deterministicApplyRandom()
		trace := []string{}
		backend := &migrationKeyBackend{values: map[string][]byte{}, trace: &trace}
		result, problem := applyOwned(context.Background(), root, backend, random, unprotect, true, plan.PlanID)
		if problem != nil || result.Plan != plan || len(backend.values) != 1 {
			code := ""
			if problem != nil {
				code = problem.Code
			}
			t.Fatalf("native coordinator failed: %+v / %s / %d", result, code, len(backend.values))
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}

	source := sourceFixture(t)
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, ".career_hub_data_home.json"), source.Marker, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootPath, "documents", "files"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, filepath.FromSlash(legacyLedgerPath)), source.LedgerCiphertext, 0600); err != nil {
		t.Fatal(err)
	}
	for name, content := range source.Files {
		if err := os.WriteFile(filepath.Join(rootPath, "documents", "files", name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(os.Args[0], "-test.run=^TestApplyOwnedWindowsChild$")
	command.Env = append(os.Environ(), applyCoordinatorChild+"=1", "CAREERHUB_DOCUMENT_MIGRATION_APPLY_ROOT="+rootPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native apply coordinator child failed: %v: %.10000s", err, output)
	}
	if _, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(datahome.DocumentRecoveryPath))); !os.IsNotExist(err) {
		t.Fatal("native coordinator retained the live journal")
	}
	marker, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(datahome.MarkerPath)))
	if err != nil {
		t.Fatal(err)
	}
	info, markerProblem := datahome.ParseMarkerForDocumentMigration(marker)
	if markerProblem != nil || info.FormatVersion != TargetFormatVersion {
		t.Fatal("native coordinator did not activate v2")
	}
	state, err := os.ReadFile(filepath.Join(rootPath, "documents", stageKeyStateName))
	if err != nil {
		t.Fatal(err)
	}
	parsed, stateProblem := keys.ParseState(state)
	if stateProblem != nil || parsed.DataHomeID != info.DataHomeID {
		t.Fatal("native coordinator published an inconsistent key state")
	}
	base := filepath.Join(rootPath, filepath.FromSlash(recoveryRelative(recoveryMigrationID)))
	for _, relative := range []string{"completed.json", "activation_original"} {
		if _, err := os.Stat(filepath.Join(base, relative)); err != nil {
			t.Fatalf("native coordinator artifact missing: %s", relative)
		}
	}
}
