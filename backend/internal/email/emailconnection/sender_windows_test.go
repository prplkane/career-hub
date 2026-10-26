//go:build windows

package emailconnection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

func TestOwnedEmailSenderDisposition(t *testing.T) {
	if os.Getenv("CAREERHUB_SENDER_MUTATION_CHILD") == "1" {
		checkOwnedEmailSenderDisposition(t, os.Getenv("CAREERHUB_SENDER_MUTATION_HOME"))
		return
	}
	homePath := filepath.Join(t.TempDir(), "home")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnedEmailSenderDisposition$", "-test.v")
	child.Env = append(os.Environ(), "CAREERHUB_SENDER_MUTATION_CHILD=1", "CAREERHUB_SENDER_MUTATION_HOME="+homePath)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("synthetic sender mutation child failed: %v %.2500s", err, output)
	}
	home, admission := datahome.Inspect(homePath, nil)
	if admission != nil || home == nil {
		t.Fatalf("synthetic Home missing after child: %v", admission)
	}
	if _, problem := SetSenderDisposition(context.Background(), homePath, *home, SenderDispositionInput{
		SenderAddress: "alerts@example.test", Disposition: senderAllowed, ExpectedRevision: 7,
	}); problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("unclaimed parent sender edit was not denied: %v", problem)
	}
}

