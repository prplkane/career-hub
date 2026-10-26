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
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

type reportedWriteSecrets struct{ *fakeSecrets }

type publicationGuardSecrets struct {
	*fakeSecrets
	root *runtimepaths.Root
}

func (backend *publicationGuardSecrets) Write(target string, value []byte) error {
	raw, present, pathProblem := backend.root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if pathProblem != nil || !present || bytes.Contains(raw, value) || bytes.Contains(raw, []byte("alerts@example.test")) || bytes.Contains(raw, []byte("jobs@example.test")) {
		return errors.New("synthetic publication ordering or intent-content violation")
	}
	return backend.fakeSecrets.Write(target, value)
}

func (backend *reportedWriteSecrets) Write(target string, value []byte) error {
	_ = backend.fakeSecrets.Write(target, value)
	return errors.New("private backend reported-after-write detail")
}

func readConfigureRow(t *testing.T, home string) (emailcredentials.RowWitness, string, int, int, int) {
	t.Helper()
	db, err := sqlitestore.OpenReadOnly(context.Background(), filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	witness, problem := readWitness(context.Background(), db)
	if problem != nil {
		t.Fatal(problem)
	}
	if !witness.Present {
		return witness, "", 0, 0, 0
	}
	var account string
	var scheduled, interval, stateCount int
	if err := db.QueryRow(`SELECT account_address,schedule_enabled,interval_minutes FROM job_email_connections WHERE connection_id='primary'`).Scan(&account, &scheduled, &interval); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_email_sync_state WHERE connection_id='primary' AND folder='INBOX'`).Scan(&stateCount); err != nil {
		t.Fatal(err)
	}
	return witness, account, scheduled, interval, stateCount
}

func assertConfigureRules(t *testing.T, home string) {
	t.Helper()
	db, err := sqlitestore.OpenReadOnly(context.Background(), filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT sender_address,purpose,subject_policy_json FROM job_email_sender_rules ORDER BY sender_address`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantAddresses := []string{"both@example.test", "jobs@example.test", "track@example.test"}
	wantPurposes := []string{emailrouting.SenderPurposeBoth, emailrouting.SenderPurposeJobAlerts, emailrouting.SenderPurposeApplicationTracking}
	for index := range wantAddresses {
		if !rows.Next() {
			t.Fatal("missing sender rule")
		}
		var address, purpose, policy string
		if err := rows.Scan(&address, &purpose, &policy); err != nil {
			t.Fatal(err)
		}
		if address != wantAddresses[index] || purpose != wantPurposes[index] || policy != emailrouting.EmptyPolicy().CanonicalJSON() {
			t.Fatalf("wrong sender rule %d: %q %q %q", index, address, purpose, policy)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected sender rule")
	}
}

func configureScenario(t *testing.T, mode string) {
	t.Helper()
	ctx, homePath := context.Background(), os.Getenv("CAREERHUB_EMAIL_CONFIGURE_HOME")
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	root, pathProblem := runtimepaths.Open(homePath)
	if pathProblem != nil {
		t.Fatal(pathProblem.Code)
	}
	defer root.Close()
	backend := &fakeSecrets{}
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := testGenerationName(t, homePath, *home)
	if err != nil || store.Set(unrelated, "SYNTHETIC-UNRELATED") != nil {
		t.Fatal("unrelated fixture failed")
	}
	input := validNewInput()
	interval := 45
	input.ScheduleEnabled, input.IntervalMinutes = true, &interval
	hooks := configureHooks{}
	var chosenBackend secretstore.Backend = &publicationGuardSecrets{backend, root}
	wantCode, wantPresent, wantSecret := "", true, true
	switch mode {
	case "success":
	case "invalid":
		input.AccountAddress = "invalid"
		wantCode, wantPresent, wantSecret = "EMAIL_CONNECTION_INVALID", false, false
	case "revision":
		input.ExpectedRevision = 1
		wantCode, wantPresent, wantSecret = "EMAIL_CONNECTION_REVISION_CONFLICT", false, false
	case "reported-write":
		chosenBackend = &reportedWriteSecrets{backend}
		wantCode, wantPresent = "EMAIL_SECRET_STORE_FAILED", false
	case "readback-error":
		hooks.afterSecretWrite = func() *Error { backend.readError = true; return nil }
		wantCode, wantPresent = "EMAIL_SECRET_STORE_FAILED", false
	case "precommit-error":
		hooks.beforeCommit = func() *Error { return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED") }
		wantCode, wantPresent = "EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED", false
	case "committed-but-error":
		hooks.commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("private postcommit detail")
		}
		wantCode = "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED"
	case "cleanup-error":
		hooks.beforeIntentRemove = func() *Error { return failure("EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED") }
		wantCode = "EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED"
	case "collision":
		collision, e := testGenerationName(t, homePath, *home)
		if e != nil || store.Set(collision, "SYNTHETIC-COLLISION") != nil {
			t.Fatal("collision fixture failed")
		}
		next, e := testGenerationName(t, homePath, *home)
		if e != nil || next == collision {
			t.Fatal("second target fixture failed")
		}
		calls := 0
		hooks.generateName = func(datahome.Info) (string, error) {
			calls++
			if calls == 1 {
				return collision, nil
			}
			return next, nil
		}
		defer func() {
			if calls != 2 {
				t.Fatalf("collision attempts %d", calls)
			}
			assertSecret(t, store, collision, true)
		}()
	default:
		t.Fatalf("unknown configure mode %q", mode)
	}
	result, problem := configureNewWithHooks(ctx, homePath, chosenBackend, input, hooks)
	backend.readError = false // The synthetic fault is transient; recovery may observe the target.
	if wantCode == "" {
		if problem != nil || !result.Committed || !result.IntentPublished || !result.IntentRemoved || result.RecoveryRequired || result.Revision != 1 {
			t.Fatalf("configure result %+v %v", result, problem)
		}
	} else if problem == nil || problem.Code != wantCode || strings.Contains(problem.Error(), "SYNTHETIC-") {
		t.Fatalf("configure failure %+v %v; want %s", result, problem, wantCode)
	}
	if wantCode == "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED" && (!result.CommitAttempted || result.Committed || !result.RecoveryRequired) {
		t.Fatalf("unknown commit misreported: %+v", result)
	}
	if mode == "precommit-error" && (result.CommitAttempted || result.Committed || !result.RecoveryRequired) {
		t.Fatalf("known rollback misreported: %+v", result)
	}
	if mode == "cleanup-error" && (!result.Committed || !result.RecoveryRequired) {
		t.Fatalf("known commit cleanup misreported: %+v", result)
	}
	if mode == "invalid" || mode == "revision" {
		if result != (ConfigureResult{}) || len(backend.values) != 1 {
			t.Fatalf("invalid input caused a mutation: %+v", result)
		}
	}
	witness, account, scheduled, gotInterval, stateCount := readConfigureRow(t, homePath)
	if witness.Present != wantPresent {
		t.Fatalf("database side: %+v", witness)
	}
	if wantPresent {
		if !witness.Enabled || witness.Revision != 1 || account != "alerts@example.test" || scheduled != 1 || gotInterval != 45 || stateCount != 1 {
			t.Fatalf("wrong connection state: %+v %q %d %d %d", witness, account, scheduled, gotInterval, stateCount)
		}
		assertConfigureRules(t, homePath)
	}
	if mode != "invalid" && mode != "revision" {
		assertSecret(t, store, witness.SecretName, wantSecret && wantPresent)
	}
	assertSecret(t, store, unrelated, true)
	_, pending, pathProblem := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if pathProblem != nil || pending != (wantCode != "" && mode != "invalid" && mode != "revision") {
		t.Fatalf("intent state %t %v", pending, pathProblem)
	}
	if pending {
		recovered, recoveryProblem := Recover(ctx, homePath, backend)
		if recoveryProblem != nil || !recovered.IntentRemoved {
			t.Fatalf("recovery %+v %v", recovered, recoveryProblem)
		}
		assertSecret(t, store, unrelated, true)
		assertSecret(t, store, witness.SecretName, wantPresent)
		if !wantPresent {
			if len(backend.values) != 1 {
				t.Fatalf("orphan secret not removed: %d", len(backend.values))
			}
		}
	}
}

func TestConfigureNewOwnedSubprocess(t *testing.T) {
	if mode := os.Getenv("CAREERHUB_EMAIL_CONFIGURE_MODE"); mode != "" {
		configureScenario(t, mode)
		return
	}
	for _, mode := range []string{"success", "invalid", "revision", "reported-write", "readback-error", "precommit-error", "committed-but-error", "cleanup-error", "collision"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConfigureNewOwnedSubprocess$")
			command.Env = append(os.Environ(), "CAREERHUB_EMAIL_CONFIGURE_MODE="+mode,
				"CAREERHUB_EMAIL_CONFIGURE_HOME="+filepath.Join(t.TempDir(), "home"))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("synthetic configure %s failed: %v %.1200s", mode, err, output)
			}
		})
	}
}

