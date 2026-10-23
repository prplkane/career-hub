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
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
)

func retireOwnedRows(t *testing.T, homePath string) (rules, syncRows, receipts, discoveries int) {
	t.Helper()
	db := settingsDatabase(t, homePath, true)
	defer db.Close()
	for _, row := range []struct {
		query string
		value *int
	}{
		{`SELECT COUNT(*) FROM job_email_sender_rules WHERE connection_id='primary'`, &rules},
		{`SELECT COUNT(*) FROM job_email_sync_state WHERE connection_id='primary'`, &syncRows},
		{`SELECT COUNT(*) FROM job_email_message_receipts WHERE connection_id='primary'`, &receipts},
		{`SELECT COUNT(*) FROM job_discoveries WHERE discovery_id='synthetic-discovery'`, &discoveries},
	} {
		if err := db.QueryRow(row.query).Scan(row.value); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func runRetireScenario(t *testing.T, mode string) {
	t.Helper()
	ctx, homePath := context.Background(), os.Getenv("CAREERHUB_EMAIL_RETIRE_HOME")
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	backend := &settingsSecrets{fakeSecrets: &fakeSecrets{}}
	configured, problem := ConfigureNew(ctx, homePath, backend, validNewInput())
	if problem != nil || !configured.Committed {
		t.Fatalf("synthetic configure: %+v %v", configured, problem)
	}
	before, _, _, _, _, _, _ := settingsState(t, homePath)
	originalTarget := before.SecretName
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := testGenerationName(t, homePath, *home)
	if err != nil || store.Set(unrelated, "SYNTHETIC-UNRELATED") != nil {
		t.Fatal("unrelated credential fixture")
	}
	settingsSeedHistory(t, homePath, `{}`)
	db := settingsDatabase(t, homePath, false)
	if _, err := db.Exec(`UPDATE job_email_connections SET schedule_enabled=1 WHERE connection_id='primary'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO job_discoveries
		(discovery_id,state,content_scope,first_seen_at,last_seen_at,updated_at,revision)
		VALUES ('synthetic-discovery','inbox','link_only','t0','t0','t0',1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	backend.writes, backend.deletes = 0, 0
	operation, revision := emailcredentials.Disconnect, int64(1)
	hooks := retireHooks{}
	wantCode, afterSide, pending := "", true, false
	switch mode {
	case "disconnect-success":
	case "forget-success":
		operation = emailcredentials.Forget
	case "disconnect-then-forget":
		disconnected, issue := Disconnect(ctx, homePath, backend, 1)
		if issue != nil || !disconnected.Committed || !disconnected.IntentRemoved {
			t.Fatalf("first disconnect failed: %+v %v", disconnected, issue)
		}
		backend.deletes = 0
		before.Enabled, before.Revision = false, 2
		operation, revision = emailcredentials.Forget, 2
	case "forget-disabled-missing-secret":
		operation = emailcredentials.Forget
		changeRetireRow(t, homePath, `UPDATE job_email_connections SET enabled=0,schedule_enabled=0 WHERE connection_id='primary'`)
		delete(backend.values, "CareerHub/dev/provider/"+before.SecretName)
		before.Enabled = false
	case "disconnect-already-disabled":
		changeRetireRow(t, homePath, `UPDATE job_email_connections SET enabled=0,schedule_enabled=0 WHERE connection_id='primary'`)
		before.Enabled = false
		wantCode, afterSide = "EMAIL_CONNECTION_ALREADY_DISABLED", false
	case "v1-disconnect", "v1-forget":
		if mode == "v1-forget" {
			operation = emailcredentials.Forget
		}
		changeRetireRow(t, homePath, `UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, emailcredentials.LegacyName)
		if store.Set(emailcredentials.LegacyName, "SYNTHETIC-LEGACY") != nil {
			t.Fatal("legacy fixture")
		}
		before.SecretName = emailcredentials.LegacyName
		wantCode, afterSide = "EMAIL_CREDENTIAL_RECONNECT_REQUIRED", false
	case "revision":
		revision = 2
		wantCode, afterSide = "EMAIL_CONNECTION_REVISION_CONFLICT", false
	case "missing":
		changeRetireRow(t, homePath, `DELETE FROM job_email_connections WHERE connection_id='primary'`)
		before = emailcredentials.RowWitness{}
		wantCode, afterSide = "EMAIL_CONNECTION_NOT_CONFIGURED", false
	case "invalid-target":
		changeRetireRow(t, homePath, `UPDATE job_email_connections SET secret_name='arbitrary' WHERE connection_id='primary'`)
		before.SecretName = "arbitrary"
		wantCode, afterSide = "EMAIL_CONNECTION_INVALID", false
	case "missing-active-secret":
		delete(backend.values, "CareerHub/dev/provider/"+before.SecretName)
		wantCode, afterSide = "EMAIL_CREDENTIAL_MISSING", false
	case "disconnect-rollback", "forget-rollback":
		if mode == "forget-rollback" {
			operation = emailcredentials.Forget
		}
		hooks.beforeCommit = func() *Error { return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED") }
		wantCode, afterSide, pending = "EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED", false, true
	case "disconnect-unknown-commit", "forget-unknown-commit":
		if mode == "forget-unknown-commit" {
			operation = emailcredentials.Forget
		}
		hooks.commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("private post-commit detail")
		}
		wantCode, pending = "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED", true
	case "disconnect-reported-delete", "forget-reported-delete":
		if mode == "forget-reported-delete" {
			operation = emailcredentials.Forget
		}
		backend.deleteAfterError = true
		wantCode, pending = "EMAIL_SECRET_STORE_FAILED", true
	case "disconnect-cleanup-error", "forget-cleanup-error":
		if mode == "forget-cleanup-error" {
			operation = emailcredentials.Forget
		}
		hooks.beforeIntentRemove = func() *Error { return failure("EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED") }
		wantCode, pending = "EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED", true
	default:
		t.Fatalf("unknown scenario %q", mode)
	}
	result, problem := retireWithHooks(ctx, homePath, backend, revision, operation, hooks)
	if wantCode == "" {
		if problem != nil || !result.Committed || !result.OldDeleteConfirmed || !result.IntentRemoved || result.RecoveryRequired {
			t.Fatalf("retirement result %+v %v", result, problem)
		}
		wantRevision := int64(0)
		if operation == emailcredentials.Disconnect {
			wantRevision = 2
		}
		if result.Revision != wantRevision {
			t.Fatalf("retirement revision %d, want %d", result.Revision, wantRevision)
		}
	} else if problem == nil || problem.Code != wantCode {
		t.Fatalf("retirement failure %+v %v; want %s", result, problem, wantCode)
	}
	if mode == "disconnect-rollback" || mode == "forget-rollback" {
		if result.CommitAttempted || result.Committed || !result.RecoveryRequired {
			t.Fatalf("known rollback misreported: %+v", result)
		}
	}
	if mode == "disconnect-unknown-commit" || mode == "forget-unknown-commit" {
		if !result.CommitAttempted || result.Committed || !result.RecoveryRequired {
			t.Fatalf("unknown commit misreported: %+v", result)
		}
	}
	if wantCode != "" && !pending && (result.IntentPublished || result.CommitAttempted || backend.deletes != 0) {
		t.Fatalf("preflight mutated state: %+v; deletes %d", result, backend.deletes)
	}
	root, pathProblem := runtimepaths.Open(homePath)
	if pathProblem != nil {
		t.Fatal(pathProblem.Code)
	}
	raw, present, pathProblem := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	root.Close()
	if pathProblem != nil || present != pending {
		t.Fatalf("intent state %t, want %t: %v", present, pending, pathProblem)
	}
	if present {
		if bytes.Contains(raw, []byte("alerts@example.test")) || bytes.Contains(raw, []byte("SYNTHETIC")) {
			t.Fatal("retirement intent contains private input")
		}
		intent, err := testDecodeIntent(t, homePath, raw, *home)
		if err != nil || intent.Operation != operation || intent.Before != before {
			t.Fatalf("wrong retirement intent: %v %+v", err, intent)
		}
	}
	actual, _, schedule, _, cursor, receipts, _ := settingsState(t, homePath)
	if afterSide {
		if operation == emailcredentials.Disconnect {
			if actual != (emailcredentials.RowWitness{Present: true, Revision: 2, Enabled: false, SecretName: before.SecretName}) || schedule != 0 || cursor != 12 || receipts != 1 {
				t.Fatalf("disconnect changed history/state: %+v %d %d %d", actual, schedule, cursor, receipts)
			}
		} else if actual.Present {
			t.Fatalf("forget retained connection: %+v", actual)
		}
	} else if actual != before {
		t.Fatalf("precommit row changed: %+v, want %+v", actual, before)
	}
	rules, syncRows, ownedReceipts, discoveries := retireOwnedRows(t, homePath)
	if discoveries != 1 {
		t.Fatalf("retirement removed imported discovery: %d", discoveries)
	}
	if operation == emailcredentials.Forget && afterSide || mode == "missing" {
		if rules != 0 || syncRows != 0 || ownedReceipts != 0 {
			t.Fatalf("forget left owned metadata: %d %d %d", rules, syncRows, ownedReceipts)
		}
	} else if rules != 3 || syncRows != 1 || ownedReceipts != 1 {
		t.Fatalf("retirement changed owned metadata: %d %d %d", rules, syncRows, ownedReceipts)
	}
	assertSecret(t, store, unrelated, true)
	if pending {
		settled, recoveryProblem := Recover(ctx, homePath, backend)
		if recoveryProblem != nil || !settled.IntentRemoved {
			t.Fatalf("retirement recovery %+v %v", settled, recoveryProblem)
		}
		assertSecret(t, store, before.SecretName, !afterSide)
	} else if wantCode == "" {
		assertSecret(t, store, before.SecretName, false)
	}
	if mode == "v1-disconnect" || mode == "v1-forget" {
		assertSecret(t, store, emailcredentials.LegacyName, true)
	}
	if mode == "invalid-target" || mode == "v1-disconnect" || mode == "v1-forget" {
		assertSecret(t, store, originalTarget, true)
	}
}

func changeRetireRow(t *testing.T, homePath, query string, args ...any) {
	t.Helper()
	db := settingsDatabase(t, homePath, false)
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestRetireOwnedSubprocess(t *testing.T) {
	if mode := os.Getenv("CAREERHUB_EMAIL_RETIRE_MODE"); mode != "" {
		runRetireScenario(t, mode)
		return
	}
	for _, mode := range []string{
		"disconnect-success", "forget-success", "disconnect-then-forget", "forget-disabled-missing-secret",
		"disconnect-already-disabled", "v1-disconnect", "v1-forget", "revision", "missing",
		"invalid-target", "missing-active-secret", "disconnect-rollback", "forget-rollback",
		"disconnect-unknown-commit", "forget-unknown-commit",
		"disconnect-reported-delete", "forget-reported-delete",
		"disconnect-cleanup-error", "forget-cleanup-error",
	} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRetireOwnedSubprocess$")
			command.Env = append(os.Environ(), "CAREERHUB_EMAIL_RETIRE_MODE="+mode,
				"CAREERHUB_EMAIL_RETIRE_HOME="+filepath.Join(t.TempDir(), "home"))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("synthetic retirement %s failed: %v %.1200s", mode, err, output)
			}
		})
	}
}

