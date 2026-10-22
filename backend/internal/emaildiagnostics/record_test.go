package emaildiagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/emailworker"
)

func decoded(t *testing.T, event Event) map[string]any {
	t.Helper()
	raw := event.Bytes()
	if len(raw) == 0 || raw[len(raw)-1] != '\n' || len(raw) > MaxEventBytes {
		t.Fatal("event is not one bounded JSONL record")
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSourceShapedEmailRunEvents(t *testing.T) {
	current := time.Date(2026, 9, 14, 2, 30, 0, 123456000, time.UTC)
	run, err := newWithClock(" 20260914T023000.123456Z_deadbeef ", func() time.Time { return current },
		func([]byte) (int, error) { t.Fatal("valid correlation ID used entropy"); return 0, nil })
	if err != nil || run.ID() != "20260914T023000.123456Z_deadbeef" {
		t.Fatalf("valid source header lost: %q %v", run.ID(), err)
	}
	startedRaw, err := run.Started()
	if err != nil {
		t.Fatal(err)
	}
	started := decoded(t, startedRaw)
	if started["schema_version"] != float64(1) || started["timestamp"] != "2026-09-14T02:30:00.123456+00:00" ||
		started["run_id"] != run.ID() || started["sequence"] != float64(1) ||
		started["component"] != "career_vault_intake" || started["source"] != "gmail_sync" ||
		started["stage"] != "email_sync_run" || started["event"] != "started" || started["status"] != "running" ||
		started["details"].(map[string]any)["operation"] != "sync" {
		t.Fatal("source start event changed", started)
	}
	if _, err := run.Started(); !errors.Is(err, ErrState) {
		t.Fatal("duplicate start was accepted", err)
	}
	current = current.Add(1234 * time.Millisecond)
	result := emailworker.Result{
		ContractVersion: emailworker.ContractVersion, Status: "partial", ErrorCode: "EMAIL_SYNC_MESSAGE_RETRY",
		Counts:            emailworker.Counts{UIDsFound: 2, MessagesImported: 1, MessagesRetryPending: 1, DiscoveriesWritten: 1},
		FailureCodeCounts: map[string]int{"EMAIL_IMPORT_DISCOVERY_FAILED": 1}, HasMore: true,
	}
	finishedRaw, err := run.Completed("manual", result)
	if err != nil {
		t.Fatal(err)
	}
	finished := decoded(t, finishedRaw)
	details := finished["details"].(map[string]any)
	counts := details["counts"].(map[string]any)
	if finished["sequence"] != float64(2) || finished["event"] != "completed" || finished["status"] != "success_with_warnings" ||
		details["trigger"] != "manual" || details["result_status"] != "partial" || details["error_code"] != "EMAIL_SYNC_MESSAGE_RETRY" ||
		details["total_duration_ms"] != float64(1234) || details["has_more"] != true ||
		counts["uids_found"] != float64(2) || counts["messages_retry_pending"] != float64(1) ||
		details["failure_code_counts"].(map[string]any)["EMAIL_IMPORT_DISCOVERY_FAILED"] != float64(1) ||
		details["summary"] != "Gmail was reached, but one or more messages could not yet be imported." {
		t.Fatal("source partial event changed", finished)
	}
	if strings.Contains(string(startedRaw.Bytes())+string(finishedRaw.Bytes()), "synthetic-app-password") ||
		strings.Contains(string(startedRaw.Bytes())+string(finishedRaw.Bytes()), "alerts@example.test") {
		t.Fatal("private mailbox values reached diagnostics")
	}
	if _, err := run.Completed("manual", result); !errors.Is(err, ErrState) {
		t.Fatal("duplicate terminal event was accepted", err)
	}
}

func TestEmailRunFallbackAndFixedFailure(t *testing.T) {
	current := time.Date(2026, 9, 14, 2, 31, 0, 0, time.UTC)
	run, err := newWithClock("private@example.test", func() time.Time { return current }, func(buf []byte) (int, error) {
		copy(buf, []byte{0x12, 0x34, 0xab, 0xcd})
		return len(buf), nil
	})
	if err != nil || run.ID() != "20260914T023100.000000Z_1234abcd" || !ValidRunID(run.ID()) {
		t.Fatalf("invalid client header was not replaced: %q %v", run.ID(), err)
	}
	start, err := run.Started()
	if err != nil || decoded(t, start)["timestamp"] != "2026-09-14T02:31:00+00:00" {
		t.Fatal("zero-microsecond source timestamp changed", err)
	}
	if _, err := run.Failed("EMAIL_SYNC_AUTH_FAILED", "private@example.test"); !errors.Is(err, ErrState) {
		t.Fatal("private failed stage was accepted", err)
	}
	current = current.Add(10 * time.Millisecond)
	failed, err := run.Failed("EMAIL_SYNC_AUTH_FAILED", "mailbox")
	if err != nil {
		t.Fatal(err)
	}
	value := decoded(t, failed)
	details := value["details"].(map[string]any)
	if value["event"] != "failed" || value["status"] != "error" || value["sequence"] != float64(2) ||
		details["error_code"] != "EMAIL_SYNC_AUTH_FAILED" || details["failed_stage"] != "mailbox" ||
		details["total_duration_ms"] != float64(10) || details["severity"] != "warning" ||
		details["retryable"] != true || details["summary"] != "Gmail rejected the saved App Password." ||
		bytes.Contains(failed.Bytes(), []byte("private@example.test")) {
		t.Fatal("fixed failure event changed or leaked client header", value)
	}
	if _, err := run.Failed("EMAIL_SYNC_AUTH_FAILED", "mailbox"); !errors.Is(err, ErrState) {
		t.Fatal("duplicate failed event was accepted", err)
	}
}

func TestEmailRunRejectsMalformedSummariesWithoutAdvancing(t *testing.T) {
	current := time.Date(2026, 9, 14, 2, 32, 0, 500000000, time.UTC)
	run, err := newWithClock("20260914T023200.500000Z_cafebabe", func() time.Time { return current },
		func([]byte) (int, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Started(); err != nil {
		t.Fatal(err)
	}
	bad := emailworker.Result{ContractVersion: 2, Status: "partial", ErrorCode: "EMAIL_SYNC_MESSAGE_RETRY",
		FailureCodeCounts: map[string]int{"private@example.test": 1}}
	if _, err := run.Completed("manual", bad); !errors.Is(err, ErrResult) {
		t.Fatal("private failure-code key was accepted", err)
	}
	bad.FailureCodeCounts = map[string]int{}
	bad.Counts.UIDsFound = emailworker.MaxMessagesPerSync + 1
	if _, err := run.Completed("manual", bad); !errors.Is(err, ErrResult) {
		t.Fatal("unbounded count was accepted", err)
	}
	bad.Counts.UIDsFound = 0
	if _, err := run.Completed("manual", bad); err != nil {
		t.Fatal("valid summary did not complete after refused input", err)
	}
	if _, err := newWithClock("", func() time.Time { return current }, func([]byte) (int, error) {
		return 0, nil
	}); !errors.Is(err, ErrState) {
		t.Fatal("missing entropy did not fail closed", err)
	}
}

func TestEmailRunPreservesSanitizedUnknownCode(t *testing.T) {
	run, err := newWithClock("20260914T023200.500000Z_cafebabe", time.Now,
		func([]byte) (int, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Started(); err != nil {
		t.Fatal(err)
	}
	terminal, err := run.Failed("EMAIL_CONNECTION_DATABASE_UNAVAILABLE", "readback")
	if err != nil {
		t.Fatal(err)
	}
	details := decoded(t, terminal)["details"].(map[string]any)
	if details["error_code"] != "EMAIL_CONNECTION_DATABASE_UNAVAILABLE" ||
		details["summary"] != failureGuidance["EMAIL_SYNC_FAILED"].summary {
		t.Fatal("public error code and generic guidance diverged", details)
	}
}
