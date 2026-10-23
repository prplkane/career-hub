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
)

type promotionSecrets struct {
	*fakeSecrets
	writes, deletes []string
}

func (backend *promotionSecrets) Write(target string, value []byte) error {
	backend.writes = append(backend.writes, target)
	return backend.fakeSecrets.Write(target, value)
}

func (backend *promotionSecrets) Delete(target string) (bool, error) {
	backend.deletes = append(backend.deletes, target)
	return backend.fakeSecrets.Delete(target)
}

func runPromotionScenario(t *testing.T, mode string) {
	t.Helper()
	ctx, homePath := context.Background(), os.Getenv("CAREERHUB_EMAIL_PROMOTION_HOME")
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	backend := &promotionSecrets{fakeSecrets: &fakeSecrets{}}
	configured, problem := ConfigureNew(ctx, homePath, backend, validNewInput())
	if problem != nil || !configured.Committed {
		t.Fatalf("synthetic connection setup: %+v %v", configured, problem)
	}
	initial, _, _, _, _, _, _ := settingsState(t, homePath)
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "v2-refused" {
		db := settingsDatabase(t, homePath, false)
		if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, emailcredentials.LegacyName); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Delete(initial.SecretName); err != nil {
			t.Fatal(err)
		}
	}
	oldPresent := mode != "disabled-missing-v1" && mode != "enabled-missing-v1" && mode != "v2-refused"
	if oldPresent {
		if err := store.Set(emailcredentials.LegacyName, "SYNTHETIC-SHARED-SECRET"); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "disabled-missing-v1" {
		db := settingsDatabase(t, homePath, false)
		if _, err := db.Exec(`UPDATE job_email_connections SET enabled=0,schedule_enabled=0 WHERE connection_id='primary'`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := emailrouting.ParsePolicyJSON(`{"application_tracking":{"include_any":[],"exclude_any":[]},"job_alerts":{"include_any":["data engineer"],"exclude_any":[]}}`)
	if err != nil {
		t.Fatal(err)
	}
	originalPolicy := policy.CanonicalJSON()
	settingsSeedHistory(t, homePath, originalPolicy)
	db := settingsDatabase(t, homePath, false)
	if _, err := db.Exec(`UPDATE job_email_connections SET last_test_status='failed',last_error_code='TEST_ONLY',last_tested_at='t1' WHERE connection_id='primary'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, _, _, _, _, _, _ := settingsState(t, homePath)
	interval := 60
	input := PromoteLegacyInput{
		ExpectedHome: home, AccountAddress: " Alerts@Example.Test ", Password: "qrst uvwx yzab cdef",
		JobSenders:         []string{"jobs@example.test", "new@example.test"},
		ApplicationSenders: []string{"jobs@example.test", "track@example.test"},
		ScheduleEnabled:    true, IntervalMinutes: &interval, ExpectedRevision: before.Revision,
	}
	hooks := rotationHooks{}
	wantCode, afterSide, pending := "", true, false
	var collisionTarget string
	switch mode {
	case "success", "disabled-missing-v1", "enabled-missing-v1":
	case "v2-refused":
		wantCode, afterSide = "EMAIL_CREDENTIAL_LEGACY_REQUIRED", false
	case "stale-revision":
		input.ExpectedRevision++
		wantCode, afterSide = "EMAIL_CONNECTION_REVISION_CONFLICT", false
	case "account-change":
		input.AccountAddress = "other@example.test"
		wantCode, afterSide = "EMAIL_CONNECTION_ACCOUNT_IMMUTABLE", false
	case "missing-password":
		input.Password = ""
		wantCode, afterSide = "EMAIL_CREDENTIAL_MISSING", false
	case "missing-home-pin":
		input.ExpectedHome = nil
		wantCode, afterSide = "EMAIL_CONNECTION_INVALID", false
	case "wrong-home-pin":
		wrong := *home
		wrong.DataHomeID = "dh_ffffffffffffffffffffffffffffffff"
		if wrong.DataHomeID == home.DataHomeID {
			wrong.DataHomeID = "dh_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		}
		input.ExpectedHome = &wrong
		wantCode, afterSide = "DATA_HOME_CHANGED", false
	case "collision":
		collisionTarget, err = testGenerationName(t, homePath, *home)
		if err != nil || store.Set(collisionTarget, "SYNTHETIC-COLLISION") != nil {
			t.Fatal("collision fixture unavailable")
		}
		next, err := testGenerationName(t, homePath, *home)
		if err != nil || next == collisionTarget {
			t.Fatal("second generation unavailable")
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
	case "post-write-error":
		hooks.afterNewWrite = func() *Error { return failure("EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED") }
		wantCode, afterSide, pending = "EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED", false, true
	case "precommit-error":
		hooks.beforeCommit = func() *Error { return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED") }
		wantCode, afterSide, pending = "EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED", false, true
	case "committed-but-error":
		hooks.commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("private synthetic post-commit detail")
		}
		wantCode, pending = "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED", true
	case "intent-cleanup-error":
		hooks.beforeIntentRemove = func() *Error { return failure("EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED") }
		wantCode, pending = "EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED", true
	default:
		t.Fatalf("unknown promotion mode %q", mode)
	}
	backend.reads, backend.writes, backend.deletes = nil, nil, nil
	var result PromoteLegacyResult
	if mode == "success" {
		result, problem = PromoteLegacy(ctx, homePath, backend, input)
	} else {
		result, problem = promoteLegacyWithHooks(ctx, homePath, backend, input, hooks)
	}
	if wantCode == "" {
		if problem != nil || !result.Committed || !result.IntentRemoved || result.RecoveryRequired || result.Revision != before.Revision+1 {
			t.Fatalf("promotion result: %+v %v", result, problem)
		}
	} else if problem == nil || problem.Code != wantCode || strings.Contains(problem.Error(), "SYNTHETIC-") {
		t.Fatalf("promotion error: %+v %v; want %s", result, problem, wantCode)
	}
	if result.CommitAttempted && !afterSide && mode != "precommit-error" {
		t.Fatal("precommit refusal attempted commit")
	}
	if mode == "committed-but-error" && (!result.CommitAttempted || result.Committed || !result.RecoveryRequired) {
		t.Fatalf("unknown commit misreported: %+v", result)
	}
	if mode == "intent-cleanup-error" && (!result.Committed || !result.RecoveryRequired || result.IntentRemoved) {
		t.Fatalf("committed cleanup error misreported: %+v", result)
	}
	for _, target := range backend.reads {
		if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
			t.Fatal("promotion read the shared v1 target")
		}
	}
	if len(backend.deletes) != 0 {
		t.Fatalf("promotion deleted targets: %d", len(backend.deletes))
	}
	if !pending && wantCode != "" && (result.IntentPublished || result.NewWriteAttempted || len(backend.writes) != 0) {
		t.Fatalf("preflight made a write: %+v", result)
	}
	root, pathProblem := runtimepaths.Open(homePath)
	if pathProblem != nil {
		t.Fatal(pathProblem.Code)
	}
	defer root.Close()
	raw, present, pathProblem := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if pathProblem != nil || present != pending {
		t.Fatalf("promotion intent present=%t pending=%t: %v", present, pending, pathProblem)
	}
	var intent emailcredentials.ScopedIntent
	if present {
		intent, err = testDecodeIntent(t, homePath, raw, *home)
		if err != nil || intent.Operation != emailcredentials.AdoptUnbound || intent.Before != before ||
			bytes.Contains(raw, []byte("qrstuvwxyzabcdef")) || bytes.Contains(raw, []byte("alerts@example.test")) {
			t.Fatalf("invalid or private promotion intent: %v", err)
		}
	}
	current, account, scheduled, gotInterval, lastUID, receipts, testStatus := settingsState(t, homePath)
	if account != "alerts@example.test" || lastUID != 12 || receipts != 1 {
		t.Fatalf("promotion changed identity/history: %q %d %d", account, lastUID, receipts)
	}
	if afterSide {
		if current.Revision != before.Revision+1 || !current.Enabled || current.SecretName == before.SecretName ||
			scheduled != 1 || gotInterval != 60 || testStatus != "not_tested" {
			t.Fatalf("promotion did not switch one row: %+v %d %d %s", current, scheduled, gotInterval, testStatus)
		}
		assertSettingsRules(t, homePath, originalPolicy, originalPolicy, true)
	} else if current != before || scheduled != 0 || gotInterval != 30 || testStatus != "failed" {
		t.Fatalf("precommit promotion changed row: %+v %d %d %s", current, scheduled, gotInterval, testStatus)
	}
	if pending {
		settled, recoveryProblem := Recover(ctx, homePath, backend)
		wantSide := emailcredentials.BeforeCommit
		if afterSide {
			wantSide = emailcredentials.AfterCommit
		}
		if recoveryProblem != nil || !settled.IntentRemoved || settled.DatabaseSide != wantSide {
			t.Fatalf("promotion recovery: %+v %v", settled, recoveryProblem)
		}
		if afterSide && settled.Cleanup != emailcredentials.CleanupNone || !afterSide && settled.Cleanup != emailcredentials.CleanupNew {
			t.Fatalf("promotion recovery cleanup: %+v", settled)
		}
		for _, target := range backend.reads {
			if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
				t.Fatal("promotion recovery read shared v1")
			}
		}
		for _, target := range backend.deletes {
			if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
				t.Fatal("promotion recovery deleted shared v1")
			}
		}
	}
	assertSecret(t, store, emailcredentials.LegacyName, oldPresent)
	assertSecret(t, store, current.SecretName, true)
	if collisionTarget != "" {
		assertSecret(t, store, collisionTarget, true)
	}
}

func TestPromoteLegacyOwnedSubprocess(t *testing.T) {
	if mode := os.Getenv("CAREERHUB_EMAIL_PROMOTION_MODE"); mode != "" {
		runPromotionScenario(t, mode)
		return
	}
	for _, mode := range []string{
		"success", "disabled-missing-v1", "enabled-missing-v1", "v2-refused", "stale-revision", "account-change",
		"missing-password", "missing-home-pin", "wrong-home-pin", "collision", "post-write-error",
		"precommit-error", "committed-but-error", "intent-cleanup-error",
	} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPromoteLegacyOwnedSubprocess$")
			command.Env = append(os.Environ(), "CAREERHUB_EMAIL_PROMOTION_MODE="+mode,
				"CAREERHUB_EMAIL_PROMOTION_HOME="+filepath.Join(t.TempDir(), "home"))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("synthetic promotion %s failed: %v %.1200s", mode, err, output)
			}
		})
	}
}

// The next process must recover an unknown commit without touching the shared
// v1 target. The fake backend reconstructs only synthetic secret presence.
func TestPromoteLegacyRecoveryAfterProcessExit(t *testing.T) {
	phase := os.Getenv("CAREERHUB_EMAIL_PROMOTION_RESTART_PHASE")
	homePath := os.Getenv("CAREERHUB_EMAIL_PROMOTION_RESTART_HOME")
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
			before, _, _, _, _, _, _ := settingsState(t, homePath)
			db := settingsDatabase(t, homePath, false)
			if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, emailcredentials.LegacyName); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := secretstore.New(home.Channel, backend)
			if err != nil || store.Set(emailcredentials.LegacyName, "SYNTHETIC-SHARED") != nil {
				t.Fatal("legacy synthetic target unavailable")
			}
			if _, err := store.Delete(before.SecretName); err != nil {
				t.Fatal(err)
			}
			backend.reads = nil
			result, problem := promoteLegacyWithHooks(ctx, homePath, backend, PromoteLegacyInput{
				ExpectedHome: home, AccountAddress: "alerts@example.test", Password: "qrstuvwxyzabcdef",
				JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 1,
			}, rotationHooks{commit: func(tx *sql.Tx) error {
				if err := tx.Commit(); err != nil {
					return err
				}
				return errors.New("private synthetic post-commit detail")
			}})
			if problem == nil || problem.Code != "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED" ||
				!result.RecoveryRequired || !result.CommitAttempted || result.Committed {
				t.Fatalf("seed did not retain unknown commit: %+v %v", result, problem)
			}
			for _, target := range backend.reads {
				if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
					t.Fatal("seed read shared v1")
				}
			}
			return
		}
		if phase != "recover" || startup == nil || startup.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatalf("restart did not stop on promotion intent: %v", startup)
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
			t.Fatal("promotion intent missing after restart")
		}
		intent, err := testDecodeIntent(t, homePath, raw, *admitted)
		if err != nil || intent.Operation != emailcredentials.AdoptUnbound || intent.Before.SecretName != emailcredentials.LegacyName {
			t.Fatalf("invalid promotion intent after restart: %v", err)
		}
		backend := &fakeSecrets{values: map[string][]byte{
			"CareerHub/dev/provider/" + emailcredentials.LegacyName: []byte("SYNTHETIC-SHARED"),
			"CareerHub/dev/provider/" + intent.After.SecretName:     []byte("SYNTHETIC-NEW"),
		}}
		settled, problem := Recover(ctx, homePath, backend)
		if problem != nil || !settled.IntentRemoved || settled.DatabaseSide != emailcredentials.AfterCommit || settled.Cleanup != emailcredentials.CleanupNone {
			t.Fatalf("restart promotion recovery: %+v %v", settled, problem)
		}
		for _, target := range backend.reads {
			if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
				t.Fatal("restart recovery read shared v1")
			}
		}
		store, err := secretstore.New(admitted.Channel, backend)
		if err != nil {
			t.Fatal(err)
		}
		assertWitness(t, homePath, intent.After)
		assertSecret(t, store, emailcredentials.LegacyName, true)
		assertSecret(t, store, intent.After.SecretName, true)
		return
	}
	homePath = filepath.Join(t.TempDir(), "home")
	for _, phase := range []string{"seed", "recover"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPromoteLegacyRecoveryAfterProcessExit$")
		command.Env = append(os.Environ(), "CAREERHUB_EMAIL_PROMOTION_RESTART_PHASE="+phase,
			"CAREERHUB_EMAIL_PROMOTION_RESTART_HOME="+homePath)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("promotion restart %s failed: %v %.1200s", phase, err, output)
		}
	}
}

func TestPromoteLegacyPreservesSecondHome(t *testing.T) {
	phase := os.Getenv("CAREERHUB_EMAIL_PROMOTION_TWO_HOME_PHASE")
	base := os.Getenv("CAREERHUB_EMAIL_PROMOTION_TWO_HOME_BASE")
	if phase != "" {
		firstPath, secondPath := filepath.Join(base, "first"), filepath.Join(base, "second")
		if phase == "seed-second" {
			ctx := context.Background()
			if home, startup := datahome.BootstrapForProcess(ctx, secondPath, "dev"); startup != nil || home == nil {
				t.Fatalf("second Home setup: %v", startup)
			}
			if _, err := schema.PrepareOwned(ctx, secondPath); err != nil {
				t.Fatal(err)
			}
			connectionRow(t, secondPath, emailcredentials.RowWitness{
				Present: true, Revision: 4, Enabled: true, SecretName: emailcredentials.LegacyName,
			})
			return
		}
		if phase != "promote-first" {
			t.Fatal("unknown two-Home promotion phase")
		}
		t.Setenv("CAREERHUB_EMAIL_PROMOTION_HOME", firstPath)
		runPromotionScenario(t, "success")
		first, firstProblem := datahome.Inspect(firstPath, nil)
		second, secondProblem := datahome.Inspect(secondPath, nil)
		if firstProblem != nil || secondProblem != nil || first == nil || second == nil ||
			first.Channel != second.Channel || first.DataHomeID == second.DataHomeID {
			t.Fatal("distinct same-channel Homes unavailable")
		}
		assertWitness(t, secondPath, emailcredentials.RowWitness{
			Present: true, Revision: 4, Enabled: true, SecretName: emailcredentials.LegacyName,
		})
		return
	}
	base = t.TempDir()
	for _, phase := range []string{"seed-second", "promote-first"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPromoteLegacyPreservesSecondHome$")
		command.Env = append(os.Environ(), "CAREERHUB_EMAIL_PROMOTION_TWO_HOME_PHASE="+phase,
			"CAREERHUB_EMAIL_PROMOTION_TWO_HOME_BASE="+base)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("two-Home promotion %s failed: %v %.1200s", phase, err, output)
		}
	}
}