func TestRetireRejectsInvalidAndUnowned(t *testing.T) {
	for _, operation := range []struct {
		name string
		call func(context.Context, string, secretstore.Backend, int64) (RetireResult, *Error)
	}{
		{"disconnect", Disconnect}, {"forget", Forget},
	} {
		t.Run(operation.name, func(t *testing.T) {
			backend := &fakeSecrets{}
			result, problem := operation.call(context.Background(), t.TempDir(), backend, 0)
			if result != (RetireResult{}) || problem == nil || problem.Code != "EMAIL_CONNECTION_INVALID" {
				t.Fatalf("invalid revision admitted: %+v %v", result, problem)
			}
			result, problem = operation.call(context.Background(), t.TempDir(), backend, 1)
			if result != (RetireResult{}) || problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
				t.Fatalf("unowned Home admitted: %+v %v", result, problem)
			}
			if len(backend.values) != 0 {
				t.Fatal("rejected retirement touched credentials")
			}
		})
	}
}

// A second process must settle a committed Forget before ordinary Home
// admission resumes. The backend reconstructs only synthetic target presence.
func TestForgetRecoveryAfterProcessExit(t *testing.T) {
	phase := os.Getenv("CAREERHUB_EMAIL_FORGET_RESTART_PHASE")
	homePath := os.Getenv("CAREERHUB_EMAIL_FORGET_RESTART_HOME")
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
			settingsSeedHistory(t, homePath, `{}`)
			changeRetireRow(t, homePath, `INSERT INTO job_discoveries
				(discovery_id,state,content_scope,first_seen_at,last_seen_at,updated_at,revision)
				VALUES ('synthetic-discovery','inbox','link_only','t0','t0','t0',1)`)
			result, problem := retireWithHooks(ctx, homePath, backend, 1, emailcredentials.Forget,
				retireHooks{commit: func(tx *sql.Tx) error {
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
			t.Fatalf("ordinary restart did not stop at Forget intent: %v", startup)
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
			t.Fatal("Forget intent missing after restart")
		}
		intent, err := testDecodeIntent(t, homePath, raw, *admitted)
		if err != nil || intent.Operation != emailcredentials.Forget {
			t.Fatalf("Forget intent invalid: %v", err)
		}
		backend := &fakeSecrets{values: map[string][]byte{
			"CareerHub/dev/provider/" + intent.Before.SecretName: []byte("SYNTHETIC-OLD"),
		}}
		unrelated, err := testGenerationName(t, homePath, *admitted)
		if err != nil || unrelated == intent.Before.SecretName {
			t.Fatal("unrelated fixture failed")
		}
		backend.values["CareerHub/dev/provider/"+unrelated] = []byte("SYNTHETIC-UNRELATED")
		settled, problem := Recover(ctx, homePath, backend)
		if problem != nil || !settled.IntentRemoved || settled.DatabaseSide != emailcredentials.AfterCommit || settled.Cleanup != emailcredentials.CleanupOld {
			t.Fatalf("restart Forget recovery: %+v %v", settled, problem)
		}
		store, err := secretstore.New(admitted.Channel, backend)
		if err != nil {
			t.Fatal(err)
		}
		assertSecret(t, store, intent.Before.SecretName, false)
		assertSecret(t, store, unrelated, true)
		assertWitness(t, homePath, emailcredentials.RowWitness{})
		rules, syncRows, receipts, discoveries := retireOwnedRows(t, homePath)
		if rules != 0 || syncRows != 0 || receipts != 0 || discoveries != 1 {
			t.Fatalf("restart Forget changed unrelated data: %d %d %d %d", rules, syncRows, receipts, discoveries)
		}
		return
	}
	homePath = filepath.Join(t.TempDir(), "home")
	for _, phase := range []string{"seed", "recover"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestForgetRecoveryAfterProcessExit$")
		command.Env = append(os.Environ(), "CAREERHUB_EMAIL_FORGET_RESTART_PHASE="+phase,
			"CAREERHUB_EMAIL_FORGET_RESTART_HOME="+homePath)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("Forget restart %s failed: %v %.1200s", phase, err, output)
		}
	}
}