func checkOwnedEmailSenderDisposition(t *testing.T, homePath string) {
	t.Helper()
	ctx := context.Background()
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	initial := SenderDispositionInput{SenderAddress: " Alerts@Example.Test ", Disposition: senderAllowed, ExpectedRevision: 1}
	if _, problem := SetSenderDisposition(ctx, homePath, *home, initial); problem == nil || problem.Code != "EMAIL_CONNECTION_NOT_CONFIGURED" {
		t.Fatalf("unconfigured sender edit accepted: %v", problem)
	}
	db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(homePath, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO job_email_connections (
		connection_id, provider, account_address, folder, secret_name,
		enabled, lookback_days, max_messages_per_sync, revision,
		created_at, updated_at
	) VALUES ('primary', 'gmail_imap', 'jobs@example.test', 'INBOX',
		'job-discovery.gmail-imap.primary.v1', 1, 14, 250, 1, 't0', 't0')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO job_email_message_receipts (
		connection_id, folder, uidvalidity, message_uid, sender_address,
		status, error_code, first_seen_at, updated_at
	) VALUES ('primary', 'INBOX', '55', 7, 'alerts@example.test',
		'unknown_sender', '', 't0', 't0')`); err != nil {
		t.Fatal(err)
	}
	if _, problem := SetSenderDisposition(ctx, homePath, *home, SenderDispositionInput{
		SenderAddress: "two@example.test", Disposition: "observe", ExpectedRevision: 1,
	}); problem == nil || problem.Code != "EMAIL_CONNECTION_INVALID" {
		t.Fatalf("invalid disposition accepted: %v", problem)
	}
	badPurpose := ""
	if _, problem := SetSenderDisposition(ctx, homePath, *home, SenderDispositionInput{
		SenderAddress: "two@example.test", Disposition: senderAllowed, Purpose: &badPurpose, ExpectedRevision: 1,
	}); problem == nil || problem.Code != "EMAIL_CONNECTION_INVALID" {
		t.Fatalf("empty explicit purpose accepted: %v", problem)
	}
	badPolicy := `{"job_alerts":{"include_any":["new jobs"]}}`
	if _, problem := SetSenderDisposition(ctx, homePath, *home, SenderDispositionInput{
		SenderAddress: "two@example.test", Disposition: senderAllowed, SubjectPolicyJSON: &badPolicy, ExpectedRevision: 1,
	}); problem == nil || problem.Code != "EMAIL_CONNECTION_INVALID" {
		t.Fatalf("incomplete policy accepted: %v", problem)
	}
	if _, problem := setSenderDispositionWithHooks(ctx, homePath, *home, initial, senderDispositionHooks{
		beforeCommit: func() *Error { return failure("EMAIL_CONNECTION_TEST_ABORT") },
	}); problem == nil || problem.Code != "EMAIL_CONNECTION_TEST_ABORT" {
		t.Fatalf("known precommit failure not returned: %v", problem)
	}
	assertSenderState(t, db, 1, "t0", "alerts@example.test", false, "", "", "", "")
	when := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	result, problem := setSenderDispositionWithHooks(ctx, homePath, *home, initial, senderDispositionHooks{now: func() time.Time { return when }})
	if problem != nil || !result.CommitAttempted || !result.Committed || result.Revision != 2 || result.RevisionRefreshRequired {
		t.Fatalf("new exact sender not committed: result=%+v problem=%v", result, problem)
	}
	emptyPolicy := emailrouting.EmptyPolicy().CanonicalJSON()
	assertSenderState(t, db, 2, "2026-08-14T12:00:00Z", "alerts@example.test", true,
		senderAllowed, emailrouting.SenderPurposeJobAlerts, emptyPolicy, "2026-08-14T12:00:00Z")
	if _, problem := SetSenderDisposition(ctx, homePath, *home, initial); problem == nil || problem.Code != "EMAIL_CONNECTION_REVISION_CONFLICT" {
		t.Fatalf("stale revision replayed: %v", problem)
	}
	staleBadPolicy := `{"job_alerts":{"include_any":["new jobs"]}}`
	if _, problem := SetSenderDisposition(ctx, homePath, *home, SenderDispositionInput{
		SenderAddress: "alerts@example.test", Disposition: senderAllowed,
		SubjectPolicyJSON: &staleBadPolicy, ExpectedRevision: 1,
	}); problem == nil || problem.Code != "EMAIL_CONNECTION_REVISION_CONFLICT" {
		t.Fatalf("stale revision did not precede policy validation: %v", problem)
	}
	policy := `{"job_alerts":{"include_any":["New JOBS"],"exclude_any":["security-alert"]},"application_tracking":{"include_any":[],"exclude_any":[]}}`
	both := emailrouting.SenderPurposeBoth
	updated := SenderDispositionInput{SenderAddress: "alerts@example.test", Disposition: senderAllowed,
		Purpose: &both, SubjectPolicyJSON: &policy, ExpectedRevision: 2}
	if result, problem := SetSenderDisposition(ctx, homePath, *home, updated); problem != nil || !result.Committed || result.Revision != 3 {
		t.Fatalf("sender policy update failed: result=%+v problem=%v", result, problem)
	}
	canonical, err := emailrouting.ParsePolicyJSON(policy)
	if err != nil {
		t.Fatal(err)
	}
	assertSenderState(t, db, 3, "", "alerts@example.test", true,
		senderAllowed, both, canonical.CanonicalJSON(), "2026-08-14T12:00:00Z")
	application := emailrouting.SenderPurposeApplicationTracking
	if result, problem := SetSenderDisposition(ctx, homePath, *home, SenderDispositionInput{
		SenderAddress: "alerts@example.test", Disposition: senderDisabled, Purpose: &application, ExpectedRevision: 3,
	}); problem != nil || !result.Committed || result.Revision != 4 {
		t.Fatalf("disable existing sender failed: result=%+v problem=%v", result, problem)
	}
	assertSenderState(t, db, 4, "", "alerts@example.test", true,
		senderDisabled, both, canonical.CanonicalJSON(), "2026-08-14T12:00:00Z")
	if result, problem := SetSenderDisposition(ctx, homePath, *home, SenderDispositionInput{
		SenderAddress: "alerts@example.test", Disposition: senderAllowed, Purpose: &application, ExpectedRevision: 4,
	}); problem != nil || !result.Committed || result.Revision != 5 {
		t.Fatalf("re-allow existing sender failed: result=%+v problem=%v", result, problem)
	}
	assertSenderState(t, db, 5, "", "alerts@example.test", true,
		senderAllowed, application, canonical.CanonicalJSON(), "2026-08-14T12:00:00Z")
	if result, problem := SetSenderDisposition(ctx, homePath, *home, SenderDispositionInput{
		SenderAddress: "ignored@example.test", Disposition: senderDisabled, ExpectedRevision: 5,
	}); problem != nil || !result.Committed || result.Revision != 6 {
		t.Fatalf("disable new sender failed: result=%+v problem=%v", result, problem)
	}
	assertSenderState(t, db, 6, "", "ignored@example.test", true,
		senderDisabled, emailrouting.SenderPurposeJobAlerts, emptyPolicy, "")
	unknown, problem := setSenderDispositionWithHooks(ctx, homePath, *home, SenderDispositionInput{
		SenderAddress: "third@example.test", Disposition: senderDisabled, ExpectedRevision: 6,
	}, senderDispositionHooks{commit: func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return errors.New("synthetic report after commit")
	}})
	if problem == nil || problem.Code != "EMAIL_CONNECTION_COMMIT_UNCONFIRMED" || !unknown.CommitAttempted || unknown.Committed || !unknown.RevisionRefreshRequired {
		t.Fatalf("reported-after-commit sender fact misclassified: result=%+v problem=%v", unknown, problem)
	}
	assertSenderState(t, db, 7, "", "third@example.test", true,
		senderDisabled, emailrouting.SenderPurposeJobAlerts, emptyPolicy, "")
	var receipts int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_email_message_receipts WHERE connection_id='primary'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("sender edit changed receipt history: count=%d err=%v", receipts, err)
	}
	for i := 0; i < 99; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO job_email_sender_rules (
			connection_id, sender_address, disposition, purpose,
			subject_policy_json, created_at, updated_at
		) VALUES ('primary', ?, 'allowed', 'job_alerts', ?, 't0', 't0')`,
			fmt.Sprintf("synthetic%03d@example.test", i), emptyPolicy); err != nil {
			t.Fatal(err)
		}
	}
	if _, problem := SetSenderDisposition(ctx, homePath, *home, SenderDispositionInput{
		SenderAddress: "excess@example.test", Disposition: senderAllowed, ExpectedRevision: 7,
	}); problem == nil || problem.Code != "EMAIL_CONNECTION_INVALID" {
		t.Fatalf("101st approved sender accepted: %v", problem)
	}
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT revision FROM job_email_connections WHERE connection_id='primary'`).Scan(&revision); err != nil || revision != 7 {
		t.Fatalf("rejected sender changed revision: revision=%d err=%v", revision, err)
	}
}

func assertSenderState(t *testing.T, db *sql.DB, wantRevision int64, wantUpdated, sender string, wantPresent bool, wantDisposition, wantPurpose, wantPolicy, wantCreated string) {
	t.Helper()
	var revision int64
	var updated string
	if err := db.QueryRow(`SELECT revision, updated_at FROM job_email_connections WHERE connection_id='primary'`).Scan(&revision, &updated); err != nil || revision != wantRevision || wantUpdated != "" && updated != wantUpdated {
		t.Fatalf("connection revision=%d updated=%s err=%v want=%d", revision, updated, err, wantRevision)
	}
	var disposition, purpose, policy, created string
	err := db.QueryRow(`SELECT disposition, purpose, subject_policy_json, created_at
		FROM job_email_sender_rules WHERE connection_id='primary' AND sender_address=?`, sender).Scan(&disposition, &purpose, &policy, &created)
	if !wantPresent {
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("sender %s unexpectedly present: %v", sender, err)
		}
		return
	}
	if err != nil || disposition != wantDisposition || purpose != wantPurpose || policy != wantPolicy || wantCreated != "" && created != wantCreated {
		t.Fatalf("sender %s disposition=%s purpose=%s policy=%s created=%s err=%v", sender, disposition, purpose, policy, created, err)
	}
}
