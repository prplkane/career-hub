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
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
)

func rotationTestStatus(t *testing.T, homePath string) (string, string, bool) {
	t.Helper()
	db := settingsDatabase(t, homePath, true)
	defer db.Close()
	var status, errorCode string
	var tested sql.NullString
	if err := db.QueryRow(`SELECT last_test_status,last_error_code,last_tested_at FROM job_email_connections WHERE connection_id='primary'`).Scan(&status, &errorCode, &tested); err != nil {
		t.Fatal(err)
	}
	return status, errorCode, tested.Valid
}

func runRotationScenario(t *testing.T, mode string) {
	t.Helper()
	ctx, homePath := context.Background(), os.Getenv("CAREERHUB_EMAIL_ROTATION_HOME")
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	backend := &settingsSecrets{fakeSecrets: &fakeSecrets{}}
	initial, problem := ConfigureNew(ctx, homePath, backend, validNewInput())
	if problem != nil || !initial.Committed {
		t.Fatalf("synthetic initial configure: %+v %v", initial, problem)
	}
	before, _, _, _, _, _, _ := settingsState(t, homePath)
	originalTarget := before.SecretName
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := testGenerationName(t, homePath, *home)
	if err != nil || store.Set(unrelated, "SYNTHETIC-UNRELATED") != nil {
		t.Fatal("unrelated fixture failed")
	}
	policyJSON := `{"application_tracking":{"include_any":[],"exclude_any":[]},"job_alerts":{"include_any":["data engineer"],"exclude_any":[]}}`
	policy, err := emailrouting.ParsePolicyJSON(policyJSON)
	if err != nil {
		t.Fatal(err)
	}
	policyJSON = policy.CanonicalJSON()
	settingsSeedHistory(t, homePath, policyJSON)
	db := settingsDatabase(t, homePath, false)
	if _, err := db.Exec(`UPDATE job_email_connections SET last_test_status='failed',last_error_code='TEST_ONLY',last_tested_at='t1' WHERE connection_id='primary'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	backend.writes, backend.deletes = 0, 0
	interval := 60
	input := RotateInput{
		AccountAddress: " Alerts@Example.Test ", Password: "qrst uvwx yzab cdef",
		JobSenders:         []string{"jobs@example.test", "new@example.test"},
		ApplicationSenders: []string{"jobs@example.test", "track@example.test"},
		ScheduleEnabled:    true, IntervalMinutes: &interval, ExpectedRevision: 1,
	}
	hooks := rotationHooks{}
	var operationBackend secretstore.Backend = backend
	wantCode, afterSide, intentPending := "", true, false
	collisionTarget := ""
	switch mode {
	case "success":
	case "disabled-missing-old":
		db := settingsDatabase(t, homePath, false)
		if _, err := db.Exec(`UPDATE job_email_connections SET enabled=0,schedule_enabled=0 WHERE connection_id='primary'`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		delete(backend.values, "CareerHub/dev/provider/"+before.SecretName)
		before.Enabled = false
	case "v1-blocked":
		db := settingsDatabase(t, homePath, false)
		if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, emailcredentials.LegacyName); err != nil {
			t.Fatal(err)
		}
		db.Close()
		if store.Set(emailcredentials.LegacyName, "SYNTHETIC-LEGACY") != nil {
			t.Fatal("legacy fixture failed")
		}
		backend.writes = 0
		before.SecretName = emailcredentials.LegacyName
		wantCode, afterSide = "EMAIL_CREDENTIAL_RECONNECT_REQUIRED", false
	case "revision":
		input.ExpectedRevision = 2
		wantCode, afterSide = "EMAIL_CONNECTION_REVISION_CONFLICT", false
	case "account":
		input.AccountAddress = "other@example.test"
		wantCode, afterSide = "EMAIL_CONNECTION_ACCOUNT_IMMUTABLE", false
	case "missing-active-secret":
		delete(backend.values, "CareerHub/dev/provider/"+before.SecretName)
		wantCode, afterSide = "EMAIL_CREDENTIAL_MISSING", false
	case "invalid-target":
		db := settingsDatabase(t, homePath, false)
		if _, err := db.Exec(`UPDATE job_email_connections SET secret_name='arbitrary' WHERE connection_id='primary'`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		before.SecretName = "arbitrary"
		wantCode, afterSide = "EMAIL_CONNECTION_INVALID", false
	case "missing-password":
		input.Password = ""
		wantCode, afterSide = "EMAIL_CREDENTIAL_MISSING", false
	case "collision":
		collisionTarget, err = testGenerationName(t, homePath, *home)
		if err != nil || store.Set(collisionTarget, "SYNTHETIC-COLLISION") != nil {
			t.Fatal("collision fixture failed")
		}
		backend.writes = 0
		next, err := testGenerationName(t, homePath, *home)
		if err != nil || next == collisionTarget {
			t.Fatal("second target fixture failed")
		}
		calls := 0
		hooks.generateName = func(datahome.Info) (string, error) {
			calls++
			if calls == 1 {
				return collisionTarget, nil
			}
			return next, nil
		}
		defer func() {
			if calls != 2 {
				t.Fatalf("collision retry count %d", calls)
			}
		}()
	case "reported-write":
		operationBackend = &reportedWriteSecrets{backend.fakeSecrets}
		wantCode, afterSide, intentPending = "EMAIL_SECRET_STORE_FAILED", false, true
	case "new-readback-missing":
		hooks.afterNewWrite = func() *Error {
			for target := range backend.values {
				if target != "CareerHub/dev/provider/"+before.SecretName && target != "CareerHub/dev/provider/"+unrelated {
					delete(backend.values, target)
				}
			}
			return nil
		}
		wantCode, afterSide, intentPending = "EMAIL_CREDENTIAL_RECOVERY_REQUIRED", false, true
	case "rollback":
		hooks.beforeCommit = func() *Error { return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED") }
		wantCode, afterSide, intentPending = "EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED", false, true
	case "committed-but-error":
		hooks.commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("private post-commit detail")
		}
		wantCode, intentPending = "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED", true
	case "reported-delete":
		backend.deleteAfterError = true
		wantCode, intentPending = "EMAIL_SECRET_STORE_FAILED", true
	case "cleanup-error":
		hooks.beforeIntentRemove = func() *Error { return failure("EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED") }
		wantCode, intentPending = "EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED", true
	default:
		t.Fatalf("unknown rotation mode %q", mode)
	}
	result, problem := rotateWithHooks(ctx, homePath, operationBackend, input, hooks)
	if wantCode == "" {
		if problem != nil || !result.Committed || !result.IntentRemoved || result.RecoveryRequired || result.Revision != 2 || !result.OldDeleteConfirmed {
			t.Fatalf("rotation result %+v %v", result, problem)
		}
	} else if problem == nil || problem.Code != wantCode {
		t.Fatalf("rotation failure %+v %v; want %s", result, problem, wantCode)
	}
	if mode == "committed-but-error" && (!result.CommitAttempted || result.Committed || !result.RecoveryRequired) {
		t.Fatalf("unknown commit misreported: %+v", result)
	}
	if mode == "rollback" && (result.CommitAttempted || result.Committed || !result.RecoveryRequired) {
		t.Fatalf("known rollback misreported: %+v", result)
	}
	if mode == "reported-delete" && (!result.Committed || !result.OldDeleteAttempted || !result.RecoveryRequired) {
		t.Fatalf("reported delete failure misreported: %+v", result)
	}
	if wantCode != "" && !intentPending && (result.IntentPublished || result.NewWriteAttempted || backend.writes != 0 || backend.deletes != 0) {
		t.Fatalf("preflight mutated credentials or intent: %+v, %d/%d", result, backend.writes, backend.deletes)
	}
	root, pathProblem := runtimepaths.Open(homePath)
	if pathProblem != nil {
		t.Fatal(pathProblem.Code)
	}
	defer root.Close()
	raw, present, pathProblem := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if pathProblem != nil || present != intentPending {
		t.Fatalf("rotation intent state %t, want %t: %v", present, intentPending, pathProblem)
	}
	var intended emailcredentials.ScopedIntent
	if present {
		if bytes.Contains(raw, []byte("qrstuvwxyzabcdef")) || bytes.Contains(raw, []byte("alerts@example.test")) || bytes.Contains(raw, []byte("jobs@example.test")) {
			t.Fatal("rotation intent contains private input")
		}
		intended, err = testDecodeIntent(t, homePath, raw, *home)
		if err != nil || intended.Operation != emailcredentials.Rotate || intended.Before != before {
			t.Fatalf("wrong rotation intent: %v", err)
		}
	}
	current, account, scheduled, gotInterval, lastUID, receipts, testStatus := settingsState(t, homePath)
	if current.Revision != before.Revision+boolInt64(afterSide) || account != "alerts@example.test" || lastUID != 12 || receipts != 1 {
		t.Fatalf("rotation changed identity/history: %+v %q %d %d", current, account, lastUID, receipts)
	}
	if afterSide {
		if !current.Enabled || current.SecretName == before.SecretName || scheduled != 1 || gotInterval != 60 || testStatus != "not_tested" {
			t.Fatalf("rotation did not switch target/settings: %+v %d %d %s", current, scheduled, gotInterval, testStatus)
		}
		status, errorCode, tested := rotationTestStatus(t, homePath)
		if status != "not_tested" || errorCode != "" || tested {
			t.Fatalf("test state not reset: %q %q %t", status, errorCode, tested)
		}
		assertSettingsRules(t, homePath, policyJSON, policyJSON, true)
	} else {
		if current.SecretName != before.SecretName || current.Enabled != before.Enabled || testStatus != "failed" || scheduled != 0 || gotInterval != 30 {
			t.Fatalf("precommit state changed: %+v %s %d %d", current, testStatus, scheduled, gotInterval)
		}
		assertSettingsRules(t, homePath, policyJSON, policyJSON, false)
	}
	assertSecret(t, store, unrelated, true)
	if collisionTarget != "" {
		assertSecret(t, store, collisionTarget, true)
	}
	if present {
		settled, recoveryProblem := Recover(ctx, homePath, backend)
		if recoveryProblem != nil || !settled.IntentRemoved {
			t.Fatalf("rotation recovery %+v %v", settled, recoveryProblem)
		}
		assertSecret(t, store, intended.After.SecretName, afterSide)
		assertSecret(t, store, before.SecretName, !afterSide && before.Enabled)
		assertSecret(t, store, unrelated, true)
	} else if wantCode == "" {
		assertSecret(t, store, current.SecretName, true)
		assertSecret(t, store, before.SecretName, false)
	}
	if mode == "invalid-target" {
		assertSecret(t, store, originalTarget, true)
	}
	if mode == "v1-blocked" {
		assertSecret(t, store, emailcredentials.LegacyName, true)
	}
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func TestRotateOwnedSubprocess(t *testing.T) {
	if mode := os.Getenv("CAREERHUB_EMAIL_ROTATION_MODE"); mode != "" {
		runRotationScenario(t, mode)
		return
	}
	for _, mode := range []string{
		"success", "disabled-missing-old", "v1-blocked", "revision", "account", "missing-active-secret", "invalid-target", "missing-password", "collision", "reported-write", "new-readback-missing", "rollback", "committed-but-error", "reported-delete", "cleanup-error",
	} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRotateOwnedSubprocess$")
			command.Env = append(os.Environ(), "CAREERHUB_EMAIL_ROTATION_MODE="+mode,
				"CAREERHUB_EMAIL_ROTATION_HOME="+filepath.Join(t.TempDir(), "home"))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("synthetic rotation %s failed: %v %.1200s", mode, err, output)
			}
		})
	}
}

// A second process must observe and settle an unknown-commit rotation. The
// fake backend reconstructs only synthetic target presence across processes.
func TestRotateRecoveryAfterProcessExit(t *testing.T) {
	phase := os.Getenv("CAREERHUB_EMAIL_ROTATION_RESTART_PHASE")
	homePath := os.Getenv("CAREERHUB_EMAIL_ROTATION_RESTART_HOME")
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
			backend := &fakeSecrets{}
			if result, problem := ConfigureNew(ctx, homePath, backend, validNewInput()); problem != nil || !result.Committed {
				t.Fatalf("seed configure: %+v %v", result, problem)
			}
			result, problem := rotateWithHooks(ctx, homePath, backend, RotateInput{
				AccountAddress: "alerts@example.test", Password: "qrstuvwxyzabcdef",
				JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 1,
			}, rotationHooks{commit: func(tx *sql.Tx) error {
				if err := tx.Commit(); err != nil {
					return err
				}
				return errors.New("private post-commit detail")
			}})
			if problem == nil || problem.Code != "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED" || !result.RecoveryRequired || result.Committed {
				t.Fatalf("seed did not retain unknown commit: %+v %v", result, problem)
			}
			return
		}
		if phase != "recover" || startup == nil || startup.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatalf("ordinary restart did not stop at rotation intent: %v", startup)
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
			t.Fatal("rotation intent missing after restart")
		}
		intent, err := testDecodeIntent(t, homePath, raw, *admitted)
		if err != nil || intent.Operation != emailcredentials.Rotate {
			t.Fatalf("rotation intent invalid: %v", err)
		}
		backend := &fakeSecrets{values: map[string][]byte{
			"CareerHub/dev/provider/" + intent.Before.SecretName: []byte("SYNTHETIC-OLD"),
			"CareerHub/dev/provider/" + intent.After.SecretName:  []byte("SYNTHETIC-NEW"),
		}}
		unrelated, err := testGenerationName(t, homePath, *admitted)
		if err != nil || unrelated == intent.After.SecretName || unrelated == intent.Before.SecretName {
			t.Fatal("unrelated fixture failed")
		}
		backend.values["CareerHub/dev/provider/"+unrelated] = []byte("SYNTHETIC-UNRELATED")
		settled, problem := Recover(ctx, homePath, backend)
		if problem != nil || !settled.IntentRemoved || settled.DatabaseSide != emailcredentials.AfterCommit || settled.Cleanup != emailcredentials.CleanupOld {
			t.Fatalf("restart rotation recovery: %+v %v", settled, problem)
		}
		store, err := secretstore.New(admitted.Channel, backend)
		if err != nil {
			t.Fatal(err)
		}
		assertSecret(t, store, intent.Before.SecretName, false)
		assertSecret(t, store, intent.After.SecretName, true)
		assertSecret(t, store, unrelated, true)
		assertWitness(t, homePath, intent.After)
		return
	}
	homePath = filepath.Join(t.TempDir(), "home")
	for _, phase := range []string{"seed", "recover"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRotateRecoveryAfterProcessExit$")
		command.Env = append(os.Environ(), "CAREERHUB_EMAIL_ROTATION_RESTART_PHASE="+phase,
			"CAREERHUB_EMAIL_ROTATION_RESTART_HOME="+homePath)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("rotation restart %s failed: %v %.1200s", phase, err, output)
		}
	}
}
