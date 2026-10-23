//go:build windows

package maintenance

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const documentMigrationAdapterChild = "CAREERHUB_DOCUMENT_MIGRATION_ADAPTER_CHILD"

type memoryDocumentKeyBackend struct{ values map[string][]byte }

func (backend *memoryDocumentKeyBackend) Write(target string, secret []byte) error {
	backend.values[target] = bytes.Clone(secret)
	return nil
}

func (backend *memoryDocumentKeyBackend) Read(target string) ([]byte, bool, error) {
	value, present := backend.values[target]
	return bytes.Clone(value), present, nil
}

func (backend *memoryDocumentKeyBackend) Delete(target string) (bool, error) {
	_, present := backend.values[target]
	delete(backend.values, target)
	return present, nil
}

func TestDocumentMigrationOwnedAdapterWindowsChild(t *testing.T) {
	if os.Getenv(documentMigrationAdapterChild) == "1" {
		rootPath := os.Getenv("CAREERHUB_DOCUMENT_MIGRATION_ADAPTER_ROOT")
		root, pathProblem := runtimepaths.Open(rootPath)
		if pathProblem != nil {
			t.Fatal(pathProblem.Code)
		}
		if pathProblem = root.ClaimForProcess(); pathProblem != nil {
			t.Fatal(pathProblem.Code)
		}
		backend := &memoryDocumentKeyBackend{values: map[string][]byte{}}
		planRequest, err := protocol.ParseDocumentMigrationMaintenance(map[string]any{
			"command": protocol.DocumentMigrationPlanCommand, "protocol_version": 1, "data_root": rootPath,
		}, protocol.PathOptions{})
		if err != nil {
			t.Fatal(err)
		}
		planRecord, code := executeDocumentMigrationOwned(context.Background(), planRequest, root, backend)
		planID, ok := planRecord["plan_id"].(string)
		if code != "" || !ok || planRecord["document_count"] != 0 {
			t.Fatalf("owned plan failed: %#v / %s", planRecord, code)
		}
		applyRequest := protocol.DocumentMigrationMaintenanceRequest{
			Command: protocol.DocumentMigrationApplyCommand, DataRoot: rootPath, ConfirmPlan: planID,
		}
		complete, code := executeDocumentMigrationOwned(context.Background(), applyRequest, root, backend)
		if code != "" || complete["event"] != protocol.DocumentMigrationCompleteEvent ||
			complete["plan_id"] != planID || len(backend.values) != 1 {
			t.Fatalf("owned apply failed: %#v / %s / %d", complete, code, len(backend.values))
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}

	rootPath := t.TempDir()
	marker := []byte(`{"application":"CareerHub","channel":"dev","format_version":1}` + "\n")
	if err := os.WriteFile(filepath.Join(rootPath, filepath.FromSlash(datahome.MarkerPath)), marker, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestDocumentMigrationOwnedAdapterWindowsChild$")
	command.Env = append(os.Environ(), documentMigrationAdapterChild+"=1", "CAREERHUB_DOCUMENT_MIGRATION_ADAPTER_ROOT="+rootPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("document migration adapter child failed: %v: %.2400s", err, output)
	}
	activeMarker, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(datahome.MarkerPath)))
	if err != nil {
		t.Fatal(err)
	}
	info, markerProblem := datahome.ParseMarkerForDocumentMigration(activeMarker)
	if markerProblem != nil || info.FormatVersion != 2 || info.Channel != "dev" {
		t.Fatal("adapter did not activate the exact v2 marker")
	}
	if _, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(datahome.DocumentRecoveryPath))); !os.IsNotExist(err) {
		t.Fatal("adapter retained a live migration journal")
	}
	entries, err := os.ReadDir(filepath.Join(rootPath, "backups", "document-encryption-migration"))
	if err != nil || len(entries) != 1 || !entries[0].IsDir() || !strings.HasPrefix(entries[0].Name(), "sec240_") {
		t.Fatalf("adapter did not retain one bounded recovery receipt: %v / %#v", err, entries)
	}
}
