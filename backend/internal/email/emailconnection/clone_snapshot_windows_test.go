//go:build windows

package emailconnection

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

type readOnlyCloneSecrets struct{ reads int }

func (b *readOnlyCloneSecrets) Read(string) ([]byte, bool, error) {
	b.reads++
	return []byte("synthetic fixture"), true, nil
}
func (*readOnlyCloneSecrets) Write(string, []byte) error {
	panic("snapshot attempted credential write")
}
func (*readOnlyCloneSecrets) Delete(string) (bool, error) {
	panic("snapshot attempted credential delete")
}

func TestClonedSnapshotHelper(t *testing.T) {
	path := os.Getenv("CAREERHUB_CLONE_SNAPSHOT_ROOT")
	if path == "" {
		return
	}
	ctx := context.Background()
	mode := os.Getenv("CAREERHUB_CLONE_SNAPSHOT_MODE")
	if mode == "create" {
		if _, err := datahome.BootstrapForProcess(ctx, path, "dev"); err != nil {
			t.Fatal(err)
		}
		if _, err := schema.PrepareOwned(ctx, path); err != nil {
			t.Fatal(err)
		}
	}
	root, e := runtimepaths.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if mode != "create" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	home, admission := datahome.Inspect(path, nil)
	if admission != nil {
		t.Fatal(admission)
	}
	if mode == "create" {
		scope, e := root.CredentialScope()
		if e != nil {
			t.Fatal(e)
		}
		target, err := emailcredentials.NewScopedGenerationName(*home, scope)
		if err != nil {
			t.Fatal(err)
		}
		connectionRow(t, path, emailcredentials.RowWitness{Present: true, Revision: 1, Enabled: true, SecretName: target})
	}
	dbPath := filepath.Join(path, filepath.FromSlash(databasePath))
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	backend := &readOnlyCloneSecrets{}
	snapshot, problem := ReadOwnedSnapshot(ctx, path, *home, backend)
	if problem != nil {
		t.Fatal(problem)
	}
	if !snapshot.Configured || snapshot.AccountAddress != "alerts@example.test" {
		t.Fatal("snapshot lost account metadata")
	}
	if mode == "copy" {
		if backend.reads != 0 || snapshot.CredentialPresent || snapshot.LastErrorCode != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" {
			t.Fatal("copy reached credentials or missed reconnect warning")
		}
		config, err := applicationwrite.New(path).EmailSyncState().ConnectionConfig(ctx)
		if err == nil || err.Error() != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" || config != (applicationwrite.EmailConnectionConfig{}) {
			t.Fatal("copied database exposed a usable sync configuration")
		}
	} else if backend.reads != 1 || !snapshot.CredentialPresent || snapshot.LastErrorCode != "" {
		t.Fatal("original Home lost its own credential admission")
	}
	after, err := os.ReadFile(dbPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("snapshot changed copied database")
	}
	db, err := sqlitestore.OpenReadOnly(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var persistedCode string
	if err := db.QueryRow(`SELECT last_error_code FROM job_email_connections WHERE connection_id='primary'`).Scan(&persistedCode); err != nil || persistedCode != "" {
		t.Fatal("derived reconnect warning overwrote saved test result")
	}
}

func TestClonedSnapshotNeverTouchesOriginalCredential(t *testing.T) {
	base := t.TempDir()
	a, b, moved := filepath.Join(base, "original"), filepath.Join(base, "copy"), filepath.Join(base, "moved")
	run := func(path, mode string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestClonedSnapshotHelper$")
		cmd.Env = append(os.Environ(), "CAREERHUB_CLONE_SNAPSHOT_ROOT="+path, "CAREERHUB_CLONE_SNAPSHOT_MODE="+mode)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("clone snapshot child failed: %.1500s", out)
		}
	}
	run(a, "create")
	// Both roots retain the same real marker and database pointer, not a
	// hand-constructed mismatch. All files are this test's synthetic fixtures.
	if err := os.CopyFS(b, os.DirFS(a)); err != nil {
		t.Fatal(err)
	}
	run(b, "copy")
	run(a, "original")
	if err := os.Rename(b, moved); err != nil {
		t.Fatal(err)
	}
	run(moved, "copy")
	run(a, "original")
}
