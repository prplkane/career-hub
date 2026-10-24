//go:build windows

package emailconnection

import (
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
	"careerhub.local/backend/internal/email/emailgate"
	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

type settingsSecrets struct {
	*fakeSecrets
	writes, deletes int
}

func (backend *settingsSecrets) Write(target string, value []byte) error {
	backend.writes++
	return backend.fakeSecrets.Write(target, value)
}
func (backend *settingsSecrets) Delete(target string) (bool, error) {
	backend.deletes++
	return backend.fakeSecrets.Delete(target)
}

func settingsDatabase(t *testing.T, path string, readOnly bool) *sql.DB {
	t.Helper()
	var db *sql.DB
	var err error
	file := filepath.Join(path, "applications", "career_hub.sqlite3")
	if readOnly {
		db, err = sqlitestore.OpenReadOnly(context.Background(), file)
	} else {
		db, err = sqlitestore.OpenReadWriteExisting(context.Background(), file)
	}
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func settingsSeedHistory(t *testing.T, home string, policy string) {
	t.Helper()
	db := settingsDatabase(t, home, false)
	defer db.Close()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE job_email_sync_state SET uidvalidity='u1',last_uid=12 WHERE connection_id='primary' AND folder='INBOX'`, nil},
		{`INSERT INTO job_email_message_receipts (connection_id,folder,uidvalidity,message_uid,status,first_seen_at,updated_at) VALUES ('primary','INBOX','u1',12,'imported','t0','t0')`, nil},
		{`UPDATE job_email_sender_rules SET subject_policy_json=? WHERE connection_id='primary' AND sender_address='jobs@example.test'`, []any{policy}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func settingsState(t *testing.T, home string) (emailcredentials.RowWitness, string, int, int, int, int, string) {
	t.Helper()
	db := settingsDatabase(t, home, true)
	defer db.Close()
	witness, problem := readWitness(context.Background(), db)
	if problem != nil {
		t.Fatal(problem)
	}
	if !witness.Present {
		return witness, "", 0, 0, 0, 0, ""
	}
	var account, testStatus string
	var scheduled, interval, lastUID, receiptCount int
	if err := db.QueryRow(`SELECT account_address,schedule_enabled,interval_minutes,last_test_status FROM job_email_connections WHERE connection_id='primary'`).Scan(&account, &scheduled, &interval, &testStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT last_uid FROM job_email_sync_state WHERE connection_id='primary' AND folder='INBOX'`).Scan(&lastUID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_email_message_receipts WHERE connection_id='primary'`).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	return witness, account, scheduled, interval, lastUID, receiptCount, testStatus
}

func settingsSenderCreated(t *testing.T, home, address string) string {
	t.Helper()
	db := settingsDatabase(t, home, true)
	defer db.Close()
	var created string
	if err := db.QueryRow(`SELECT created_at FROM job_email_sender_rules WHERE connection_id='primary' AND sender_address=?`, address).Scan(&created); err != nil {
		t.Fatal(err)
	}
	return created
}

func assertSettingsRules(t *testing.T, home, originalPolicy, overridePolicy string, applied bool) {
	t.Helper()
	db := settingsDatabase(t, home, true)
	defer db.Close()
	rows, err := db.Query(`SELECT sender_address,disposition,purpose,subject_policy_json,created_at FROM job_email_sender_rules WHERE connection_id='primary' ORDER BY sender_address`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	rules := map[string][4]string{}
	for rows.Next() {
		var address, disposition, purpose, policy, created string
		if err := rows.Scan(&address, &disposition, &purpose, &policy, &created); err != nil {
			t.Fatal(err)
		}
		rules[address] = [4]string{disposition, purpose, policy, created}
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if !applied {
		if len(rules) != 3 || rules["jobs@example.test"][2] != originalPolicy || rules["both@example.test"][0] != "allowed" {
			t.Fatalf("failed edit changed rules: %+v", rules)
		}
		return
	}
	if len(rules) != 4 || rules["both@example.test"][0] != "disabled" ||
		rules["jobs@example.test"][0] != "allowed" || rules["jobs@example.test"][1] != emailrouting.SenderPurposeBoth ||
		rules["jobs@example.test"][2] != overridePolicy ||
		rules["new@example.test"][0] != "allowed" || rules["new@example.test"][1] != emailrouting.SenderPurposeJobAlerts ||
		rules["track@example.test"][0] != "allowed" || rules["track@example.test"][1] != emailrouting.SenderPurposeApplicationTracking {
		t.Fatalf("source-shaped sender edit failed: %+v", rules)
	}
}

func runSettingsScenario(t *testing.T, mode string) {
	t.Helper()
	ctx, homePath := context.Background(), os.Getenv("CAREERHUB_EMAIL_SETTINGS_HOME")
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
		t.Fatalf("synthetic initial configure: %+v %v", configured, problem)
	}
	before, _, _, _, _, _, _ := settingsState(t, homePath)
	originalTarget := before.SecretName
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := emailcredentials.NewGenerationName(*home)
	if err != nil || store.Set(unrelated, "SYNTHETIC-UNRELATED") != nil {
		t.Fatal("unrelated fixture failed")
	}
	backend.writes, backend.deletes = 0, 0
	policy, err := emailrouting.ParsePolicyJSON(`{"application_tracking":{"include_any":[],"exclude_any":[]},"job_alerts":{"include_any":["Data Engineer"],"exclude_any":[]}}`)
	if err != nil {
		t.Fatal(err)
	}
	originalPolicy := policy.CanonicalJSON()
	settingsSeedHistory(t, homePath, originalPolicy)
	originalCreated := settingsSenderCreated(t, homePath, "jobs@example.test")
	interval := 60
	input := EditSettingsInput{
		AccountAddress:     " Alerts@Example.Test ",
		JobSenders:         []string{"jobs@example.test", "new@example.test"},
		ApplicationSenders: []string{"jobs@example.test", "track@example.test"},
		ScheduleEnabled:    true, IntervalMinutes: &interval, ExpectedRevision: 1,
	}
	hooks := settingsHooks{}
	wantCode, committed := "", true
	switch mode {
	case "success":
	case "v1":
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
		wantCode, committed = "EMAIL_CREDENTIAL_RECONNECT_REQUIRED", false
	case "override-policy":
		input.SubjectPolicies = map[string]string{
			"jobs@example.test": "{}", "new@example.test": "{}", "track@example.test": "{}",
		}
	case "invalid":
		input.JobSenders, input.ApplicationSenders = nil, nil
		wantCode, committed = "EMAIL_CONNECTION_INVALID", false
	case "missing":
		db := settingsDatabase(t, homePath, false)
		if _, err := db.Exec(`DELETE FROM job_email_connections WHERE connection_id='primary'`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		wantCode, committed = "EMAIL_CONNECTION_NOT_CONFIGURED", false
	case "disabled":
		db := settingsDatabase(t, homePath, false)
		if _, err := db.Exec(`UPDATE job_email_connections SET enabled=0 WHERE connection_id='primary'`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		wantCode, committed = "EMAIL_CREDENTIAL_MISSING", false
	case "revision":
		input.ExpectedRevision = 2
		wantCode, committed = "EMAIL_CONNECTION_REVISION_CONFLICT", false
	case "account":
		input.AccountAddress = "other@example.test"
		wantCode, committed = "EMAIL_CONNECTION_ACCOUNT_IMMUTABLE", false
	case "missing-secret":
		delete(backend.values, "CareerHub/dev/provider/"+before.SecretName)
		wantCode, committed = "EMAIL_CREDENTIAL_MISSING", false
	case "invalid-target":
		db := settingsDatabase(t, homePath, false)
		if _, err := db.Exec(`UPDATE job_email_connections SET secret_name='arbitrary' WHERE connection_id='primary'`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		before.SecretName = "arbitrary"
		wantCode, committed = "EMAIL_CONNECTION_INVALID", false
	case "rollback":
		hooks.beforeCommit = func() *Error { return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED") }
		wantCode, committed = "EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED", false
	case "committed-but-error":
		hooks.commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("private post-commit details")
		}
		wantCode = "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED"
	case "gates":
		hooks.beforeCommit = func() *Error {
			wait, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
			defer cancel()
			if release, err := emailgate.Acquire(wait); err == nil {
				release()
				t.Fatal("email gate was released during settings edit")
			}
			if release, err := operationgate.Acquire(wait); err == nil {
				release()
				t.Fatal("general gate was released during settings edit")
			}
			return nil
		}
	default:
		t.Fatalf("unknown settings mode %q", mode)
	}
	backend.reads = nil
	result, problem := editSettingsWithHooks(ctx, homePath, backend, input, hooks)
	if mode == "v1" && len(backend.reads) != 0 {
		t.Fatal("settings edit read an unbound credential")
	}
	if wantCode == "" {
		if problem != nil || !result.Committed || result.Revision != 2 || result.RevisionRefreshRequired {
			t.Fatalf("settings result %+v %v", result, problem)
		}
	} else if problem == nil || problem.Code != wantCode {
		t.Fatalf("settings failure %+v %v; want %s", result, problem, wantCode)
	}
	if mode == "rollback" && (result.CommitAttempted || result.Committed || result.RevisionRefreshRequired) {
		t.Fatalf("known rollback misreported: %+v", result)
	}
	if mode == "committed-but-error" && (!result.CommitAttempted || result.Committed || !result.RevisionRefreshRequired) {
		t.Fatalf("unknown commit misreported: %+v", result)
	}
	if backend.writes != 0 || backend.deletes != 0 {
		t.Fatalf("settings edit mutated credentials: writes=%d deletes=%d", backend.writes, backend.deletes)
	}
	root, pathProblem := runtimepaths.Open(homePath)
	if pathProblem != nil {
		t.Fatal(pathProblem.Code)
	}
	_, intentPresent, pathProblem := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	root.Close()
	if pathProblem != nil || intentPresent {
		t.Fatalf("settings edit published credential intent: %v", pathProblem)
	}
	after, account, scheduled, gotInterval, lastUID, receipts, testStatus := settingsState(t, homePath)
	if mode != "missing" {
		wantRevision := int64(1)
		if committed {
			wantRevision = 2
		}
		if after.Revision != wantRevision || after.SecretName != before.SecretName || account != "alerts@example.test" || lastUID != 12 || receipts != 1 || testStatus != "not_tested" {
			t.Fatalf("settings changed identity/history: %+v %q %d %d %q", after, account, lastUID, receipts, testStatus)
		}
		if committed && (scheduled != 1 || gotInterval != 60) || !committed && (scheduled != 0 || gotInterval != 30) {
			t.Fatalf("settings changed schedule unexpectedly: %d %d", scheduled, gotInterval)
		}
		expectedPolicy := originalPolicy
		if mode == "override-policy" {
			expectedPolicy = emailrouting.EmptyPolicy().CanonicalJSON()
		}
		assertSettingsRules(t, homePath, originalPolicy, expectedPolicy, committed)
		if settingsSenderCreated(t, homePath, "jobs@example.test") != originalCreated {
			t.Fatal("existing sender creation time changed")
		}
	}
	assertSecret(t, store, unrelated, true)
	if mode == "v1" {
		assertSecret(t, store, emailcredentials.LegacyName, true)
	} else if mode != "missing-secret" && mode != "invalid-target" {
		assertSecret(t, store, before.SecretName, true)
	}
	if mode == "invalid-target" {
		assertSecret(t, store, originalTarget, true)
	}
}

func TestEditSettingsOwnedSubprocess(t *testing.T) {
	if mode := os.Getenv("CAREERHUB_EMAIL_SETTINGS_MODE"); mode != "" {
		runSettingsScenario(t, mode)
		return
	}
	for _, mode := range []string{"success", "v1", "override-policy", "invalid", "missing", "disabled", "revision", "account", "missing-secret", "invalid-target", "rollback", "committed-but-error", "gates"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEditSettingsOwnedSubprocess$")
			command.Env = append(os.Environ(), "CAREERHUB_EMAIL_SETTINGS_MODE="+mode,
				"CAREERHUB_EMAIL_SETTINGS_HOME="+filepath.Join(t.TempDir(), "home"))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("synthetic settings %s failed: %v %.1200s", mode, err, output)
			}
		})
	}
}
