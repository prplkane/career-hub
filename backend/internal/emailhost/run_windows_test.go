//go:build windows

package emailhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/emailgate"
	"careerhub.local/backend/internal/emailworker"
	"careerhub.local/backend/internal/mailtransport"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sqlitestore"
)

const fixturePassword = "SYNTHETIC-SECRET-DO-NOT-LOG"

type memorySecrets struct {
	values  map[string][]byte
	readErr error
	onRead  func() error
}

func (s *memorySecrets) Write(target string, value []byte) error {
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	s.values[target] = bytes.Clone(value)
	return nil
}
func (s *memorySecrets) Read(target string) ([]byte, bool, error) {
	if s.readErr != nil {
		return nil, false, s.readErr
	}
	if s.onRead != nil {
		if err := s.onRead(); err != nil {
			return nil, false, err
		}
	}
	value, found := s.values[target]
	return bytes.Clone(value), found, nil
}
func (s *memorySecrets) Delete(target string) (bool, error) {
	_, found := s.values[target]
	delete(s.values, target)
	return found, nil
}

type oneMessage struct {
	raw    []byte
	closed bool
	since  time.Time
}

func (*oneMessage) UIDValidity() string { return "42" }
func (m *oneMessage) SearchUIDs(_ context.Context, start uint32, since time.Time) ([]uint32, error) {
	m.since = since
	if start <= 1 {
		return []uint32{1, 2}, nil
	}
	if start == 2 {
		return []uint32{2}, nil
	}
	return nil, nil
}
func (m *oneMessage) FetchHeaders(_ context.Context, uid uint32) (mailtransport.Headers, error) {
	end := bytes.Index(m.raw, []byte("\r\n\r\n"))
	width := 4
	if end < 0 {
		end = bytes.Index(m.raw, []byte("\n\n"))
		width = 2
	}
	if uid != 1 || end < 0 {
		return mailtransport.Headers{}, errors.New("synthetic header failure")
	}
	return mailtransport.Headers{UID: uid, SizeBytes: int64(len(m.raw)), RawHeaders: m.raw[:end+width]}, nil
}
func (m *oneMessage) FetchMessage(_ context.Context, uid uint32) ([]byte, error) {
	if uid != 1 {
		return nil, errors.New("synthetic body failure")
	}
	return bytes.Clone(m.raw), nil
}
func (m *oneMessage) Close() error { m.closed = true; return nil }

func hostSQL(t *testing.T, home, query string) {
	t.Helper()
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

func hostCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want || strings.Contains(err.Error(), fixturePassword) {
		t.Fatalf("host error: %v; want %s", err, want)
	}
}

