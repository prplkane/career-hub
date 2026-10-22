//go:build windows

package startup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/keys"
	"careerhub.local/backend/internal/homebackup"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sidecar/protocol"
)

type restoreMaintenanceKeys struct {
	*backupTestKeys
	failAfterFirst bool
}

func (b *restoreMaintenanceKeys) Read(target string) ([]byte, bool, error) {
	raw, present, err := b.backupTestKeys.Read(target)
	if b.failAfterFirst && b.reads > 1 {
		clear(raw)
		return nil, false, nil
	}
	return raw, present, err
}

func TestRestoreMaintenanceNativeBoundary(t *testing.T) {
	for _, mode := range []string{"success", "resource", "overlap", "missing-marker", "recovery-present", "channel", "unconfirmed", "cancel", "factory-failure", "late-key-failure", "recover-none", "recover-wrong-kind", "recover-mixed", "recover-invalid-journal"} {
		t.Run(mode, func(t *testing.T) {
			start := fixture(t)
			writeFixture(t, start.DataRoot, "jobs/original.txt", "original")
			if mode != "missing-marker" {
				raw, p := datahome.MarkerBytes("dev", "dh_"+strings.Repeat("a", 32))
				if p != nil {
					t.Fatal(p)
				}
				writeFixture(t, start.DataRoot, datahome.MarkerPath, string(raw))
			}
			if mode == "recovery-present" || mode == "recover-mixed" || mode == "recover-invalid-journal" {
				writeFixture(t, start.DataRoot, datahome.RestoreRecoveryPath, "synthetic invalid journal")
			}
			if mode == "recover-wrong-kind" || mode == "recover-mixed" {
				writeFixture(t, start.DataRoot, datahome.NewJobRecoveryPath, "synthetic pending new job")
			}
			run := func(mode string) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestoreMaintenanceNativeHelper$")
				cmd.Env = append(os.Environ(), "CH_RESTORE_MAINT_HOME="+start.DataRoot, "CH_RESTORE_MAINT_RES="+start.ResourceRoot, "CH_RESTORE_MAINT_MODE="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v\n%s", mode, err, out)
				}
			}
			run(mode)
			if mode == "late-key-failure" {
				run("recover-success")
			}
		})
	}
}

func TestRestoreMaintenanceNativeHelper(t *testing.T) {
	mode := os.Getenv("CH_RESTORE_MAINT_MODE")
	if mode == "" {
		return
	}
	start := protocol.StartRecord{DataRoot: os.Getenv("CH_RESTORE_MAINT_HOME"), ResourceRoot: os.Getenv("CH_RESTORE_MAINT_RES"), Channel: "dev"}
	command := protocol.BackupRestoreCommand
	if strings.HasPrefix(mode, "recover-") {
		command = protocol.BackupRestoreRecoverCommand
	}
	request := protocol.BackupRestoreRequest{Command: command, Start: start, ConfirmWritersStopped: true}
	if command == protocol.BackupRestoreCommand {
		request.ArchiveRelativePath = "backups/input.zip"
		files := map[string][]byte{"jobs/candidate.txt": []byte("candidate")}
		if mode == "late-key-failure" {
			state := keys.State{Version: 1, Algorithm: keys.Algorithm, DataHomeID: "dh_" + strings.Repeat("a", 32), ActiveKeyID: "key_" + strings.Repeat("b", 32)}
			raw, err := state.CanonicalBytes()
			if err != nil {
				t.Fatal(err)
			}
			files["documents/key_state.json"] = raw
		}
		manifest := homebackup.Manifest{FormatVersion: 2, DataHomeFormatVersion: 2, DataHomeID: "dh_" + strings.Repeat("a", 32), DataHomeChannel: "dev", Files: []homebackup.File{}}
		var buffer bytes.Buffer
		z := zip.NewWriter(&buffer)
		for name, raw := range files {
			digest := sha256.Sum256(raw)
			manifest.Files = append(manifest.Files, homebackup.File{RelativePath: name, SizeBytes: int64(len(raw)), SHA256: hex.EncodeToString(digest[:])})
			w, err := z.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(raw); err != nil {
				t.Fatal(err)
			}
		}
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		w, err := z.Create(homebackup.ManifestName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, start.DataRoot, request.ArchiveRelativePath, buffer.String())
	}
	before := snapshot(t, start.DataRoot)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	switch mode {
	case "resource":
		request.Start.ResourceRoot += "-missing"
	case "overlap":
		request.Start.ResourceRoot = start.DataRoot
	case "channel":
		request.Start.Channel = "stable"
	case "unconfirmed":
		request.ConfirmWritersStopped = false
	case "cancel":
		cancel()
	}
	backend := &restoreMaintenanceKeys{backupTestKeys: &backupTestKeys{t: t}, failAfterFirst: mode == "late-key-failure"}
	factories := 0
	out, err := runBackupRestoreForProcess(ctx, request, func() (secretstore.Backend, error) {
		factories++
		r, q := runtimepaths.Open(start.DataRoot)
		if q != nil {
			t.Fatal(q)
		}
		defer r.Close()
		if r.RequireProcessOwnership() != nil {
			t.Fatal("factory before ownership")
		}
		classification, p := datahome.ClassifyOwnedRecovery(r, &request.Start.Channel)
		if p != nil {
			t.Fatal("factory before channel/marker admission", p)
		}
		if command == protocol.BackupRestoreRecoverCommand {
			if requireRecoveryKind(classification, datahome.RecoveryRestore) != nil {
				t.Fatal("factory before exact recovery kind")
			}
		} else if classification.Cardinality != datahome.RecoveryNone {
			t.Fatal("factory before normal admission")
		}
		if mode == "factory-failure" {
			return nil, errors.New("private provider prose")
		}
		return backend, nil
	})
	want := mode == "success" || mode == "recover-success"
	if (err == nil) != want {
		t.Fatal(mode, out, err)
	}
	wantFactory := 0
	if want || mode == "factory-failure" || mode == "late-key-failure" || mode == "recover-invalid-journal" {
		wantFactory = 1
	}
	if factories != wantFactory {
		t.Fatal("factory admission order", factories, wantFactory)
	}
	if want {
		if !out.Finish.Complete || out.Finish.JournalRemoval != "removed" {
			t.Fatal(out)
		}
		if _, p := datahome.Inspect(start.DataRoot, &start.Channel); p != nil {
			t.Fatal("completed Home not admitted", p)
		}
		name, value := "jobs/candidate.txt", "candidate"
		if mode == "recover-success" {
			name, value = "jobs/original.txt", "original"
		}
		raw, e := os.ReadFile(filepath.Join(start.DataRoot, filepath.FromSlash(name)))
		if e != nil || string(raw) != value {
			t.Fatal("wrong active data", e)
		}
		return
	}
	if mode == "late-key-failure" {
		var p *homebackup.Error
		if !errors.As(err, &p) || p.Code != "BACKUP_DOCUMENT_KEY_UNAVAILABLE" || out.Journal.Journal.Outcome != runtimepaths.FilePublished || out.Finish.JournalRemoval != "not_removed" || !out.Roots.RootPhaseComplete {
			t.Fatal("partial facts lost", out, err)
		}
		if _, p := datahome.Inspect(start.DataRoot, nil); p == nil {
			t.Fatal("pending restore admitted startup")
		}
	} else if !reflect.DeepEqual(before, snapshot(t, start.DataRoot)) || out.Finish.JournalRemoval != "not_removed" {
		t.Fatal("admission failure changed Home", out)
	}
	if mode != "late-key-failure" && backend.reads != 0 {
		t.Fatal("unexpected credential reads", backend.reads)
	}
}
