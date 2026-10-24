package emaildiagnostics

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"careerhub.local/backend/internal/email/emailworker"
)

func TestConnectionRunIsFixedAndContentMinimized(t *testing.T) {
	current := time.Date(2026, 9, 14, 4, 0, 0, 123456000, time.UTC)
	run, err := newConnectionWithClock("20260914T040000.123456Z_deadbeef", func() time.Time { return current }, func([]byte) (int, error) {
		t.Fatal("valid run ID should not consume entropy")
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := run.Started()
	if err != nil {
		t.Fatal(err)
	}
	value := decoded(t, started)
	if value["source"] != "gmail_connection" || value["stage"] != "email_connection_run" ||
		value["details"].(map[string]any)["operation"] != "save" {
		t.Fatal("connection run was not fixed to the source operation", value)
	}
	if _, valid := decodeEvent(started.Bytes()); !valid {
		t.Fatal("minted connection start was rejected by the journal")
	}
	if _, err := run.Completed("manual", emailworker.Result{}); !errors.Is(err, ErrState) {
		t.Fatal("connection run accepted sync completion", err)
	}
	current = current.Add(10 * time.Millisecond)
	finished, err := run.ConnectionCompleted(1, true)
	if err != nil {
		t.Fatal(err)
	}
	details := decoded(t, finished)["details"].(map[string]any)
	if details["revision"] != float64(1) || details["credential_supplied"] != true || details["total_duration_ms"] != float64(10) {
		t.Fatal("completed connection lost safe facts", details)
	}
	if _, valid := decodeEvent(finished.Bytes()); !valid {
		t.Fatal("minted connection completion was rejected by the journal")
	}
	if _, err := run.ConnectionFailed("EMAIL_CONNECTION_FAILED", "readback", true, true, false); !errors.Is(err, ErrState) {
		t.Fatal("connection run accepted a second terminal", err)
	}
}

func TestConnectionFailureRejectsPrivateAndContradictoryFacts(t *testing.T) {
	run, err := NewConnection("20260914T040001.123456Z_deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Started(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		code, stage          string
		attempted, committed bool
	}{
		{"private@example.test", "readback", false, false},
		{"EMAIL_CONNECTION_FAILED", "private@example.test", false, false},
		{"EMAIL_CONNECTION_FAILED", "readback", false, true},
	} {
		if _, err := run.ConnectionFailed(tc.code, tc.stage, tc.attempted, tc.committed, false); !errors.Is(err, ErrState) {
			t.Fatal("unsafe connection failure accepted", tc, err)
		}
	}
	failed, err := run.ConnectionFailed("EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED", "local_persistence", true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	details := decoded(t, failed)["details"].(map[string]any)
	if details["commit_attempted"] != true || details["committed"] != false || details["recovery_required"] != true ||
		bytes.Contains(failed.Bytes(), []byte("private@example.test")) {
		t.Fatal("failure lost uncertain-commit facts or included private input", details)
	}
	if _, valid := decodeEvent(failed.Bytes()); !valid {
		t.Fatal("minted connection failure was rejected by the journal")
	}
}

func TestJournalRejectsTamperedDetailsAndNoncanonicalEvents(t *testing.T) {
	run, err := NewConnection("20260914T040002.123456Z_deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	started, err := run.Started()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{
		bytes.Replace(started.Bytes(), []byte(`"operation":"save"`), []byte(`"operation":"private@example.test"`), 1),
		bytes.Replace(started.Bytes(), []byte(`"operation":"save"`), []byte(`"operation":"save","password":"synthetic-secret"`), 1),
		append([]byte(" "), started.Bytes()...),
	} {
		if _, valid := decodeEvent(raw); valid {
			t.Fatalf("tampered native event accepted: %.120s", raw)
		}
	}
}
