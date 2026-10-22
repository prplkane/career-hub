//go:build windows

package emailconnection

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/runtimepaths"
)

// The parent copies the actual marker/database/journal after the source process
// exits. The backend is synthetic, shared-target presence is reconstructed, and
// every I/O target is audited: no test touches the user's credential manager.
func TestReconnectCopyHelper(t *testing.T) {
	path := os.Getenv("CAREERHUB_RECONNECT_COPY_HOME")
	if path == "" {
		return
	}
	mode, kind := os.Getenv("CAREERHUB_RECONNECT_COPY_MODE"), os.Getenv("CAREERHUB_RECONNECT_COPY_KIND")
	ctx := context.Background()
	backend := &promotionSecrets{fakeSecrets: &fakeSecrets{}}
	if mode == "seed" {
		home, problem := datahome.BootstrapForProcess(ctx, path, "dev")
		if problem != nil {
			t.Fatal(problem)
		}
		if _, err := schema.PrepareOwned(ctx, path); err != nil {
			t.Fatal(err)
		}
		hooks := configureHooks{}
		if kind == "pending-after" {
			hooks.beforeIntentRemove = func() *Error { return failure("EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED") }
		}
		result, p := configureNewWithHooks(ctx, path, backend, validNewInput(), hooks)
		if !result.Committed || (kind != "pending-after" && p != nil) || (kind == "pending-after" && (p == nil || !result.RecoveryRequired)) {
			t.Fatalf("seed failed: %+v %v", result, p)
		}
		if kind == "pending-before" {
			r, p := rotateWithHooks(ctx, path, backend, RotateInput{
				ExpectedHome: home, AccountAddress: "alerts@example.test", Password: "qrstuvwxyzabcdef",
				JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 1,
			}, rotationHooks{beforeCommit: func() *Error { return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED") }})
			if p == nil || r.Committed || !r.RecoveryRequired {
				t.Fatalf("pending precommit seed: %+v %v", r, p)
			}
		}
		if kind == "v2" {
			name, err := emailcredentials.NewGenerationName(*home)
			if err != nil {
				t.Fatal(err)
			}
			db := settingsDatabase(t, path, false)
			if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, name); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	root, e := runtimepaths.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e)
	}
	home, admission := datahome.InspectOwnedForEmailCredentialRecovery(root, nil)
	if admission != nil || home == nil {
		t.Fatal(admission)
	}
	before, _, _, _, _, _, _ := settingsState(t, path)
	oldTarget := "CareerHub/dev/provider/" + before.SecretName
	backend.values = map[string][]byte{oldTarget: []byte("SYNTHETIC-ORIGINAL")}
	input := RotateInput{ExpectedHome: home, AccountAddress: "alerts@example.test", Password: "qrstuvwxyzabcdef",
		JobSenders: []string{"jobs@example.test"}, ExpectedRevision: before.Revision}
	dbFile := filepath.Join(path, filepath.FromSlash(databasePath))
	originalDB, err := os.ReadFile(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	raw, pending, e := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if e != nil {
		t.Fatal(e)
	}
	assertNoEffects := func() {
		t.Helper()
		if len(backend.reads)+len(backend.writes)+len(backend.deletes) != 0 {
			t.Fatal("refusal reached credential backend")
		}
		after, err := os.ReadFile(dbFile)
		if err != nil || !bytes.Equal(originalDB, after) {
			t.Fatal("refusal changed copied database")
		}
		retained, exists, e := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
		if e != nil || exists != pending || !bytes.Equal(raw, retained) {
			t.Fatal("refusal changed recovery journal")
		}
	}
	// Ordinary controls must not silently adopt the copied/unbound pointer.
	_, p := Rotate(ctx, path, backend, input)
	if p == nil {
		t.Fatal("ordinary rotation admitted copied/unbound target")
	}
	_, p = Disconnect(ctx, path, backend, before.Revision)
	if p == nil {
		t.Fatal("disconnect admitted copied/unbound target")
	}
	_, p = Forget(ctx, path, backend, before.Revision)
	if p == nil {
		t.Fatal("forget admitted copied/unbound target")
	}
	_, p = EditSettings(ctx, path, backend, EditSettingsInput{AccountAddress: input.AccountAddress, JobSenders: input.JobSenders, ExpectedRevision: before.Revision})
	if p == nil {
		t.Fatal("settings admitted copied/unbound target")
	}
	_, p = ConfigureNew(ctx, path, backend, validNewInput())
	if p == nil {
		t.Fatal("configure overwrote copied row")
	}
	assertNoEffects()
	if strings.HasPrefix(kind, "pending-") {
		_, p = Recover(ctx, path, backend)
		if p == nil || p.Code != "EMAIL_CREDENTIAL_INTENT_SCOPE_CHANGED" {
			t.Fatalf("copied journal recovery: %v", p)
		}
		_, p = Reconnect(ctx, path, backend, PromoteLegacyInput(input))
		if p == nil {
			t.Fatal("reconnect bypassed copied recovery journal")
		}
		assertNoEffects()
		return
	}
	hooks := rotationHooks{}
	switch mode {
	case "success":
	case "rollback":
		hooks.beforeCommit = func() *Error { return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED") }
	case "unknown-commit":
		hooks.commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("synthetic post-commit failure")
		}
	case "cleanup":
		hooks.beforeIntentRemove = func() *Error { return failure("EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED") }
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	var result PromoteLegacyResult
	if mode == "success" {
		result, p = Reconnect(ctx, path, backend, PromoteLegacyInput(input))
	} else {
		r, err := rotateWithOperation(ctx, path, backend, input, hooks, "reconnect")
		result, p = nonDeletingResult(r), err
	}
	if mode == "success" {
		if p != nil || !result.Committed || !result.IntentRemoved || result.RecoveryRequired {
			t.Fatalf("reconnect: %+v %v", result, p)
		}
	} else {
		if p == nil || !result.RecoveryRequired || result.IntentRemoved {
			t.Fatalf("missing recovery state: %+v %v", result, p)
		}
		raw, exists, e := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
		if e != nil || !exists {
			t.Fatal("missing reconnect journal")
		}
		intent, err := testDecodeIntent(t, path, raw, *home)
		wantOperation := emailcredentials.RebindCopied
		if kind == "v2" {
			wantOperation = emailcredentials.AdoptUnbound
		}
		if err != nil || intent.Operation != wantOperation || intent.Before != before {
			t.Fatalf("wrong reconnect journal: %v", err)
		}
		settled, p := Recover(ctx, path, backend)
		wantSide, wantCleanup := emailcredentials.AfterCommit, emailcredentials.CleanupNone
		if mode == "rollback" {
			wantSide, wantCleanup = emailcredentials.BeforeCommit, emailcredentials.CleanupNew
		}
		if p != nil || !settled.IntentRemoved || settled.DatabaseSide != wantSide || settled.Cleanup != wantCleanup {
			t.Fatalf("reconnect recovery: %+v %v", settled, p)
		}
	}
	for _, targets := range [][]string{backend.reads, backend.writes, backend.deletes} {
		for _, target := range targets {
			if target == oldTarget {
				t.Fatal("reconnect or recovery touched original credential")
			}
		}
	}
	if string(backend.values[oldTarget]) != "SYNTHETIC-ORIGINAL" {
		t.Fatal("original credential changed")
	}
	after, _, _, _, _, _, _ := settingsState(t, path)
	if mode == "rollback" {
		if after != before || len(backend.values) != 1 {
			t.Fatal("rollback did not retain original row and clean new credential")
		}
		return
	}
	if after.Revision != before.Revision+1 || after.SecretName == before.SecretName {
		t.Fatal("reconnect failed row switch")
	}
	if err := emailcredentials.ValidateScopedTarget(*home, testScope(t, path), after.SecretName); err != nil {
		t.Fatal(err)
	}
	snapshot, p := ReadOwnedSnapshot(ctx, path, *home, backend)
	if p != nil || !snapshot.CredentialPresent {
		t.Fatalf("reconnected copy cannot use its own key: %v", p)
	}
	backend.reads, backend.writes, backend.deletes = nil, nil, nil
	input.ExpectedRevision = after.Revision
	_, p = Reconnect(ctx, path, backend, PromoteLegacyInput(input))
	if p == nil || p.Code != "EMAIL_CREDENTIAL_RECONNECT_NOT_REQUIRED" || len(backend.reads)+len(backend.writes)+len(backend.deletes) != 0 {
		t.Fatalf("repeated reconnect was not inert: %v", p)
	}
}

func TestReconnectCopiedHomeAndUnboundRecovery(t *testing.T) {
	run := func(t *testing.T, path, kind, mode string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReconnectCopyHelper$")
		cmd.Env = append(os.Environ(), "CAREERHUB_RECONNECT_COPY_HOME="+path, "CAREERHUB_RECONNECT_COPY_KIND="+kind, "CAREERHUB_RECONNECT_COPY_MODE="+mode)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s/%s: %v %.2000s", kind, mode, err, out)
		}
	}
	for _, kind := range []string{"v2", "v3", "pending-before", "pending-after"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "source")
			run(t, source, kind, "seed")
			original, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(databasePath)))
			if err != nil {
				t.Fatal(err)
			}
			modes := []string{"success", "rollback", "unknown-commit", "cleanup"}
			if strings.HasPrefix(kind, "pending-") {
				modes = []string{"refuse"}
			}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					copyPath := filepath.Join(base, "copy-"+strings.ReplaceAll(mode, "-", "_"))
					if err := os.CopyFS(copyPath, os.DirFS(source)); err != nil {
						t.Fatal(err)
					}
					run(t, copyPath, kind, mode)
				})
			}
			after, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(databasePath)))
			if err != nil || !bytes.Equal(original, after) {
				t.Fatal("copy operations changed original database")
			}
		})
	}
}
