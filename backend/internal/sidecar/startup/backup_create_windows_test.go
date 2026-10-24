//go:build windows

package startup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/homebackup"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
)

type backupTestKeys struct {
	t     *testing.T
	reads int
}

func (b *backupTestKeys) Read(target string) ([]byte, bool, error) {
	b.reads++
	if target != "CareerHub/dev/document-key/dh_"+strings.Repeat("a", 32)+"/key_"+strings.Repeat("b", 32) {
		b.t.Fatal("unexpected key target")
	}
	return bytes.Repeat([]byte{7}, 32), true, nil
}
func (b *backupTestKeys) Write(string, []byte) error {
	b.t.Fatal("backup wrote credentials")
	return nil
}
func (b *backupTestKeys) Delete(string) (bool, error) {
	b.t.Fatal("backup deleted credentials")
	return false, nil
}

func TestBackupMaintenanceNativeBoundary(t *testing.T) {
	for _, mode := range []string{"success", "partial", "resource", "overlap", "missing-marker", "recovery", "channel", "unconfirmed", "cancel", "factory-failure", "documents"} {
		t.Run(mode, func(t *testing.T) {
			start := fixture(t)
			writeFixture(t, start.DataRoot, "jobs/a.txt", "synthetic job")
			if mode != "missing-marker" {
				raw, p := datahome.MarkerBytes("dev", "dh_"+strings.Repeat("a", 32))
				if p != nil {
					t.Fatal(p)
				}
				writeFixture(t, start.DataRoot, datahome.MarkerPath, string(raw))
			}
			if mode == "recovery" {
				writeFixture(t, start.DataRoot, datahome.RestoreRecoveryPath, "synthetic intent")
			}
			if mode == "documents" {
				state := keys.State{Version: 1, Algorithm: keys.Algorithm, DataHomeID: "dh_" + strings.Repeat("a", 32), ActiveKeyID: "key_" + strings.Repeat("b", 32)}
				raw, err := state.CanonicalBytes()
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, start.DataRoot, "documents/key_state.json", string(raw))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBackupMaintenanceNativeHelper$")
			cmd.Env = append(os.Environ(), "CH_BACKUP_MAINT_HOME="+start.DataRoot, "CH_BACKUP_MAINT_RES="+start.ResourceRoot, "CH_BACKUP_MAINT_MODE="+mode)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, out)
			}
		})
	}
}

func TestBackupMaintenanceNativeHelper(t *testing.T) {
	mode := os.Getenv("CH_BACKUP_MAINT_MODE")
	if mode == "" {
		return
	}
	start := protocol.StartRecord{DataRoot: os.Getenv("CH_BACKUP_MAINT_HOME"), ResourceRoot: os.Getenv("CH_BACKUP_MAINT_RES"), Channel: "dev"}
	before := snapshot(t, start.DataRoot)
	request := protocol.BackupCreateRequest{Start: start, ConfirmWritersStopped: true, ArchiveRelativePath: "backups/created.zip"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	switch mode {
	case "resource":
		request.Start.ResourceRoot += "-absent"
	case "overlap":
		request.Start.ResourceRoot = start.DataRoot
	case "channel":
		request.Start.Channel = "stable"
	case "unconfirmed":
		request.ConfirmWritersStopped = false
	case "cancel":
		cancel()
	case "partial":
		request.IncludeRoots = []string{"jobs"}
	}
	backend := &backupTestKeys{t: t}
	factories := 0
	result, err := createBackupForProcess(ctx, request, func() (secretstore.Backend, error) {
		factories++
		r, p := runtimepaths.Open(start.DataRoot)
		if p != nil {
			t.Fatal(p)
		}
		defer r.Close()
		if r.RequireProcessOwnership() != nil {
			t.Fatal("factory before ownership")
		}
		if _, p := datahome.Inspect(start.DataRoot, &request.Start.Channel); p != nil {
			t.Fatal("factory before admission")
		}
		if mode == "factory-failure" {
			return nil, errors.New("private backend prose")
		}
		return backend, nil
	})
	success := mode == "success" || mode == "partial" || mode == "documents"
	if (err == nil) != success {
		t.Fatal(result, err)
	}
	wantFactory := 0
	if success || mode == "factory-failure" {
		wantFactory = 1
	}
	if factories != wantFactory {
		t.Fatal("factory admission order", factories)
	}
	if backend.reads != 0 && mode != "documents" || mode == "documents" && backend.reads != 1 {
		t.Fatal("unexpected key reads", backend.reads)
	}
	if !success {
		want := map[string]string{"resource": "SIDECAR_RESOURCE_INVALID", "overlap": "SIDECAR_PATH_BOUNDARY_INVALID", "missing-marker": "DATA_HOME_UNAVAILABLE", "recovery": "DATA_HOME_RECOVERY_REQUIRED", "channel": "DATA_HOME_CHANNEL_MISMATCH", "unconfirmed": "BACKUP_WRITER_CONFIRMATION_REQUIRED", "cancel": "DATA_HOME_STARTUP_CANCELLED", "factory-failure": "BACKUP_DOCUMENT_KEY_UNAVAILABLE"}[mode]
		code := ""
		switch p := err.(type) {
		case *Error:
			code = p.Code
		case *homebackup.Error:
			code = p.Code
		}
		if code != want {
			t.Fatal("wrong refusal stage", code, want)
		}
		if result.ArchivePublication.Outcome != runtimepaths.FileNotPublished || result.SnapshotNamespace != runtimepaths.DirectoryNotCreated || !reflect.DeepEqual(before, snapshot(t, start.DataRoot)) {
			t.Fatal("refusal changed source", result)
		}
		return
	}
	r, p := runtimepaths.Open(start.DataRoot)
	if p != nil {
		t.Fatal(p)
	}
	defer r.Close()
	manifest, p2 := homebackup.VerifyLocal(context.Background(), r, result.ArchiveRelativePath)
	if p2 != nil {
		t.Fatal(p2)
	}
	if result.ArchivePublication.Outcome != runtimepaths.FilePublished || manifest.DataHomeChannel != "dev" || result.FullSourceValidation == nil {
		t.Fatal(result)
	}
	after := snapshot(t, start.DataRoot)
	for name, raw := range before {
		if after[name] != raw {
			t.Fatal("active source changed", name)
		}
	}
	if _, err := os.Stat(filepath.Join(start.DataRoot, "applications", "career_hub.sqlite3")); !os.IsNotExist(err) {
		t.Fatal("backup initialized database", err)
	}
}
