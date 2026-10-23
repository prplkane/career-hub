//go:build windows

package emailimport_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailalerts"
	"careerhub.local/backend/internal/email/emailimport"
	"careerhub.local/backend/internal/email/emailworker"
	"careerhub.local/backend/internal/email/mailtransport"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

type fixtureMailbox struct {
	messages          map[uint32][]byte
	messageFetchCount int
}

func (*fixtureMailbox) UIDValidity() string { return "41" }

func (m *fixtureMailbox) SearchUIDs(_ context.Context, start uint32, _ time.Time) ([]uint32, error) {
	var ids []uint32
	for uid := range m.messages {
		if uid >= start {
			ids = append(ids, uid)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (m *fixtureMailbox) FetchHeaders(_ context.Context, uid uint32) (mailtransport.Headers, error) {
	raw, found := m.messages[uid]
	if !found {
		return mailtransport.Headers{}, errors.New("EMAIL_SYNC_PROTOCOL_FAILED")
	}
	end, width := bytes.Index(raw, []byte("\r\n\r\n")), 4
	if end < 0 {
		end, width = bytes.Index(raw, []byte("\n\n")), 2
	}
	if end < 0 {
		return mailtransport.Headers{}, errors.New("EMAIL_SYNC_PROTOCOL_FAILED")
	}
	return mailtransport.Headers{UID: uid, SizeBytes: int64(len(raw)), RawHeaders: raw[:end+width]}, nil
}

func (m *fixtureMailbox) FetchMessage(_ context.Context, uid uint32) ([]byte, error) {
	raw, found := m.messages[uid]
	if !found {
		return nil, errors.New("EMAIL_SYNC_PROTOCOL_FAILED")
	}
	m.messageFetchCount++
	return raw, nil
}

func (*fixtureMailbox) Close() error { return nil }

func nativeWorkerSeed(t *testing.T, home string) {
	t.Helper()
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`INSERT INTO job_email_connections
			(connection_id,provider,account_address,folder,secret_name,enabled,
			lookback_days,max_messages_per_sync,revision,created_at,updated_at)
			VALUES ('primary','gmail_imap','alerts@example.test','INBOX',
			'synthetic-no-secret',1,14,10,1,'t0','t0')`,
		`INSERT INTO job_email_sync_state (connection_id,folder) VALUES ('primary','INBOX')`,
		`INSERT INTO job_email_sender_rules
			(connection_id,sender_address,disposition,purpose,subject_policy_json,created_at,updated_at)
			VALUES ('primary','jobs-noreply@linkedin.com','allowed','both','{}','t0','t0')`,
		`INSERT INTO job_applications
			(job_id,company,title,source_url,content_fingerprint,current_status,created_at,updated_at)
			VALUES ('synthetic-linkedin-job','Synthetic Commerce','Data Engineer',
			'https://www.linkedin.com/jobs/view/4451000001/',
			'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			'applied','t0','t0')`,
		`INSERT INTO application_status_events (job_id,status,occurred_at,recorded_at,note)
			VALUES ('synthetic-linkedin-job','applied','t0','t0','')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func runNativeMailbox(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	home := os.Getenv("CAREERHUB_NATIVE_EMAIL_HOME")
	if _, e := datahome.BootstrapForProcess(ctx, home, "dev"); e != nil {
		t.Fatalf("synthetic bootstrap: %s", e.Code)
	}
	if _, err := schema.PrepareOwned(ctx, home); err != nil {
		t.Fatalf("synthetic schema: %v", err)
	}
	nativeWorkerSeed(t, home)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "email_alerts", "linkedin_rejection_with_suggestions.eml"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := emailalerts.Parse(raw)
	if err != nil || len(parsed.Candidates) != 2 || len(parsed.ApplicationOutcomes) != 1 {
		t.Fatalf("mixed fixture changed: candidates=%d outcomes=%d err=%v", len(parsed.Candidates), len(parsed.ApplicationOutcomes), err)
	}
	mailbox := &fixtureMailbox{messages: map[uint32][]byte{1: raw}}
	owner := applicationwrite.New(home)
	importer := emailimport.Importer{Writer: owner}
	open := func(context.Context) (emailworker.Mailbox, error) { return mailbox, nil }
	config := func(at time.Time) emailworker.Config {
		return emailworker.Config{
			Trigger: "manual", LookbackDays: 14, MaxMessages: 10,
			Now: func() time.Time { return at },
		}
	}
	first, err := emailworker.Run(ctx, owner.EmailSyncState(), open, importer,
		config(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)))
	if err != nil || first.Status != "succeeded" || first.Counts.MessagesImported != 1 ||
		first.Counts.CandidatesFound != 2 || first.Counts.DiscoveriesWritten != 2 ||
		first.Counts.ApplicationOutcomesFound != 1 || first.Counts.ApplicationsUpdated != 1 ||
		first.Counts.BodiesFetched != 1 {
		t.Fatalf("first full-stack sync: %+v err=%v", first, err)
	}
	mailbox.messages[2] = raw
	second, err := emailworker.Run(ctx, owner.EmailSyncState(), open, importer,
		config(time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC)))
	if err != nil || second.Status != "succeeded" || second.Counts.MessagesDuplicate != 1 ||
		second.Counts.BodiesFetched != 0 || mailbox.messageFetchCount != 1 {
		t.Fatalf("same Message-ID replay: %+v body-fetches=%d err=%v", second, mailbox.messageFetchCount, err)
	}
	read, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	var status, resolution, matched, epoch, receipt1, receipt2 string
	var discoveries, applications, events, outcomes, cursor, verified, linkOnly int
	for _, query := range []struct {
		statement string
		args      []any
	}{
		{`SELECT current_status FROM job_applications WHERE job_id='synthetic-linkedin-job'`, []any{&status}},
		{`SELECT COUNT(*) FROM job_applications`, []any{&applications}},
		{`SELECT COUNT(*) FROM application_status_events`, []any{&events}},
		{`SELECT COUNT(*),COALESCE(SUM(CASE WHEN content_verified_at <> '' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN content_scope = 'link_only' THEN 1 ELSE 0 END),0)
			FROM job_discoveries`, []any{&discoveries, &verified, &linkOnly}},
		{`SELECT COUNT(*) FROM job_email_application_outcomes`, []any{&outcomes}},
		{`SELECT resolution,matched_job_id FROM job_email_application_outcomes`, []any{&resolution, &matched}},
		{`SELECT uidvalidity,last_uid FROM job_email_sync_state WHERE connection_id='primary' AND folder='INBOX'`, []any{&epoch, &cursor}},
		{`SELECT status FROM job_email_message_receipts WHERE message_uid=1`, []any{&receipt1}},
		{`SELECT status FROM job_email_message_receipts WHERE message_uid=2`, []any{&receipt2}},
	} {
		if err := read.QueryRow(query.statement).Scan(query.args...); err != nil {
			t.Fatalf("ledger query failed: %v", err)
		}
	}
	if status != "rejected" || applications != 1 || events != 2 || discoveries != 2 || verified != 0 || linkOnly != 2 ||
		outcomes != 1 || resolution != "application_updated" || matched != "synthetic-linkedin-job" ||
		epoch != "41" || cursor != 2 || receipt1 != "imported" || receipt2 != "duplicate" {
		t.Fatalf("ledger facts: status=%q apps=%d events=%d discoveries=%d verified=%d link_only=%d outcomes=%d resolution=%q match=%q epoch=%q cursor=%d receipts=%q/%q",
			status, applications, events, discoveries, verified, linkOnly, outcomes, resolution, matched, epoch, cursor, receipt1, receipt2)
	}
}

// This deliberately slow probe is opt-in. It measures the real guarded state
// owner under the normal 250-message configuration without requiring Gmail.
func runNativeThroughput(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	home := os.Getenv("CAREERHUB_NATIVE_EMAIL_HOME")
	if _, e := datahome.BootstrapForProcess(ctx, home, "dev"); e != nil {
		t.Fatalf("synthetic bootstrap: %s", e.Code)
	}
	if _, err := schema.PrepareOwned(ctx, home); err != nil {
		t.Fatalf("synthetic schema: %v", err)
	}
	nativeWorkerSeed(t, home)
	db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO job_email_sender_rules
		(connection_id,sender_address,disposition,purpose,subject_policy_json,created_at,updated_at)
		VALUES ('primary','empty@example.test','allowed','both','{}','t0','t0')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "email_alerts", "no_job_links.eml"))
	if err != nil {
		t.Fatal(err)
	}
	mailbox := &fixtureMailbox{messages: make(map[uint32][]byte, 250)}
	for uid := uint32(1); uid <= 250; uid++ {
		mailbox.messages[uid] = bytes.ReplaceAll(raw, []byte("<empty-404@example.test>"),
			[]byte(fmt.Sprintf("<synthetic-%d@example.test>", uid)))
	}
	owner := applicationwrite.New(home)
	started := time.Now()
	result, err := emailworker.Run(ctx, owner.EmailSyncState(),
		func(context.Context) (emailworker.Mailbox, error) { return mailbox, nil },
		emailimport.Importer{Writer: owner}, emailworker.Config{
			Trigger: "manual", LookbackDays: 14, MaxMessages: 250,
			Now: func() time.Time { return time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC) },
		})
	elapsed := time.Since(started)
	if err != nil || result.Status != "succeeded" || result.HasMore ||
		result.Counts.UIDsFound != 250 || result.Counts.HeadersFetched != 250 ||
		result.Counts.BodiesFetched != 250 || result.Counts.MessagesWithoutCandidates != 250 ||
		result.Counts.MessagesImported != 0 {
		t.Fatalf("250-message run: elapsed=%s result=%+v err=%v", elapsed, result, err)
	}
	read, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	var receipts, cursor int
	if err := read.QueryRow(`SELECT COUNT(*) FROM job_email_message_receipts WHERE status='no_candidates'`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := read.QueryRow(`SELECT last_uid FROM job_email_sync_state WHERE connection_id='primary' AND folder='INBOX'`).Scan(&cursor); err != nil || receipts != 250 || cursor != 250 {
		t.Fatalf("250-message durability: receipts=%d cursor=%d err=%v", receipts, cursor, err)
	}
	t.Logf("native 250-message worker elapsed=%s; receipts=%d cursor=%d", elapsed, receipts, cursor)
}

func TestNativeWorkerOwnedHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_NATIVE_EMAIL_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	if os.Getenv("CAREERHUB_NATIVE_EMAIL_MODE") == "throughput" {
		runNativeThroughput(t)
		return
	}
	runNativeMailbox(t)
}

func TestNativeWorkerThroughOwners(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeWorkerOwnedHelper$")
	command.Env = append(os.Environ(), "CAREERHUB_NATIVE_EMAIL_HELPER=1",
		"CAREERHUB_NATIVE_EMAIL_HOME="+filepath.Join(t.TempDir(), "home"))
	command.WaitDelay = time.Second
	var output, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostic
	if err := command.Run(); err != nil {
		t.Fatalf("native worker fixture: %v stdout=%.1000s stderr=%.300s", err, output.String(), diagnostic.String())
	}
}

func TestNativeWorkerNormalLimit(t *testing.T) {
	if os.Getenv("CAREERHUB_EMAIL_THROUGHPUT") != "1" {
		t.Skip("opt-in native throughput probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeWorkerOwnedHelper$", "-test.v")
	command.Env = append(os.Environ(), "CAREERHUB_NATIVE_EMAIL_HELPER=1",
		"CAREERHUB_NATIVE_EMAIL_MODE=throughput",
		"CAREERHUB_NATIVE_EMAIL_HOME="+filepath.Join(t.TempDir(), "home"))
	command.WaitDelay = time.Second
	var output, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostic
	if err := command.Run(); err != nil {
		t.Fatalf("native throughput fixture: %v stdout=%.1100s stderr=%.300s", err, output.String(), diagnostic.String())
	}
	t.Logf("%s", output.String())
}
