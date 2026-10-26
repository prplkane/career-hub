//go:build windows

package emailconnection

import (
	"context"
	"errors"
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

const (
	promotionCrashExit  = 79
	recoveryCleanupExit = 80
)

type promotionCrashBackend struct {
	*fakeSecrets
	exitAfterDelete bool
}

func (backend *promotionCrashBackend) Read(target string) ([]byte, bool, error) {
	if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
		return nil, false, errors.New("shared legacy target must not be read")
	}
	return backend.fakeSecrets.Read(target)
}

func (backend *promotionCrashBackend) Delete(target string) (bool, error) {
	if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
		return false, errors.New("shared legacy target must not be deleted")
	}
	deleted, err := backend.fakeSecrets.Delete(target)
	if backend.exitAfterDelete && err == nil {
		os.Exit(recoveryCleanupExit)
	}
	return deleted, err
}

func seedPromotionCrash(t *testing.T, stage, homePath string) {
	t.Helper()
	ctx := context.Background()
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	backend := &fakeSecrets{}
	configured, problem := ConfigureNew(ctx, homePath, backend, validNewInput())
	if problem != nil || !configured.Committed {
		t.Fatalf("seed connection: %+v %v", configured, problem)
	}
	initial, _, _, _, _, _, _ := settingsState(t, homePath)
	db := settingsDatabase(t, homePath, false)
	if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, emailcredentials.LegacyName); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := secretstore.New(home.Channel, backend)
	if err != nil || store.Set(emailcredentials.LegacyName, "SYNTHETIC-SHARED") != nil {
		t.Fatal("shared legacy fixture unavailable")
	}
	if _, err := store.Delete(initial.SecretName); err != nil {
		t.Fatal(err)
	}
	hooks := rotationHooks{}
	exitNow := func() *Error { os.Exit(promotionCrashExit); return nil }
	switch stage {
	case "before-intent":
		hooks.generateName = func(datahome.Info) (string, error) {
			os.Exit(promotionCrashExit)
			return "", nil
		}
	case "after-intent":
		hooks.afterPublication = exitNow
	case "after-new-write":
		hooks.afterNewWrite = exitNow
	case "before-sql-commit":
		hooks.beforeCommit = exitNow
	case "after-sql-commit":
		hooks.beforeIntentRemove = exitNow
	default:
		t.Fatalf("unknown crash stage %q", stage)
	}
	input := PromoteLegacyInput{
		ExpectedHome: home, AccountAddress: "alerts@example.test",
		Password: "qrstuvwxyzabcdef", JobSenders: []string{"jobs@example.test"},
		ExpectedRevision: 1,
	}
	if result, problem := promoteLegacyWithHooks(ctx, homePath, &promotionCrashBackend{fakeSecrets: backend}, input, hooks); problem != nil || result.Committed {
		t.Fatalf("crash hook was not reached: %+v %v", result, problem)
	}
	// Every test stage must terminate the child at its named native boundary.
	t.Fatal("promotion returned instead of exiting")
}

