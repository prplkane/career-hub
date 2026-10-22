//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/homebackup"
)

const maintenanceProcessChild = "CAREERHUB_MAINTENANCE_PROCESS_CHILD"

func TestDesktopMaintenanceProcessPlansFictionalLegacyHome(t *testing.T) {
	if os.Getenv(maintenanceProcessChild) == "1" {
		os.Exit(execute([]string{"--desktop-maintenance"}, os.Stdin, os.Stdout))
	}

	root := t.TempDir()
	marker := []byte(`{"application":"CareerHub","channel":"dev","format_version":1}` + "\n")
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(datahome.MarkerPath)), marker, 0600); err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]any{
		"command": "document_migration_plan", "protocol_version": 1, "data_root": root,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestDesktopMaintenanceProcessPlansFictionalLegacyHome$")
	command.Env = append(os.Environ(), maintenanceProcessChild+"=1")
	command.Stdin = bytes.NewReader(append(request, '\n'))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("maintenance child failed: %v: %.2400s", err, output)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output), &record); err != nil {
		t.Fatalf("invalid child record: %v: %.2400s", err, output)
	}
	if record["event"] != "document_migration_plan" || record["document_count"] != float64(0) {
		t.Fatalf("unexpected child record: %#v", record)
	}
}

// Runs the production execute -> maintenance.Run -> default native startup
// composition in an isolated process. The fixture contains no document key
// state, so constructing the Windows provider resolves DLL symbols only: no
// credential lookup/write/delete, mailbox, server or installed Home is involved.
func TestDesktopMaintenanceCreatesLocalBackup(t *testing.T) {
	if os.Getenv("CH_BACKUP_PROCESS_CHILD") == "1" {
		os.Exit(execute([]string{"--desktop-maintenance"}, os.Stdin, os.Stdout))
	}
	base := t.TempDir()
	home, resources := filepath.Join(base, "home"), filepath.Join(base, "resources")
	write := func(root, name, value string) {
		t.Helper()
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker, p := datahome.MarkerBytes("dev", "dh_0123456789abcdef0123456789abcdef")
	if p != nil {
		t.Fatal(p)
	}
	write(home, datahome.MarkerPath, string(marker))
	write(home, "jobs/note.txt", "synthetic job")
	write(resources, "VERSION", "1.2.3\n")
	write(resources, "src/intake_ui_static", "synthetic resources")
	write(resources, catalogstore.CoreGroupsPath, "[]\n")
	write(resources, catalogstore.CoreSkillsPath, "[]\n")
	request := map[string]any{"command": "backup_create", "protocol_version": 1, "data_root": home, "resource_root": resources, "channel": "dev", "confirm_writers_stopped": true, "archive_relative_path": "backups/test.zip"}
	run := func(wantExit int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDesktopMaintenanceCreatesLocalBackup$")
		cmd.Env = append(os.Environ(), "CH_BACKUP_PROCESS_CHILD=1")
		cmd.Stdin = bytes.NewReader(append(raw, '\n'))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantExit {
			t.Fatalf("child exit: %v output=%.1600s stderr=%.400s", err, out, stderr.Bytes())
		}
		var record map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(out), &record); err != nil {
			t.Fatalf("not exactly one JSON record: %v", err)
		}
		if bytes.Contains(out, []byte(home)) || bytes.Contains(out, []byte(resources)) {
			t.Fatal("absolute private path in result")
		}
		return record
	}
	request["confirm_writers_stopped"] = false
	run(3)
	if _, err := os.Stat(filepath.Join(home, ".career_hub_staging")); !os.IsNotExist(err) {
		t.Fatal("unconfirmed command wrote staging", err)
	}
	request["confirm_writers_stopped"] = true
	result := run(0)
	if result["event"] != "backup_create_result" || result["success"] != true || result["archive_publication"] != "published" || result["full_source_validated"] != true {
		t.Fatal(result)
	}
	cleanup, ok := result["snapshot_cleanup"].(map[string]any)
	if !ok || len(result) != 15 || cleanup["status"] != "complete" || cleanup["snapshot_removal"] != "removed" || cleanup["removed_files"] != result["staged_files"] {
		t.Fatal("cleanup facts missing", result)
	}
	if _, err := os.Lstat(filepath.Join(home, filepath.FromSlash(result["snapshot_relative_path"].(string)))); !os.IsNotExist(err) {
		t.Fatal("completed snapshot still exists", err)
	}
	archivePath := filepath.Join(home, "backups", "test.zip")
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 2 {
		t.Fatal("wrong archive membership")
	}
	for _, file := range reader.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(r)
		closeErr := r.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
		if file.Name == homebackup.ManifestName {
			manifest, p := homebackup.ParseManifest(raw)
			if p != nil || len(manifest.Files) != 1 || manifest.Files[0].RelativePath != "jobs/note.txt" {
				t.Fatal(manifest, p)
			}
		} else if file.Name != "jobs/note.txt" || string(raw) != "synthetic job" {
			t.Fatal("unexpected payload")
		}
	}
	result = run(4)
	if result["error_code"] != "BACKUP_DESTINATION_EXISTS" || result["archive_publication"] != "not_published" {
		t.Fatal(result)
	}
	after, err := os.ReadFile(archivePath)
	if err != nil || sha256.Sum256(archive) != sha256.Sum256(after) {
		t.Fatal("existing archive changed", err)
	}
	// A fresh process can claim the same Home again only after the previous
	// child has exited; this also exercises a second distinct no-overwrite target.
	request["archive_relative_path"] = "backups/second.zip"
	run(0)
	if raw, err := os.ReadFile(filepath.Join(home, datahome.MarkerPath)); err != nil || !bytes.Equal(raw, marker) {
		t.Fatal("marker changed", err)
	}
	if _, err := os.Stat(filepath.Join(home, "applications", "career_hub.sqlite3")); !os.IsNotExist(err) {
		t.Fatal("maintenance initialized SQLite", err)
	}
}
