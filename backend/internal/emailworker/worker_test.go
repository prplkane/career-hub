package emailworker

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"careerhub.local/backend/internal/emailalerts"
	"careerhub.local/backend/internal/emailmime"
	"careerhub.local/backend/internal/emailreceipts"
	"careerhub.local/backend/internal/emailrouting"
	"careerhub.local/backend/internal/mailtransport"
	"careerhub.local/backend/internal/sqlitestore"
)

type sqliteState struct {
	db            *sql.DB
	failCalls     int
	unknownRecord bool
}

func newSQLiteState(t *testing.T) *sqliteState {
	t.Helper()
	path := filepath.Join(t.TempDir(), "worker.sqlite3")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range []string{
		`CREATE TABLE job_email_connections (connection_id TEXT PRIMARY KEY,
			last_tested_at TEXT,last_test_status TEXT NOT NULL DEFAULT 'not_tested',
			last_error_code TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL DEFAULT 't0')`,
		`CREATE TABLE job_email_sync_state (connection_id TEXT NOT NULL,folder TEXT NOT NULL,
			uidvalidity TEXT NOT NULL DEFAULT '',last_uid INTEGER NOT NULL DEFAULT 0,
			last_started_at TEXT,last_completed_at TEXT,last_result TEXT NOT NULL DEFAULT 'never',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,next_retry_at TEXT,
			last_error_code TEXT NOT NULL DEFAULT '',revision INTEGER NOT NULL DEFAULT 1,
			lease_owner TEXT NOT NULL DEFAULT '',lease_expires_at TEXT,
			PRIMARY KEY(connection_id,folder))`,
		`CREATE TABLE job_email_sender_rules (connection_id TEXT NOT NULL,sender_address TEXT NOT NULL,
			disposition TEXT NOT NULL,purpose TEXT NOT NULL,subject_policy_json TEXT NOT NULL DEFAULT '{}',
			PRIMARY KEY(connection_id,sender_address))`,
		`CREATE TABLE job_email_message_receipts (receipt_id INTEGER PRIMARY KEY AUTOINCREMENT,
			connection_id TEXT NOT NULL,folder TEXT NOT NULL,uidvalidity TEXT NOT NULL,message_uid INTEGER NOT NULL,
			message_id_hash TEXT NOT NULL DEFAULT '',content_sha256 TEXT NOT NULL DEFAULT '',
			sender_address TEXT NOT NULL DEFAULT '',status TEXT NOT NULL,
			attempt_count INTEGER NOT NULL DEFAULT 1,candidate_count INTEGER NOT NULL DEFAULT 0,
			discovery_count INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '',
			first_seen_at TEXT NOT NULL,updated_at TEXT NOT NULL,message_sent_date TEXT NOT NULL DEFAULT '',
			subject_job_signal INTEGER NOT NULL DEFAULT 0,subject_service_signal INTEGER NOT NULL DEFAULT 0,
			subject_application_signal INTEGER NOT NULL DEFAULT 0,subject_route TEXT NOT NULL DEFAULT '',
			UNIQUE(connection_id,folder,uidvalidity,message_uid))`,
		`INSERT INTO job_email_connections(connection_id) VALUES('primary')`,
		`INSERT INTO job_email_sync_state(connection_id,folder) VALUES('primary','INBOX')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return &sqliteState{db: db}
}

func (s *sqliteState) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteState) addRule(t *testing.T, address, purpose, policy string) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO job_email_sender_rules
		(connection_id,sender_address,disposition,purpose,subject_policy_json)
		VALUES('primary',?,'allowed',?,?)`, address, purpose, policy)
	if err != nil {
		t.Fatal(err)
	}
}

