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

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestConfigureRechecksAfterCommitAndRemovalHooks(t *testing.T) {
	mode := os.Getenv("CAREERHUB_CONFIGURE_DRIFT_MODE")
	if mode == "" {
		for _, mode := range []string{"precommit", "preremove"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConfigureRechecksAfterCommitAndRemovalHooks$")
				cmd.Env = append(os.Environ(), "CAREERHUB_CONFIGURE_DRIFT_MODE="+mode, "CAREERHUB_CONFIGURE_DRIFT_HOME="+filepath.Join(t.TempDir(), "home"))
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("configure drift %s: %v %.1500s", mode, err, out)
				}
			})
		}
		return
	}
	ctx, path := context.Background(), os.Getenv("CAREERHUB_CONFIGURE_DRIFT_HOME")
	if _, e := datahome.BootstrapForProcess(ctx, path, "dev"); e != nil {
		t.Fatal(e)
	}
	if _, err := schema.PrepareOwned(ctx, path); err != nil {
		t.Fatal(err)
	}
	root, e := runtimepaths.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	changed := []byte("synthetic changed journal")
	drift := func() *Error {
		raw, exists, e := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
		if e != nil || !exists {
			t.Fatal("journal missing before drift")
		}
		if replaced, e := root.ReplaceFile(datahome.EmailCredentialRecoveryPath, raw, changed); e != nil || !replaced {
			t.Fatal("cannot inject journal replacement")
		}
		return nil
	}
	hooks := configureHooks{}
	if mode == "precommit" {
		hooks.beforeCommit = drift
	} else {
		hooks.beforeIntentRemove = drift
	}
	backend := &promotionSecrets{fakeSecrets: &fakeSecrets{}}
	result, p := configureNewWithHooks(ctx, path, backend, validNewInput(), hooks)
	committed := mode == "preremove"
	if p == nil || p.Code != "EMAIL_CREDENTIAL_INTENT_CHANGED" || result.Committed != committed || result.CommitAttempted != committed || !result.RecoveryRequired || result.IntentRemoved {
		t.Fatalf("drift result: %+v %v", result, p)
	}
	witness, _, _, _, _ := readConfigureRow(t, path)
	if witness.Present != committed {
		t.Fatal("commit side mismatch")
	}
	raw, exists, e := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if e != nil || !exists || !bytes.Equal(raw, changed) {
		t.Fatal("changed journal was discarded")
	}
	if len(backend.values) != 1 || len(backend.deletes) != 0 {
		t.Fatal("unknown state triggered implicit credential cleanup")
	}
}

type driftingSecrets struct {
	*promotionSecrets
	after func()
}

func (b *driftingSecrets) Read(target string) ([]byte, bool, error) {
	value, found, err := b.promotionSecrets.Read(target)
	b.after()
	return value, found, err
}
func (b *driftingSecrets) Write(target string, value []byte) error {
	err := b.promotionSecrets.Write(target, value)
	b.after()
	return err
}
func (b *driftingSecrets) Delete(target string) (bool, error) {
	deleted, err := b.promotionSecrets.Delete(target)
	b.after()
	return deleted, err
}

func TestBoundSecretsEffectGuards(t *testing.T) {
	mode := os.Getenv("CAREERHUB_BOUND_SECRETS_MODE")
	if mode == "" {
		for _, mode := range []string{"read", "write", "delete", "pre-canceled", "unbound"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBoundSecretsEffectGuards$")
				cmd.Env = append(os.Environ(), "CAREERHUB_BOUND_SECRETS_MODE="+mode, "CAREERHUB_BOUND_SECRETS_HOME="+filepath.Join(t.TempDir(), "home"))
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("bound secret %s: %v %.1500s", mode, err, out)
				}
			})
		}
		return
	}
	path := os.Getenv("CAREERHUB_BOUND_SECRETS_HOME")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	home, admission := datahome.BootstrapForProcess(ctx, path, "dev")
	if admission != nil {
		t.Fatal(admission)
	}
	root, e := runtimepaths.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	name, err := testGenerationName(t, path, *home)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "unbound" {
		name, err = emailcredentials.NewGenerationName(*home)
		if err != nil {
			t.Fatal(err)
		}
	}
	target := "CareerHub/dev/provider/" + name
	backend := &driftingSecrets{promotionSecrets: &promotionSecrets{fakeSecrets: &fakeSecrets{values: map[string][]byte{target: []byte("SYNTHETIC-SECRET")}}}}
	backend.after = func() {
		if created, e := root.CreateFile(datahome.EmailCredentialRecoveryPath, []byte("synthetic journal drift")); e != nil || !created {
			t.Fatal("cannot inject journal drift")
		}
	}
	bound, err := newBoundSecrets(ctx, root, *home, backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "pre-canceled" {
		cancel()
	}
	value, found, deleted := "", false, false
	switch mode {
	case "read", "pre-canceled", "unbound":
		value, found, err = bound.Get(name)
	case "write":
		err = bound.Set(name, "SYNTHETIC-NEW")
	case "delete":
		deleted, err = bound.Delete(name)
	}
	want := "EMAIL_CREDENTIAL_INTENT_CHANGED"
	if mode == "pre-canceled" {
		want = "EMAIL_CREDENTIAL_OPERATION_CANCELED"
	}
	if mode == "unbound" {
		want = emailcredentials.ErrInvalidTarget.Error()
	}
	if err == nil || credentialStoreProblem(err).Code != want {
		t.Fatalf("wrong guard result: %v", err)
	}
	if value != "" || found {
		t.Fatal("failed postcheck leaked credential value/presence")
	}
	wantCalls := 1
	if mode == "pre-canceled" || mode == "unbound" {
		wantCalls = 0
	}
	if len(backend.reads)+len(backend.writes)+len(backend.deletes) != wantCalls {
		t.Fatal("unexpected backend effects")
	}
	if mode == "delete" && (!deleted || backend.values[target] != nil) {
		t.Fatal("postcheck erased actual deletion fact")
	}
	if mode == "write" && string(backend.values[target]) != "SYNTHETIC-NEW" {
		t.Fatal("write hook did not exercise post-effect failure")
	}
	// The changed/canceled boundary must refuse the next effect entirely.
	if err := bound.Set(name, "must-not-write"); err == nil {
		t.Fatal("changed boundary admitted another write")
	}
	if len(backend.reads)+len(backend.writes)+len(backend.deletes) != wantCalls {
		t.Fatal("failed guard still reached backend")
	}
}
