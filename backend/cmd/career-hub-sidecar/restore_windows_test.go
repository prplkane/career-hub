//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
)

// Actual execute -> maintenance.Run -> default Windows startup -> domain owner.
// No document state exists, so the provider is constructed but no OS credential
// is read/written/deleted. All fixtures are synthetic and outside installed Homes.
func TestDesktopMaintenanceRestoresLocalBackup(t *testing.T) {
	if os.Getenv("CH_RESTORE_PROCESS_CHILD") == "1" {
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
	write(home, "jobs/note.txt", "original archived job")
	write(resources, "VERSION", "1.2.3\n")
	write(resources, "src/intake_ui_static", "synthetic resources")
	write(resources, catalogstore.CoreGroupsPath, "[]\n")
	write(resources, catalogstore.CoreSkillsPath, "[]\n")
	request := map[string]any{"command": "backup_create", "protocol_version": 1, "data_root": home, "resource_root": resources, "channel": "dev", "confirm_writers_stopped": true, "archive_relative_path": "backups/input.zip"}
	run := func(wantExit int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDesktopMaintenanceRestoresLocalBackup$")
		cmd.Env = append(os.Environ(), "CH_RESTORE_PROCESS_CHILD=1")
		cmd.Stdin = bytes.NewReader(append(raw, '\n'))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantExit {
			t.Fatalf("exit %v result=%.2000s stderr=%.400s", err, out, stderr.Bytes())
		}
		var record map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(out), &record); err != nil {
			t.Fatal("not one JSON record", err)
		}
		for _, path := range []string{home, resources} {
			encoded, _ := json.Marshal(path)
			if bytes.Contains(out, encoded[1:len(encoded)-1]) {
				t.Fatal("absolute private path leaked")
			}
		}
		return record
	}
	if r := run(0); r["archive_publication"] != "published" {
		t.Fatal(r)
	}
	write(home, "jobs/note.txt", "newer active job")
	request["command"], request["confirm_writers_stopped"] = "backup_restore", false
	if r := run(3); r["event"] != "error" {
		t.Fatal(r)
	}
	raw, err := os.ReadFile(filepath.Join(home, "jobs/note.txt"))
	if err != nil || string(raw) != "newer active job" {
		t.Fatal("unconfirmed restore changed source", err)
	}
	request["confirm_writers_stopped"] = true
	r := run(0)
	if r["event"] != "backup_restore_result" || r["success"] != true || r["complete"] != true || r["journal_removal"] != "removed" || len(r) != 20 {
		t.Fatal(r)
	}
	raw, err = os.ReadFile(filepath.Join(home, "jobs/note.txt"))
	if err != nil || string(raw) != "original archived job" {
		t.Fatal("archive was not restored", err)
	}
	recovery, ok := r["recovery_relative_path"].(string)
	if !ok {
		t.Fatal(r)
	}
	raw, err = os.ReadFile(filepath.Join(home, filepath.FromSlash(recovery+"/jobs/note.txt")))
	if err != nil || string(raw) != "newer active job" {
		t.Fatal("original not retained", err)
	}
	if _, p := datahome.Inspect(home, nil); p != nil {
		t.Fatal("committed Home unavailable", p)
	}
	if _, err := os.Stat(filepath.Join(home, "applications/career_hub.sqlite3")); !os.IsNotExist(err) {
		t.Fatal("maintenance initialized schema", err)
	}
	request["command"] = "backup_restore_recover"
	delete(request, "archive_relative_path")
	r = run(4)
	if r["event"] != "backup_restore_recovery_result" || r["success"] != false || r["error_code"] != "DATA_HOME_RECOVERY_NOT_REQUIRED" || r["journal_removal"] != "not_removed" {
		t.Fatal("no-intent recovery misreported", r)
	}
}