func (s *sqliteState) Acquire(ctx context.Context, started, owner string, scheduled bool) (string, error) {
	var deferred string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var e error
		deferred, e = emailreceipts.AcquireLease(ctx, tx, started, owner, scheduled)
		return e
	})
	return deferred, err
}
func (s *sqliteState) SetUIDValidity(ctx context.Context, epoch string) (bool, error) {
	var changed bool
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var e error
		changed, e = emailreceipts.SetUIDValidity(ctx, tx, epoch)
		return e
	})
	return changed, err
}
func (s *sqliteState) Position(ctx context.Context) (string, uint32, error) {
	var epoch string
	var uid uint32
	err := s.tx(ctx, func(tx *sql.Tx) error { var e error; epoch, uid, e = emailreceipts.Position(ctx, tx); return e })
	return epoch, uid, err
}
func (s *sqliteState) AllowedRules(ctx context.Context) (map[string]emailrouting.Rule, error) {
	var rules map[string]emailrouting.Rule
	err := s.tx(ctx, func(tx *sql.Tx) error { var e error; rules, e = emailrouting.LoadAllowed(ctx, tx); return e })
	return rules, err
}
func (s *sqliteState) PendingRetryUIDs(ctx context.Context, epoch string, allowed map[string]bool) ([]uint32, error) {
	var uids []uint32
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var e error
		uids, e = emailreceipts.PendingRetryUIDs(ctx, tx, epoch, allowed)
		return e
	})
	return uids, err
}
func (s *sqliteState) Receipt(ctx context.Context, epoch string, uid uint32) (*emailreceipts.Receipt, error) {
	var receipt *emailreceipts.Receipt
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var e error
		receipt, e = emailreceipts.ByTransport(ctx, tx, epoch, uid)
		return e
	})
	return receipt, err
}
func (s *sqliteState) IdentityProcessed(ctx context.Context, field, value, epoch string, uid uint32) (bool, error) {
	var found bool
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var e error
		found, e = emailreceipts.IdentityWasProcessed(ctx, tx, field, value, epoch, uid)
		return e
	})
	return found, err
}
func (s *sqliteState) Record(ctx context.Context, input emailreceipts.RecordInput) (int, error) {
	if s.unknownRecord {
		s.unknownRecord = false
		return 0, ErrCommitUnknown
	}
	var attempts int
	err := s.tx(ctx, func(tx *sql.Tx) error { var e error; attempts, e = emailreceipts.Record(ctx, tx, input); return e })
	return attempts, err
}
func (s *sqliteState) Complete(ctx context.Context, owner, at string, issues, retry bool) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		_, e := emailreceipts.MarkCompleted(ctx, tx, owner, at, issues, retry)
		return e
	})
}
func (s *sqliteState) Fail(ctx context.Context, owner, code, at string) error {
	s.failCalls++
	return s.tx(ctx, func(tx *sql.Tx) error { _, e := emailreceipts.MarkFailed(ctx, tx, owner, code, at); return e })
}

type fakeMailbox struct {
	epoch                   string
	messages                map[uint32][]byte
	reported                map[uint32]int64
	headers, bodies, starts []uint32
	closed                  bool
}