func runHostFixture(t *testing.T) {
	t.Helper()
	ctx, home := context.Background(), os.Getenv("CAREERHUB_EMAIL_HOST_HOME")
	info, startupErr := datahome.BootstrapForProcess(ctx, home, "dev")
	if startupErr != nil {
		t.Fatal(startupErr.Code)
	}
	if _, err := schema.PrepareOwned(ctx, home); err != nil {
		t.Fatal(err)
	}
	owner := applicationwrite.New(home)
	backend := &memorySecrets{}
	secrets, err := secretstore.New("dev", backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.Set(applicationwrite.EmailPrimarySecretName, fixturePassword); err != nil {
		t.Fatal(err)
	}
	openCalls := 0
	rejectOpen := func(context.Context, string, string, string) (emailworker.Mailbox, error) {
		openCalls++
		return nil, errors.New("unexpected mailbox open")
	}
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_CONNECTION_NOT_CONFIGURED")
	hostSQL(t, home, `INSERT INTO job_email_connections
		(connection_id,provider,account_address,folder,secret_name,enabled,
		lookback_days,max_messages_per_sync,revision,created_at,updated_at)
		VALUES ('primary','gmail_imap','alerts@example.test','INBOX',
		'job-discovery.gmail-imap.primary.v1',1,14,250,1,'t0','t0')`)
	hostSQL(t, home, `INSERT INTO job_email_sync_state (connection_id,folder) VALUES ('primary','INBOX')`)
	hostSQL(t, home, `INSERT INTO job_email_sender_rules
		(connection_id,sender_address,disposition,purpose,subject_policy_json,created_at,updated_at)
		VALUES ('primary','empty@example.test','allowed','both','{}','t0','t0')`)
	releaseEmail, err := emailgate.TryAcquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secretReads := 0
	backend.onRead = func() error { secretReads++; return nil }
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_SYNC_ALREADY_RUNNING")
	releaseEmail()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = run(canceled, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_SYNC_CANCELED")
	backend.onRead = nil
	if secretReads != 0 || openCalls != 0 {
		t.Fatal("overlapping or canceled host reached a secret or mailbox")
	}
	backend.onRead = func() error { secretReads++; return nil }
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_CREDENTIAL_RECONNECT_REQUIRED")
	if secretReads != 0 || openCalls != 0 {
		t.Fatal("legacy target reached secret store or mailbox")
	}
	backend.onRead = nil
	root, pathError := runtimepaths.Open(home)
	if pathError != nil {
		t.Fatal(pathError)
	}
	defer root.Close()
	scope, pathError := root.CredentialScope()
	if pathError != nil {
		t.Fatal(pathError)
	}
	target, err := emailcredentials.NewScopedGenerationName(*info, scope)
	if err != nil {
		t.Fatal(err)
	}
	hostSQL(t, home, `UPDATE job_email_connections SET secret_name='`+target+`' WHERE connection_id='primary'`)
	if _, err := secrets.Delete(target); err != nil {
		t.Fatal(err)
	}
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_CREDENTIAL_MISSING")
	otherChannelSecrets, err := secretstore.New("stable", backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := otherChannelSecrets.Set(target, fixturePassword); err != nil {
		t.Fatal(err)
	}
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_CREDENTIAL_MISSING")
	backend.readErr = errors.New("private backend text")
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_SECRET_STORE_FAILED")
	backend.readErr = nil
	if err := secrets.Set(target, fixturePassword); err != nil {
		t.Fatal(err)
	}
	hostSQL(t, home, `UPDATE job_email_connections SET enabled=0 WHERE connection_id='primary'`)
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_SYNC_DISABLED")
	hostSQL(t, home, `UPDATE job_email_connections SET enabled=1,secret_name='another-target' WHERE connection_id='primary'`)
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_CONNECTION_INVALID")
	hostSQL(t, home, `UPDATE job_email_connections SET secret_name='`+target+`',folder='OTHER' WHERE connection_id='primary'`)
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_CONNECTION_INVALID")
	hostSQL(t, home, `UPDATE job_email_connections SET folder='INBOX',account_address='' WHERE connection_id='primary'`)
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_CONNECTION_INVALID")
	hostSQL(t, home, `UPDATE job_email_connections SET account_address='alerts@example.test',max_messages_per_sync=1 WHERE connection_id='primary'`)
	markerPath := filepath.Join(home, datahome.MarkerPath)
	originalMarker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	replacementID := "dh_11111111111111111111111111111111"
	if info.DataHomeID == replacementID {
		replacementID = "dh_22222222222222222222222222222222"
	}
	swappedMarker := bytes.Replace(originalMarker, []byte(info.DataHomeID), []byte(replacementID), 1)
	if bytes.Equal(swappedMarker, originalMarker) {
		t.Fatal("synthetic Home marker did not contain its ID")
	}
	backend.onRead = func() error { return os.WriteFile(markerPath, swappedMarker, 0600) }
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_SYNC_LOCAL_WRITE_FAILED")
	backend.onRead = nil
	if openCalls != 0 {
		t.Fatal("replaced Home reached mailbox opener")
	}
	guardRead, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	var beforeReceipts, beforeCursor int
	var beforeLease string
	if err := guardRead.QueryRow(`SELECT COUNT(*) FROM job_email_message_receipts`).Scan(&beforeReceipts); err != nil {
		t.Fatal(err)
	}
	if err := guardRead.QueryRow(`SELECT last_uid,lease_owner FROM job_email_sync_state WHERE connection_id='primary' AND folder='INBOX'`).Scan(&beforeCursor, &beforeLease); err != nil {
		t.Fatal(err)
	}
	if err := guardRead.Close(); err != nil || beforeReceipts != 0 || beforeCursor != 0 || beforeLease != "" {
		t.Fatalf("replaced Home changed sync state: receipts=%d cursor=%d lease=%q err=%v", beforeReceipts, beforeCursor, beforeLease, err)
	}
	if err := os.WriteFile(markerPath, originalMarker, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := run(ctx, owner, backend, "scheduled", rejectOpen)
	if err != nil || result.Status != "disabled" || openCalls != 0 {
		t.Fatalf("scheduled disabled: %+v %v; opens=%d", result, err, openCalls)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "email_alerts", "no_job_links.eml"))
	if err != nil {
		t.Fatal(err)
	}
	mailbox := &oneMessage{raw: raw}
	workerSecretReads := 0
	backend.onRead = func() error { workerSecretReads++; return nil }
	result, err = run(ctx, owner, backend, "manual", func(_ context.Context, account, password, folder string) (emailworker.Mailbox, error) {
		openCalls++
		if account != "alerts@example.test" || password != fixturePassword || folder != "INBOX" {
			t.Fatal("mailbox received changed configuration or secret")
		}
		_, nestedError := run(ctx, owner, backend, "manual", rejectOpen)
		hostCode(t, nestedError, "EMAIL_SYNC_ALREADY_RUNNING")
		if workerSecretReads != 1 {
			t.Fatal("overlapping worker accessed the credential")
		}
		return mailbox, nil
	})
	backend.onRead = nil
	lookbackAge := time.Since(mailbox.since)
	if err != nil || result.Status != "succeeded" || !result.HasMore || result.Counts.UIDsFound != 2 || result.Counts.BodiesFetched != 1 ||
		result.Counts.MessagesWithoutCandidates != 1 || result.Counts.MessagesImported != 0 ||
		openCalls != 1 || !mailbox.closed || lookbackAge < 14*24*time.Hour || lookbackAge > 16*24*time.Hour {
		t.Fatalf("native host run: %+v %v; opens=%d closed=%v lookback=%s", result, err, openCalls, mailbox.closed, lookbackAge)
	}
	db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	var receiptCount, cursor int
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_email_message_receipts WHERE status='no_candidates'`).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT last_uid FROM job_email_sync_state WHERE connection_id='primary' AND folder='INBOX'`).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil || receiptCount != 1 || cursor != 1 {
		t.Fatalf("native receipt/cursor: %d/%d %v", receiptCount, cursor, err)
	}
	v2Name, err := emailcredentials.NewGenerationName(*info)
	if err != nil {
		t.Fatal(err)
	}
	wrongHomeName := strings.Replace(v2Name, info.DataHomeID[3:], strings.Repeat("f", 32), 1)
	if wrongHomeName == v2Name {
		wrongHomeName = strings.Replace(v2Name, info.DataHomeID[3:], strings.Repeat("e", 32), 1)
	}
	hostSQL(t, home, `UPDATE job_email_connections SET secret_name='`+wrongHomeName+`' WHERE connection_id='primary'`)
	previousOpenCalls := openCalls
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_CONNECTION_INVALID")
	if openCalls != previousOpenCalls {
		t.Fatal("cross-Home target reached mailbox opener")
	}
	hostSQL(t, home, `UPDATE job_email_connections SET secret_name='`+v2Name+`' WHERE connection_id='primary'`)
	backend.onRead = func() error { secretReads++; return nil }
	readsBeforeUnbound := secretReads
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_CREDENTIAL_RECONNECT_REQUIRED")
	if secretReads != readsBeforeUnbound || openCalls != previousOpenCalls {
		t.Fatal("v2 credential read or mailbox opened")
	}
	backend.onRead = nil
	foreignParts := strings.Split(target, ".")
	foreignParts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hostSQL(t, home, `UPDATE job_email_connections SET secret_name='`+strings.Join(foreignParts, ".")+`' WHERE connection_id='primary'`)
	backend.onRead = func() error { secretReads++; return nil }
	_, err = run(ctx, owner, backend, "manual", rejectOpen)
	hostCode(t, err, "EMAIL_CREDENTIAL_RECONNECT_REQUIRED")
	if secretReads != readsBeforeUnbound || openCalls != previousOpenCalls {
		t.Fatal("foreign v3 credential read or mailbox opened")
	}
	backend.onRead = nil
	newTarget, err := emailcredentials.NewScopedGenerationName(*info, scope)
	if err != nil {
		t.Fatal(err)
	}
	hostSQL(t, home, `UPDATE job_email_connections SET secret_name='`+newTarget+`' WHERE connection_id='primary'`)
	const newPassword = fixturePassword + "-V3"
	if err := secrets.Set(newTarget, newPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Delete(target); err != nil {
		t.Fatal(err)
	}
	_, err = run(ctx, owner, backend, "manual", func(_ context.Context, account, password, folder string) (emailworker.Mailbox, error) {
		if account != "alerts@example.test" || password != newPassword || folder != "INBOX" {
			t.Fatal("v3 credential or configuration mismatch")
		}
		return nil, &mailtransport.Error{Code: "EMAIL_SYNC_NETWORK_FAILED"}
	})
	hostCode(t, err, "EMAIL_SYNC_NETWORK_FAILED")
	config, err := owner.EmailSyncState().ConnectionConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*applicationwrite.EmailConnectionConfig){
		func(c *applicationwrite.EmailConnectionConfig) { c.Revision++ },
		func(c *applicationwrite.EmailConnectionConfig) { c.Scope = runtimepaths.CredentialScope{} },
		func(c *applicationwrite.EmailConnectionConfig) { c.AccountAddress = "changed@example.test" },
	} {
		stale := config
		change(&stale)
		called := false
		err := owner.EmailSyncState().WithConnectionConfig(ctx, stale, func(applicationwrite.EmailConnectionConfig) error { called = true; return nil })
		hostCode(t, err, "EMAIL_CONNECTION_CHANGED")
		if called {
			t.Fatal("stale configuration reached effect callback")
		}
	}
	postOpenCtx, cancelPostOpen := context.WithCancel(ctx)
	defer cancelPostOpen()
	openedThenRejected := &oneMessage{raw: raw}
	_, err = run(postOpenCtx, owner, backend, "manual", func(context.Context, string, string, string) (emailworker.Mailbox, error) {
		cancelPostOpen()
		return openedThenRejected, nil
	})
	hostCode(t, err, "EMAIL_SYNC_CANCELED")
	if !openedThenRejected.closed || !openedThenRejected.since.IsZero() {
		t.Fatal("mailbox survived failed post-open admission or was scanned")
	}
}

func TestEmailHostOwnedHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_EMAIL_HOST_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	runHostFixture(t)
}

func TestEmailHostNativeComposition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmailHostOwnedHelper$")
	command.Env = append(os.Environ(), "CAREERHUB_EMAIL_HOST_HELPER=1",
		"CAREERHUB_EMAIL_HOST_HOME="+filepath.Join(t.TempDir(), "home"))
	command.WaitDelay = time.Second
	var output, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostic
	if err := command.Run(); err != nil {
		t.Fatalf("native email host: %v stdout=%.1200s stderr=%.400s", err, output.String(), diagnostic.String())
	}
}
