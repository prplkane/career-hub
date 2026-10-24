//go:build windows

package emailconnection

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/secretstore"
)

// This opt-in test touches only fresh, generated v3 targets in the real Windows
// Generic Credential store. It never enumerates credentials or opens Gmail.
func TestNativeGmailCredentialLifecycle(t *testing.T) {
	if testing.Short() || os.Getenv("CAREERHUB_TEST_NATIVE_EMAIL_CREDENTIALS") != "1" {
		t.Skip("set CAREERHUB_TEST_NATIVE_EMAIL_CREDENTIALS=1 for isolated Windows credential test")
	}
	if os.Getenv("CAREERHUB_NATIVE_EMAIL_CHILD") == "1" {
		runNativeGmailCredentialLifecycle(t, os.Getenv("CAREERHUB_NATIVE_EMAIL_HOME"))
		return
	}
	homePath := filepath.Join(t.TempDir(), "home")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeGmailCredentialLifecycle$", "-test.v")
	command.Env = append(os.Environ(), "CAREERHUB_NATIVE_EMAIL_CHILD=1", "CAREERHUB_NATIVE_EMAIL_HOME="+homePath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated native credential child failed: %v %.2000s", err, output)
	}
	t.Logf("isolated native credential child: %.2000s", output)
}

func runNativeGmailCredentialLifecycle(t *testing.T, homePath string) {
	t.Helper()
	ctx := context.Background()
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatalf("synthetic Home startup: %s", startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	backend, err := secretstore.NewWindowsBackend()
	if err != nil {
		t.Fatal("Windows Credential Manager unavailable for enabled test")
	}
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		t.Fatal(err)
	}
	owned := map[string]string{}
	t.Cleanup(func() {
		for name, expected := range owned {
			value, found, err := store.Get(name)
			if err != nil {
				t.Errorf("test-owned target read failed during cleanup: %v", err)
				continue
			}
			if found {
				if value != expected {
					t.Errorf("test-owned target changed unexpectedly; refusing cleanup of %s", name)
					continue
				}
				if _, err := store.Delete(name); err != nil {
					t.Errorf("exact test-owned target cleanup failed: %v", err)
					continue
				}
			}
			if _, found, err := store.Get(name); err != nil || found {
				t.Errorf("exact test-owned target cleanup unconfirmed: %v", err)
			}
		}
	})
	newName := func(value string) string {
		for attempt := 0; attempt < 8; attempt++ {
			name, err := testGenerationName(t, homePath, *home)
			if err != nil {
				t.Fatal("test target generation failed")
			}
			if _, alreadyOwned := owned[name]; alreadyOwned {
				continue
			}
			_, found, err := store.Get(name)
			if err != nil {
				t.Fatal("test target absence could not be established")
			}
			if found {
				continue // Never overwrite an existing credential.
			}
			owned[name] = value // Record exact cleanup authority before first write.
			fmt.Fprintf(os.Stderr, "CH-120 test-owned target: CareerHub/%s/provider/%s\n", home.Channel, name)
			return name
		}
		t.Fatal("no absent test credential target available")
		return ""
	}
	assertValue := func(name, want string, present bool) {
		t.Helper()
		value, found, err := store.Get(name)
		if err != nil || found != present || present && value != want {
			t.Fatalf("native credential state mismatch for test-owned target: present=%t want=%t error=%v", found, present, err)
		}
	}

	initial := validNewInput()
	firstPassword := normalizeConnectionPassword(initial.Password)
	first := newName(firstPassword)
	configured, problem := configureNewWithHooks(ctx, homePath, backend, initial, configureHooks{
		generateName: func(datahome.Info) (string, error) { return first, nil },
	})
	if problem != nil || !configured.Committed || !configured.IntentRemoved {
		t.Fatalf("native initial configure: %+v %v", configured, problem)
	}
	assertValue(first, firstPassword, true)
	settingsSeedHistory(t, homePath, `{}`)

	secondRaw := "qrst uvwx yzab cdef"
	secondPassword := normalizeConnectionPassword(secondRaw)
	second := newName(secondPassword)
	rotated, problem := rotateWithHooks(ctx, homePath, backend, RotateInput{
		AccountAddress: "alerts@example.test", Password: secondRaw,
		JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 1,
	}, rotationHooks{generateName: func(datahome.Info) (string, error) { return second, nil }})
	if problem != nil || !rotated.Committed || !rotated.OldDeleteConfirmed || !rotated.IntentRemoved {
		t.Fatalf("native rotation: %+v %v", rotated, problem)
	}
	assertValue(first, firstPassword, false)
	assertValue(second, secondPassword, true)

	disconnected, problem := Disconnect(ctx, homePath, backend, 2)
	if problem != nil || !disconnected.Committed || !disconnected.OldDeleteConfirmed || !disconnected.IntentRemoved {
		t.Fatalf("native disconnect: %+v %v", disconnected, problem)
	}
	assertValue(second, secondPassword, false)
	witness, _, scheduled, _, cursor, receipts, _ := settingsState(t, homePath)
	if witness != (emailcredentials.RowWitness{Present: true, Revision: 3, Enabled: false, SecretName: second}) ||
		scheduled != 0 || cursor != 12 || receipts != 1 {
		t.Fatalf("native disconnect changed saved history: %+v %d %d %d", witness, scheduled, cursor, receipts)
	}
	forgotten, problem := Forget(ctx, homePath, backend, 3)
	if problem != nil || !forgotten.Committed || !forgotten.OldDeleteConfirmed || !forgotten.IntentRemoved {
		t.Fatalf("native forget after disconnect: %+v %v", forgotten, problem)
	}
	assertWitness(t, homePath, emailcredentials.RowWitness{})

	third := newName(firstPassword)
	configured, problem = configureNewWithHooks(ctx, homePath, backend, initial, configureHooks{
		generateName: func(datahome.Info) (string, error) { return third, nil },
	})
	if problem != nil || !configured.Committed || !configured.IntentRemoved {
		t.Fatalf("native reconfigure after forget: %+v %v", configured, problem)
	}
	assertValue(third, firstPassword, true)
	forgotten, problem = Forget(ctx, homePath, backend, 1)
	if problem != nil || !forgotten.Committed || !forgotten.OldDeleteConfirmed || !forgotten.IntentRemoved {
		t.Fatalf("native active forget: %+v %v", forgotten, problem)
	}
	assertValue(third, firstPassword, false)
	assertWitness(t, homePath, emailcredentials.RowWitness{})
}