// The second subprocess must acquire the Home anew while its sentinel still
// blocks ordinary admission. The fake backend reconstructs only presence,
// not a real password; no credential or mailbox is accessed.
func TestConfigureNewRecoveryAfterProcessExit(t *testing.T) {
	phase := os.Getenv("CAREERHUB_EMAIL_CONFIGURE_RESTART_PHASE")
	homePath := os.Getenv("CAREERHUB_EMAIL_CONFIGURE_RESTART_HOME")
	if phase != "" {
		ctx := context.Background()
		home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
		if phase == "seed" {
			if startup != nil || home == nil {
				t.Fatalf("seed startup: %v", startup)
			}
			if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
				t.Fatal(err)
			}
			result, problem := configureNewWithHooks(ctx, homePath, &fakeSecrets{}, validNewInput(), configureHooks{
				afterSecretWrite: func() *Error { return failure("EMAIL_CREDENTIAL_OPERATION_CANCELED") },
			})
			if problem == nil || problem.Code != "EMAIL_CREDENTIAL_OPERATION_CANCELED" || !result.RecoveryRequired || result.CommitAttempted {
				t.Fatalf("seed did not leave a precommit intent: %+v %v", result, problem)
			}
			return
		}
		if phase != "recover" || startup == nil || startup.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatalf("ordinary restart did not stop at sentinel: %v", startup)
		}
		root, pathProblem := runtimepaths.Open(homePath)
		if pathProblem != nil {
			t.Fatal(pathProblem.Code)
		}
		defer root.Close()
		admitted, admission := datahome.InspectOwnedForEmailCredentialRecovery(root, nil)
		if admission != nil || admitted == nil {
			t.Fatalf("explicit recovery admission: %v", admission)
		}
		raw, present, pathProblem := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
		if pathProblem != nil || !present {
			t.Fatal("restart intent missing")
		}
		intent, err := testDecodeIntent(t, homePath, raw, *admitted)
		if err != nil {
			t.Fatal(err)
		}
		backend := &fakeSecrets{values: map[string][]byte{
			"CareerHub/dev/provider/" + intent.After.SecretName: []byte("SYNTHETIC-SECRET"),
		}}
		unrelated, err := testGenerationName(t, homePath, *admitted)
		if err != nil || unrelated == intent.After.SecretName {
			t.Fatal("unrelated target fixture failed")
		}
		backend.values["CareerHub/dev/provider/"+unrelated] = []byte("SYNTHETIC-UNRELATED")
		settled, problem := Recover(ctx, homePath, backend)
		if problem != nil || !settled.IntentRemoved || settled.DatabaseSide != emailcredentials.BeforeCommit {
			t.Fatalf("restart recovery: %+v %v", settled, problem)
		}
		store, err := secretstore.New(admitted.Channel, backend)
		if err != nil {
			t.Fatal(err)
		}
		assertSecret(t, store, intent.After.SecretName, false)
		assertSecret(t, store, unrelated, true)
		assertWitness(t, homePath, emailcredentials.RowWitness{})
		return
	}
	for _, phase := range []string{"seed", "recover"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if phase == "seed" {
			homePath = filepath.Join(t.TempDir(), "home")
		}
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConfigureNewRecoveryAfterProcessExit$")
		command.Env = append(os.Environ(), "CAREERHUB_EMAIL_CONFIGURE_RESTART_PHASE="+phase,
			"CAREERHUB_EMAIL_CONFIGURE_RESTART_HOME="+homePath)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("restart %s failed: %v %.1200s", phase, err, output)
		}
	}
}