func recoverPromotionCrash(t *testing.T, stage, phase, homePath string) {
	t.Helper()
	ctx := context.Background()
	_, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	before := emailcredentials.RowWitness{Present: true, Revision: 1, Enabled: true, SecretName: emailcredentials.LegacyName}
	if stage == "before-intent" {
		if startup != nil {
			t.Fatalf("prepublication exit left startup blocked: %v", startup)
		}
		assertWitness(t, homePath, before)
		return
	}
	if startup == nil || startup.Code != "DATA_HOME_RECOVERY_REQUIRED" {
		t.Fatalf("pending promotion did not block ordinary startup: %v", startup)
	}
	root, pathProblem := runtimepaths.Open(homePath)
	if pathProblem != nil {
		t.Fatal(pathProblem.Code)
	}
	defer root.Close()
	home, admission := datahome.InspectOwnedForEmailCredentialRecovery(root, nil)
	if admission != nil || home == nil {
		t.Fatalf("explicit recovery admission: %v", admission)
	}
	raw, present, pathProblem := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if pathProblem != nil || !present {
		t.Fatal("durable promotion intent missing")
	}
	for _, private := range []string{"qrstuvwxyzabcdef", "alerts@example.test", "jobs@example.test", "SYNTHETIC-SHARED"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("promotion intent contains private fixture data")
		}
	}
	intent, err := testDecodeIntent(t, homePath, raw, *home)
	if err != nil || intent.Operation != emailcredentials.AdoptUnbound || intent.Before != before {
		t.Fatalf("invalid promotion intent: %v", err)
	}
	newPresent := stage != "after-intent" && phase != "recover-after-delete"
	backend := &promotionCrashBackend{fakeSecrets: &fakeSecrets{values: map[string][]byte{
		"CareerHub/dev/provider/" + emailcredentials.LegacyName: []byte("SYNTHETIC-SHARED"),
	}}}
	if newPresent {
		backend.values["CareerHub/dev/provider/"+intent.After.SecretName] = []byte("SYNTHETIC-NEW")
	}
	if phase == "recover-delete-crash" {
		if stage != "after-new-write" {
			t.Fatal("delete crash requires a precommit orphan")
		}
		backend.exitAfterDelete = true
		Recover(ctx, homePath, backend)
		t.Fatal("recovery did not exit after deleting new target")
	}
	result, problem := Recover(ctx, homePath, backend)
	wantSide, wantCleanup := emailcredentials.BeforeCommit, emailcredentials.CleanupNone
	if stage == "before-sql-commit" {
		wantCleanup = emailcredentials.CleanupNew
	}
	if stage == "after-sql-commit" {
		wantSide = emailcredentials.AfterCommit
	}
	if problem != nil || !result.IntentRemoved || result.DatabaseSide != wantSide || result.Cleanup != wantCleanup {
		t.Fatalf("crash recovery: %+v %v; want %d/%d", result, problem, wantSide, wantCleanup)
	}
	for _, target := range backend.reads {
		if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
			t.Fatal("recovery read shared v1")
		}
	}
	store, err := secretstore.New(home.Channel, backend.fakeSecrets)
	if err != nil {
		t.Fatal(err)
	}
	assertSecret(t, store, emailcredentials.LegacyName, true)
	assertSecret(t, store, intent.After.SecretName, stage == "after-sql-commit")
	if wantSide == emailcredentials.BeforeCommit {
		assertWitness(t, homePath, intent.Before)
	} else {
		assertWitness(t, homePath, intent.After)
	}
	if admitted, problem := datahome.Inspect(homePath, nil); problem != nil || admitted == nil {
		t.Fatalf("settled Home not admitted: %v", problem)
	}
}

func TestPromoteLegacyCrashMatrix(t *testing.T) {
	stage := os.Getenv("CAREERHUB_EMAIL_PROMOTION_CRASH_STAGE")
	phase := os.Getenv("CAREERHUB_EMAIL_PROMOTION_CRASH_PHASE")
	homePath := os.Getenv("CAREERHUB_EMAIL_PROMOTION_CRASH_HOME")
	if stage != "" {
		if phase == "seed" {
			seedPromotionCrash(t, stage, homePath)
		} else {
			recoverPromotionCrash(t, stage, phase, homePath)
		}
		return
	}
	for _, stage := range []string{"before-intent", "after-intent", "after-new-write", "before-sql-commit", "after-sql-commit"} {
		t.Run(stage, func(t *testing.T) {
			homePath := filepath.Join(t.TempDir(), "home")
			phases := []string{"seed", "recover"}
			if stage == "after-new-write" {
				phases = []string{"seed", "recover-delete-crash", "recover-after-delete"}
			}
			for _, phase := range phases {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPromoteLegacyCrashMatrix$")
				command.Env = append(os.Environ(), "CAREERHUB_EMAIL_PROMOTION_CRASH_STAGE="+stage,
					"CAREERHUB_EMAIL_PROMOTION_CRASH_PHASE="+phase,
					"CAREERHUB_EMAIL_PROMOTION_CRASH_HOME="+homePath)
				output, err := command.CombinedOutput()
				cancel()
				wantExit := 0
				if phase == "seed" {
					wantExit = promotionCrashExit
				} else if phase == "recover-delete-crash" {
					wantExit = recoveryCleanupExit
				}
				gotExit := 0
				if err != nil {
					var exitError *exec.ExitError
					if errors.As(err, &exitError) {
						gotExit = exitError.ExitCode()
					} else {
						gotExit = -1
					}
				}
				if gotExit != wantExit {
					t.Fatalf("%s %s exit=%d want=%d: %v %.1200s", stage, phase, gotExit, wantExit, err, output)
				}
			}
		})
	}
}
