//go:build windows

package emailconnection

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
)

const nativeClonePassword = "qrstuvwxyzabcdef" // Fictional fixture, never a Gmail password.

// Only the parent's pre-registered, absent test target can reach Windows. In a
// copied Home this excludes the original target, including any attempted read.
type nativeCloneBackend struct {
	secretstore.Backend
	target          string
	calls, rejected int
	deleteThenError bool
}

func (b *nativeCloneBackend) admit(target string) error {
	if target != b.target {
		b.rejected++
		return errors.New("native fixture refuses non-test target")
	}
	b.calls++
	return nil
}
func (b *nativeCloneBackend) Read(target string) ([]byte, bool, error) {
	if err := b.admit(target); err != nil {
		return nil, false, err
	}
	return b.Backend.Read(target)
}
func (b *nativeCloneBackend) Write(target string, value []byte) error {
	if err := b.admit(target); err != nil {
		return err
	}
	if !bytes.Equal(value, []byte(nativeClonePassword)) {
		return errors.New("native fixture refuses unexpected value")
	}
	return b.Backend.Write(target, value)
}
func (b *nativeCloneBackend) Delete(target string) (bool, error) {
	if err := b.admit(target); err != nil {
		return false, err
	}
	deleted, err := b.Backend.Delete(target)
	if err == nil && b.deleteThenError {
		return deleted, errors.New("synthetic post-delete failure")
	}
	return deleted, err
}

func TestNativeCloneCredentialHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_TEST_NATIVE_EMAIL_CREDENTIALS") != "1" {
		t.Skip("native credential opt-in required")
	}
	path, mode := os.Getenv("CAREERHUB_NATIVE_CLONE_HOME"), os.Getenv("CAREERHUB_NATIVE_CLONE_MODE")
	if path == "" || mode == "" {
		return
	}
	ctx := context.Background()
	if mode == "prepare-source" {
		if _, p := datahome.BootstrapForProcess(ctx, path, "dev"); p != nil {
			t.Fatal(p)
		}
		if _, err := schema.PrepareOwned(ctx, path); err != nil {
			t.Fatal(err)
		}
		if _, p := ConfigureNew(ctx, path, &fakeSecrets{}, validNewInput()); p != nil {
			t.Fatal(p)
		}
		row, _, _, _, _, _, _ := settingsState(t, path)
		fmt.Println("CH_TARGET=" + row.SecretName)
		return // No native credentials written by preparation.
	}
	root, e := runtimepaths.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e)
	}
	home, p := datahome.InspectOwnedForEmailCredentialRecovery(root, nil)
	if p != nil || home == nil {
		t.Fatal(p)
	}
	if mode == "prepare-copy" {
		name, err := testGenerationName(t, path, *home)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("CH_TARGET=" + name)
		return
	}
	name := os.Getenv("CAREERHUB_NATIVE_CLONE_TARGET")
	if err := emailcredentials.ValidateScopedTarget(*home, testScope(t, path), name); err != nil {
		t.Fatal(err)
	}
	native, err := secretstore.NewWindowsBackend()
	if err != nil {
		t.Fatal("native test backend unavailable")
	}
	backend := &nativeCloneBackend{Backend: native, target: "CareerHub/dev/provider/" + name}
	defer func() {
		if backend.rejected != 0 {
			t.Fatal("owner attempted non-test/original credential I/O")
		}
	}()
	if strings.HasPrefix(mode, "crash-") {
		snapshot, problem := ReadOwnedSnapshot(ctx, path, *home, backend)
		if problem != nil || snapshot.CredentialPresent || snapshot.LastErrorCode != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" || backend.calls != 0 || backend.rejected != 0 {
			t.Fatal("copied Home probed the original key")
		}
		hooks := rotationHooks{generateName: func(datahome.Info) (string, error) { return name, nil }}
		if mode == "crash-before" {
			hooks.afterNewWrite = func() *Error { os.Exit(71); return nil }
		} else if mode == "crash-after" {
			hooks.commit = func(tx *sql.Tx) error {
				if err := tx.Commit(); err != nil {
					return err
				}
				os.Exit(72)
				return nil
			}
		} else {
			t.Fatal("unknown crash phase")
		}
		_, problem = rotateWithOperation(ctx, path, backend, RotateInput{
			ExpectedHome: home, AccountAddress: "alerts@example.test", Password: nativeClonePassword,
			JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 1,
		}, hooks, "reconnect")
		t.Fatalf("crash hook was not reached: %v", problem)
	}
	if strings.HasPrefix(mode, "recover-") {
		backend.deleteThenError = mode == "recover-uncertain"
		result, problem := Recover(ctx, path, backend)
		if mode == "recover-uncertain" {
			if problem == nil || problem.Code != "EMAIL_SECRET_STORE_FAILED" || !result.CleanupAttempted || result.IntentRemoved {
				t.Fatalf("uncertain cleanup: %+v %v", result, problem)
			}
			return
		}
		wantSide := emailcredentials.BeforeCommit
		if mode == "recover-after" {
			wantSide = emailcredentials.AfterCommit
		}
		if problem != nil || !result.IntentRemoved || result.DatabaseSide != wantSide {
			t.Fatalf("restart recovery: %+v %v", result, problem)
		}
		again, problem := Recover(ctx, path, backend)
		if problem != nil || again != (Result{}) {
			t.Fatalf("second recovery not idle: %+v %v", again, problem)
		}
		if wantSide == emailcredentials.BeforeCommit {
			return
		}
	} else if mode != "verify" {
		t.Fatal("unknown native test phase")
	}
	snapshot, problem := ReadOwnedSnapshot(ctx, path, *home, backend)
	if problem != nil || !snapshot.CredentialPresent || snapshot.LastErrorCode != "" {
		t.Fatalf("owner cannot use its own native key: %v", problem)
	}
}