func (m *fakeMailbox) UIDValidity() string { return m.epoch }
func (m *fakeMailbox) SearchUIDs(_ context.Context, start uint32, _ time.Time) ([]uint32, error) {
	m.starts = append(m.starts, start)
	uids := make([]uint32, 0)
	for uid := range m.messages {
		if uid >= start {
			uids = append(uids, uid)
		}
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	return uids, nil
}
func (m *fakeMailbox) FetchHeaders(_ context.Context, uid uint32) (mailtransport.Headers, error) {
	m.headers = append(m.headers, uid)
	message, ok := m.messages[uid]
	if !ok {
		return mailtransport.Headers{}, errors.New("EMAIL_SYNC_MESSAGE_MISSING")
	}
	breakAt := bytes.Index(message, []byte("\r\n\r\n"))
	separator := 4
	if breakAt < 0 {
		breakAt = bytes.Index(message, []byte("\n\n"))
		separator = 2
	}
	if breakAt < 0 {
		return mailtransport.Headers{}, errors.New("EMAIL_SYNC_PROTOCOL_FAILED")
	}
	size := int64(len(message))
	if reported, ok := m.reported[uid]; ok {
		size = reported
	}
	return mailtransport.Headers{UID: uid, SizeBytes: size, RawHeaders: message[:breakAt+separator]}, nil
}
func (m *fakeMailbox) FetchMessage(_ context.Context, uid uint32) ([]byte, error) {
	m.bodies = append(m.bodies, uid)
	message, ok := m.messages[uid]
	if !ok {
		return nil, errors.New("EMAIL_SYNC_MESSAGE_MISSING")
	}
	return message, nil
}
func (m *fakeMailbox) Close() error { m.closed = true; return nil }
func openFake(m *fakeMailbox) OpenMailbox {
	return func(context.Context) (Mailbox, error) { return m, nil }
}

type importFunc func(context.Context, emailalerts.Result, string) (ImportResult, error)

func (f importFunc) Import(ctx context.Context, result emailalerts.Result, purpose string) (ImportResult, error) {
	return f(ctx, result, purpose)
}

func simpleMessage(sender, subject, id string) []byte {
	return []byte(fmt.Sprintf("From: %s\r\nDate: Fri, 14 Aug 2026 08:30:00 -0400\r\nMessage-ID: <%s>\r\nSubject: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nSynthetic body.\r\n", sender, id, subject))
}
func config(at time.Time) Config {
	return Config{Trigger: "manual", LookbackDays: 14, MaxMessages: 250, Now: func() time.Time { return at }}
}
func at(hour, minute int) time.Time { return time.Date(2026, 8, 14, hour, minute, 0, 0, time.UTC) }
func run(t *testing.T, state *sqliteState, mailbox *fakeMailbox, importer Importer, cfg Config) Result {
	t.Helper()
	result, err := Run(context.Background(), state, openFake(mailbox), importer, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSourceImportOnceAndEpochDedup(t *testing.T) {
	t.Parallel()
	state := newSQLiteState(t)
	state.addRule(t, "alerts@example.test", "job_alerts", "{}")
	message, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "email_alerts", "single_great_match_html.eml"))
	if err != nil {
		t.Fatal(err)
	}
	mailbox := &fakeMailbox{epoch: "101", messages: map[uint32][]byte{1: message}}
	imports := 0
	importer := importFunc(func(_ context.Context, parsed emailalerts.Result, purpose string) (ImportResult, error) {
		imports++
		if purpose != "job_alerts" || len(parsed.Candidates) != 1 {
			t.Fatalf("wrong import route/candidates: %q %d", purpose, len(parsed.Candidates))
		}
		return ImportResult{Status: "imported", CandidateCount: 1, DiscoveryCount: 1}, nil
	})
	first := run(t, state, mailbox, importer, config(at(12, 0)))
	second := run(t, state, mailbox, importer, config(at(12, 5)))
	if first.Counts.MessagesImported != 1 || first.Counts.DiscoveriesWritten != 1 || second.Counts.UIDsFound != 0 || imports != 1 ||
		!reflect.DeepEqual(mailbox.bodies, []uint32{1}) || !reflect.DeepEqual(mailbox.starts, []uint32{1, 2}) {
		t.Fatalf("first=%+v second=%+v imports=%d bodies=%v starts=%v", first, second, imports, mailbox.bodies, mailbox.starts)
	}
	mailbox.epoch = "102"
	third := run(t, state, mailbox, importer, config(at(12, 10)))
	if !third.UIDValidityReset || third.Counts.MessagesDuplicate != 1 || imports != 1 || len(mailbox.bodies) != 1 {
		t.Fatalf("epoch reset did not dedup: %+v imports=%d bodies=%v", third, imports, mailbox.bodies)
	}
}

func TestSourceRetryDoesNotBlockNewerAndFinalizes(t *testing.T) {
	t.Parallel()
	state := newSQLiteState(t)
	state.addRule(t, "alerts@example.test", "both", "{}")
	valid, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "email_alerts", "single_great_match_html.eml"))
	if err != nil {
		t.Fatal(err)
	}
	mailbox := &fakeMailbox{epoch: "101", messages: map[uint32][]byte{
		1: simpleMessage("alerts@example.test", "Private failing alert", "failing@example.test"), 2: valid,
	}}
	importer := importFunc(func(_ context.Context, parsed emailalerts.Result, _ string) (ImportResult, error) {
		if parsed.Subject == "Private failing alert" {
			return ImportResult{Status: "failed", ErrorCode: "EMAIL_IMPORT_DISCOVERY_FAILED"}, nil
		}
		return ImportResult{Status: "imported", CandidateCount: 1, DiscoveryCount: 1}, nil
	})
	first := run(t, state, mailbox, importer, config(at(14, 0)))
	if first.Status != "partial" || first.ErrorCode != "EMAIL_SYNC_MESSAGE_RETRY" || first.Counts.MessagesImported != 1 || first.Counts.MessagesRetryPending != 1 || first.Counts.DiscoveriesWritten != 1 || first.FailureCodeCounts["EMAIL_IMPORT_DISCOVERY_FAILED"] != 1 {
		t.Fatalf("first=%+v", first)
	}
	scheduled := config(at(14, 2))
	scheduled.Trigger = "scheduled"
	scheduled.ScheduleEnabled = true
	deferred := run(t, state, mailbox, importer, scheduled)
	if deferred.Status != "deferred" || deferred.DeferredUntil != "2026-08-14T14:05:00Z" {
		t.Fatalf("deferred=%+v", deferred)
	}
	second := run(t, state, mailbox, importer, config(at(14, 5)))
	third := run(t, state, mailbox, importer, config(at(14, 10)))
	if second.Counts.MessagesRetryPending != 1 || third.Counts.MessagesFailedFinal != 1 || third.ErrorCode != "EMAIL_SYNC_MESSAGE_SKIPPED" ||
		!reflect.DeepEqual(mailbox.bodies, []uint32{1, 2, 1, 1}) {
		t.Fatalf("second=%+v third=%+v bodies=%v", second, third, mailbox.bodies)
	}
	var status string
	var attempts, failures int
	if err := state.db.QueryRow(`SELECT status,attempt_count FROM job_email_message_receipts WHERE message_uid=1`).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRow(`SELECT consecutive_failures FROM job_email_sync_state WHERE connection_id='primary'`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if status != "failed_final" || attempts != 3 || failures != 0 {
		t.Fatalf("terminal retry: status=%q attempts=%d failures=%d", status, attempts, failures)
	}
}