func TestNativeCopiedHomeCredentialsAndRestart(t *testing.T) {
	if testing.Short() || os.Getenv("CAREERHUB_TEST_NATIVE_EMAIL_CREDENTIALS") != "1" {
		t.Skip("native credential opt-in required")
	}
	native, err := secretstore.NewWindowsBackend()
	if err != nil {
		t.Fatal("native test backend unavailable")
	}
	store, err := secretstore.New("dev", native)
	if err != nil {
		t.Fatal(err)
	}
	owned := map[string]bool{}
	t.Cleanup(func() {
		for name := range owned {
			value, found, err := store.Get(name)
			if err != nil || (found && value != nativeClonePassword) {
				t.Errorf("test key changed/unreadable; refusing cleanup of %s", name)
				continue
			}
			if found {
				if _, err := store.Delete(name); err != nil {
					t.Errorf("test key cleanup failed: %v", err)
					continue
				}
			}
			if _, found, err := store.Get(name); err != nil || found {
				t.Errorf("test key cleanup not confirmed: %s", name)
			}
		}
	})
	run := func(t *testing.T, path, mode, target string, wantExit int) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeCloneCredentialHelper$")
		cmd.Env = append(os.Environ(), "CAREERHUB_NATIVE_CLONE_HOME="+path, "CAREERHUB_NATIVE_CLONE_MODE="+mode, "CAREERHUB_NATIVE_CLONE_TARGET="+target)
		out, err := cmd.CombinedOutput()
		if wantExit == 0 {
			if err != nil {
				t.Fatalf("native clone %s: %v %.1800s", mode, err, out)
			}
		} else {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != wantExit {
				t.Fatalf("expected test crash %d: %v %.1800s", wantExit, err, out)
			}
		}
		if !strings.HasPrefix(mode, "prepare-") {
			return ""
		}
		var name string
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "CH_TARGET=") {
				if name != "" {
					t.Fatal("duplicate generated target")
				}
				name = strings.TrimSpace(strings.TrimPrefix(line, "CH_TARGET="))
			}
		}
		if !strings.HasPrefix(name, "email.v3.") || len(name) != 75 || owned[name] {
			t.Fatal("invalid/reused generated test target")
		}
		if _, found, err := store.Get(name); err != nil || found {
			t.Fatal("test target absence not established; refusing overwrite")
		}
		owned[name] = true // Cleanup authority recorded BEFORE any native write.
		return name
	}
	assertValue := func(name string, present bool) {
		t.Helper()
		value, found, err := store.Get(name)
		if err != nil || found != present || (found && value != nativeClonePassword) {
			t.Fatal("unexpected native test key state")
		}
	}
	base := t.TempDir()
	source := filepath.Join(base, "source")
	original := run(t, source, "prepare-source", "", 0)
	if err := store.Set(original, nativeClonePassword); err != nil {
		t.Fatal("cannot write pre-registered test key")
	}
	for _, side := range []string{"before", "after"} {
		t.Run(side, func(t *testing.T) {
			copyPath := filepath.Join(base, "copy-"+side)
			if err := os.CopyFS(copyPath, os.DirFS(source)); err != nil {
				t.Fatal(err)
			}
			name := run(t, copyPath, "prepare-copy", "", 0)
			exit := 71
			if side == "after" {
				exit = 72
			}
			run(t, copyPath, "crash-"+side, name, exit)
			assertValue(original, true)
			assertValue(name, true)
			if side == "before" {
				run(t, copyPath, "recover-uncertain", name, 0)
				assertValue(name, false)
			}
			run(t, copyPath, "recover-"+side, name, 0)
			assertValue(name, side == "after")
			assertValue(original, true)
			run(t, source, "verify", original, 0)
		})
	}
}