func TestSourceHeaderGateAndTimeBudget(t *testing.T) {
	t.Parallel()
	state := newSQLiteState(t)
	state.addRule(t, "alerts@example.test", "both", "{}")
	policy := `{"job_alerts":{"include_any":["New jobs"],"exclude_any":[]},"application_tracking":{"include_any":["Application update"],"exclude_any":[]}}`
	state.addRule(t, "jobs@example.test", "job_alerts", policy)
	mailbox := &fakeMailbox{epoch: "101", messages: map[uint32][]byte{
		1: simpleMessage("unknown@example.test", "New jobs", "unknown@example.test"),
		2: simpleMessage("jobs@example.test", "Security notice", "filtered@example.test"),
		3: simpleMessage("alerts@example.test", "New jobs", "large@example.test"),
	}, reported: map[uint32]int64{3: emailmime.MaxEmailBytes + 1}}
	importer := importFunc(func(context.Context, emailalerts.Result, string) (ImportResult, error) {
		t.Fatal("header gate fetched/imported a body")
		return ImportResult{}, nil
	})
	result := run(t, state, mailbox, importer, config(at(12, 0)))
	if result.Status != "partial" || result.ErrorCode != "EMAIL_SYNC_MESSAGE_SKIPPED" || result.Counts.HeadersFetched != 3 || result.Counts.BodiesFetched != 0 ||
		result.Counts.MessagesUnknownSender != 1 || result.Counts.MessagesFilteredSubject != 1 || result.Counts.MessagesTooLarge != 1 || len(mailbox.bodies) != 0 {
		t.Fatalf("header gate: %+v bodies=%v", result, mailbox.bodies)
	}
	for uid, want := range map[uint32]string{1: "unknown_sender", 2: "no_candidates", 3: "too_large"} {
		var got string
		if err := state.db.QueryRow(`SELECT status FROM job_email_message_receipts WHERE message_uid=?`, uid).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("uid=%d status=%q want=%q", uid, got, want)
		}
	}
	// The next pass finds no new UIDs; a separate new epoch proves the 120s cap.
	mailbox.epoch = "102"
	cfg := config(at(13, 0))
	cfg.Elapsed = func() time.Duration { return MaxSyncDuration }
	limited := run(t, state, mailbox, importer, cfg)
	if !limited.HasMore || limited.Counts.HeadersFetched != 0 || limited.Counts.UIDsFound != 3 {
		t.Fatalf("duration cap: %+v", limited)
	}
}

func TestUncertainImportOrReceiptNeverMarksLeaseFailed(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"import", "receipt"} {
		t.Run(stage, func(t *testing.T) {
			state := newSQLiteState(t)
			state.addRule(t, "alerts@example.test", "both", "{}")
			mailbox := &fakeMailbox{epoch: "101", messages: map[uint32][]byte{1: simpleMessage("alerts@example.test", "New jobs", "uncertain@example.test")}}
			if stage == "receipt" {
				state.unknownRecord = true
			}
			importer := importFunc(func(context.Context, emailalerts.Result, string) (ImportResult, error) {
				if stage == "import" {
					return ImportResult{}, ErrCommitUnknown
				}
				return ImportResult{Status: "no_candidates"}, nil
			})
			_, err := Run(context.Background(), state, openFake(mailbox), importer, config(at(12, 0)))
			if err == nil || err.Error() != ErrCommitUnknown.Error() || state.failCalls != 0 {
				t.Fatalf("uncertain %s: err=%v failCalls=%d", stage, err, state.failCalls)
			}
			var result, owner string
			if e := state.db.QueryRow(`SELECT last_result,lease_owner FROM job_email_sync_state WHERE connection_id='primary'`).Scan(&result, &owner); e != nil {
				t.Fatal(e)
			}
			if result != "running" || owner == "" {
				t.Fatalf("uncertain %s lost lease: result=%q owner=%q", stage, result, owner)
			}
		})
	}
}

func TestTerminalReceiptAdvancesCursorWithoutRefetch(t *testing.T) {
	t.Parallel()
	state := newSQLiteState(t)
	ctx := context.Background()
	if _, err := state.SetUIDValidity(ctx, "101"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Record(ctx, emailreceipts.RecordInput{
		UIDValidity: "101", UID: 1, SenderAddress: "alerts@example.test",
		Status: "imported", RecordedAt: "2026-08-14T11:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	mailbox := &fakeMailbox{epoch: "101", messages: map[uint32][]byte{
		1: simpleMessage("alerts@example.test", "New jobs", "already@example.test"),
	}}
	importer := importFunc(func(context.Context, emailalerts.Result, string) (ImportResult, error) {
		t.Fatal("terminal receipt must not import again")
		return ImportResult{}, nil
	})
	result := run(t, state, mailbox, importer, config(at(12, 0)))
	_, cursor, err := state.Position(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cursor != 1 || result.Counts.UIDsFound != 1 || result.Counts.HeadersFetched != 0 || len(mailbox.bodies) != 0 {
		t.Fatalf("terminal receipt: cursor=%d result=%+v bodies=%v", cursor, result, mailbox.bodies)
	}
}

func TestNewlyApprovedSenderReplaysWithoutNewUID(t *testing.T) {
	t.Parallel()
	state := newSQLiteState(t)
	state.addRule(t, "other@example.test", "job_alerts", "{}")
	mailbox := &fakeMailbox{epoch: "101", messages: map[uint32][]byte{
		1: simpleMessage("new@example.test", "New jobs", "new-sender@example.test"),
	}}
	imports := 0
	importer := importFunc(func(context.Context, emailalerts.Result, string) (ImportResult, error) {
		imports++
		return ImportResult{Status: "no_candidates"}, nil
	})
	first := run(t, state, mailbox, importer, config(at(12, 0)))
	state.addRule(t, "new@example.test", "job_alerts", "{}")
	second := run(t, state, mailbox, importer, config(at(12, 5)))
	if first.Counts.MessagesUnknownSender != 1 || second.Counts.UIDsFound != 1 ||
		second.Counts.MessagesWithoutCandidates != 1 || imports != 1 ||
		!reflect.DeepEqual(mailbox.starts, []uint32{1, 2}) ||
		!reflect.DeepEqual(mailbox.bodies, []uint32{1}) {
		t.Fatalf("new approval: first=%+v second=%+v imports=%d starts=%v bodies=%v", first, second, imports, mailbox.starts, mailbox.bodies)
	}
}
