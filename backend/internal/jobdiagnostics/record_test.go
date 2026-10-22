package jobdiagnostics

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fixedRun(t *testing.T, requested string) *Run {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 9, 14, 12, 30, 0, 123456000, time.UTC) }
	run, err := newWithClock(requested, "fictional-job", now, func(value []byte) (int, error) {
		return copy(value, []byte{0x12, 0x34, 0xab, 0xcd}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestIntakeNamespaceOnlyFailureRoundTrip(t *testing.T) {
	run := fixedIntakeRun(t, "20260920T154500.123456Z_4321dcba", "synthetic", "captured")
	if _, err := run.Started(); err != nil {
		t.Fatal(err)
	}
	dispatched, err := run.Dispatched()
	if err != nil {
		t.Fatal(err)
	}
	event, err := run.Failed("JOB_INTAKE_FILES_FAILED", "namespace", false)
	if err != nil {
		t.Fatal(err)
	}
	prior, got := decodedEvent(t, dispatched), decodedEvent(t, event)
	if !contiguous(&prior, got) || got.DurablePhase != "namespace" || got.RecoveryRequired {
		t.Fatal("namespace-only diagnostic lost", got)
	}
}

func fixedIntakeRun(t *testing.T, requested, source, initialStatus string) *IntakeRun {
	t.Helper()
	fingerprint := sha256.Sum256([]byte(source))
	now := func() time.Time { return time.Date(2026, 9, 20, 15, 45, 0, 123456000, time.UTC) }
	run, err := newIntakeWithClock(requested, hex.EncodeToString(fingerprint[:]), initialStatus, now, func(value []byte) (int, error) {
		return copy(value, []byte{0x43, 0x21, 0xdc, 0xba}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func decodedEvent(t *testing.T, event Event) Decoded {
	t.Helper()
	value, err := Decode(event.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSavedJDRunEventsAreFixedAndContentMinimized(t *testing.T) {
	run := fixedRun(t, "untrusted title or job ID")
	if run.ID() != "20260914T123000.123456Z_1234abcd" {
		t.Fatal("unsafe requested run ID was not replaced", run.ID())
	}
	if _, err := New("../unsafe/job"); err != ErrState {
		t.Fatal("unsafe job selector was accepted", err)
	}
	start, err := run.Started()
	if err != nil || decodedEvent(t, start).Event != "started" || decodedEvent(t, start).JobKeySHA256 != sha256.Sum256([]byte("fictional-job")) {
		t.Fatal("started event", err)
	}
	prior, next := sha256.Sum256([]byte("fictional prior card")), sha256.Sum256([]byte("fictional next card"))
	if _, err := run.Planned(prior, prior); err != ErrState {
		t.Fatal("indistinguishable publication plan was accepted", err)
	}
	plan, err := run.Planned(prior, next)
	if err != nil {
		t.Fatal(err)
	}
	parsed := decodedEvent(t, plan)
	if !parsed.HasPlan || parsed.PriorCardSHA256 != prior || parsed.NextCardSHA256 != next || parsed.Sequence != 2 {
		t.Fatal("planned witness lost", parsed)
	}
	otherRun, err := newWithClock(run.ID(), "other-fictional-job", func() time.Time {
		return time.Date(2026, 9, 14, 12, 30, 0, 123456000, time.UTC)
	}, func(value []byte) (int, error) { return copy(value, []byte{1, 2, 3, 4}), nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherRun.Started(); err != nil {
		t.Fatal(err)
	}
	otherPlan, err := otherRun.Planned(prior, next)
	if err != nil {
		t.Fatal(err)
	}
	startDecoded := decodedEvent(t, start)
	if contiguous(&startDecoded, decodedEvent(t, otherPlan)) {
		t.Fatal("one run accepted a plan for a different job key")
	}
	completed, err := run.Completed()
	if err != nil || !decodedEvent(t, completed).Committed {
		t.Fatal("successful publication facts missing", err)
	}
	if _, err := run.Failed("JOB_REANALYSIS_WRITE_UNCERTAIN", true, true); err != ErrState {
		t.Fatal("terminal run accepted another event", err)
	}
	for _, event := range []Event{start, plan, completed} {
		original := event.Bytes()
		if len(original) > MaxEventBytes || bytes.Contains(original, []byte("untrusted title")) || bytes.Contains(original, []byte("fictional-job")) || bytes.Contains(original, []byte("fictional prior card")) {
			t.Fatal("private text or oversized event reached codec")
		}
		original[0] = 'X'
		if _, err := Decode(event.Bytes()); err != nil {
			t.Fatal("event bytes were mutable", err)
		}
	}
	var public map[string]any
	if json.Unmarshal(completed.Bytes(), &public) != nil || public["source"] != "saved_job" || public["stage"] != "job_reanalysis_run" {
		t.Fatal("diagnostic source shape changed", public)
	}
	withPrivateField := bytes.Replace(start.Bytes(), []byte(`"operation":"reanalyze"`), []byte(`"operation":"reanalyze","job_title":"private"`), 1)
	if _, err := Decode(withPrivateField); err != ErrEvent {
		t.Fatal("private extra field was admitted", err)
	}
	badStatus := bytes.Replace(start.Bytes(), []byte(`"status":"running"`), []byte(`"status":"success"`), 1)
	if _, err := Decode(badStatus); err != ErrEvent {
		t.Fatal("invalid stage transition was admitted", err)
	}
}

func TestNewJobIntakeEventsAreFixedAndContentMinimized(t *testing.T) {
	const privateSource = "Private Example role at private.example with a signed URL"
	if _, err := NewIntake("not-a-fingerprint", "captured"); err != ErrState {
		t.Fatal("invalid source fingerprint was accepted", err)
	}
	validFingerprint := sha256.Sum256([]byte(privateSource))
	if _, err := NewIntake(hex.EncodeToString(validFingerprint[:]), "interview"); err != ErrState {
		t.Fatal("invalid initial status was accepted", err)
	}
	run := fixedIntakeRun(t, "unsafe requested id", privateSource, "captured")
	started, err := run.Started()
	if err != nil {
		t.Fatal(err)
	}
	decodedStart := decodedEvent(t, started)
	wantKey := sha256.Sum256([]byte(hex.EncodeToString(validFingerprint[:])))
	if decodedStart.Operation != "intake" || decodedStart.InitialStatus != "captured" || decodedStart.SourceKeySHA256 != wantKey || decodedStart.JobKeySHA256 != ([32]byte{}) {
		t.Fatal("intake start lost bounded identity", decodedStart)
	}
	if _, err := run.Completed("terminal", true); err != ErrState {
		t.Fatal("terminal success claimed duplicate", err)
	}
	dispatched, err := run.Dispatched()
	if err != nil {
		t.Fatal(err)
	}
	decodedDispatched := decodedEvent(t, dispatched)
	if !contiguous(&decodedStart, decodedDispatched) || decodedDispatched.Event != "dispatched" || decodedDispatched.Sequence != 2 {
		t.Fatal("intake dispatch boundary lost", decodedDispatched)
	}
	completed, err := run.Completed("terminal", false)
	if err != nil {
		t.Fatal(err)
	}
	decodedDone := decodedEvent(t, completed)
	if !contiguous(&decodedDispatched, decodedDone) || decodedDone.Operation != "intake" || decodedDone.Sequence != 3 || decodedDone.DurablePhase != "terminal" || decodedDone.Duplicate || decodedDone.RecoveryRequired {
		t.Fatal("intake terminal facts lost", decodedDone)
	}
	for _, event := range []Event{started, dispatched, completed} {
		raw := event.Bytes()
		if len(raw) > MaxEventBytes || bytes.Contains(raw, []byte(privateSource)) || bytes.Contains(raw, []byte(hex.EncodeToString(validFingerprint[:]))) {
			t.Fatal("private intake material reached diagnostic event")
		}
	}
	withPrivateField := bytes.Replace(started.Bytes(), []byte(`"initial_status":"captured"`), []byte(`"initial_status":"captured","source_url":"private"`), 1)
	if _, err := Decode(withPrivateField); err != ErrEvent {
		t.Fatal("private intake field was admitted", err)
	}
	if _, err := Interrupted(decodedStart, PublicationNotPublished); err != ErrState {
		t.Fatal("intake start was misclassified as reanalysis", err)
	}
	interruptedBeforeOwner, err := IntakeInterrupted(decodedStart, "none", false)
	if err != nil || !contiguous(&decodedStart, decodedEvent(t, interruptedBeforeOwner)) || decodedEvent(t, interruptedBeforeOwner).RecoveryRequired {
		t.Fatal("started-only intake interruption invented an owner effect", err)
	}

	failedRun := fixedIntakeRun(t, "20260920T154500.123456Z_4321dcba", privateSource, "applied")
	failedStart, err := failedRun.Started()
	if err != nil {
		t.Fatal(err)
	}
	failedDispatch, err := failedRun.Dispatched()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failedRun.Failed("private@example.test", "files", true); err != ErrState {
		t.Fatal("arbitrary intake error was accepted", err)
	}
	failed, err := failedRun.Failed("JOB_INTAKE_LEDGER_FAILED", "files", true)
	if err != nil {
		t.Fatal(err)
	}
	decodedFailed := decodedEvent(t, failed)
	failedPrevious := decodedEvent(t, failedDispatch)
	if !contiguous(&failedPrevious, decodedFailed) || decodedFailed.ErrorCode != "JOB_INTAKE_LEDGER_FAILED" || decodedFailed.DurablePhase != "files" || !decodedFailed.RecoveryRequired {
		t.Fatal("intake failure facts lost", decodedFailed)
	}
	if _, err := IntakeInterrupted(decodedEvent(t, failedStart), "files", true); err != ErrState {
		t.Fatal("started-only interruption claimed a dispatched owner", err)
	}
	dispatchedRun := fixedIntakeRun(t, "20260920T154500.123456Z_4321dcbb", privateSource, "captured")
	if _, err := dispatchedRun.Started(); err != nil {
		t.Fatal(err)
	}
	dispatchedEvent, err := dispatchedRun.Dispatched()
	if err != nil {
		t.Fatal(err)
	}
	interruptedAfterDispatch, err := IntakeInterrupted(decodedEvent(t, dispatchedEvent), "files", true)
	if err != nil || !decodedEvent(t, interruptedAfterDispatch).RecoveryRequired {
		t.Fatal("dispatched interruption lost recovery requirement", err)
	}
	reanalysis := fixedRun(t, failedRun.ID())
	reanalysisStart, err := reanalysis.Started()
	if err != nil {
		t.Fatal(err)
	}
	if contiguous(&failedPrevious, decodedEvent(t, reanalysisStart)) {
		t.Fatal("cross-operation event was admitted into an intake run")
	}
}

func TestSavedJDRunFailureAndCrashClassification(t *testing.T) {
	priorBytes, nextBytes := []byte("fictional existing card"), []byte("fictional committed card")
	prior, next := sha256.Sum256(priorBytes), sha256.Sum256(nextBytes)
	run := fixedRun(t, "20260914T123000.123456Z_1234abcd")
	if _, err := run.Started(); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Failed("private@example.test", false, false); err != ErrState {
		t.Fatal("arbitrary failure text was accepted", err)
	}
	if _, err := run.Failed("JOB_REANALYSIS_PREPARATION_FAILED", true, false); err != ErrState {
		t.Fatal("pre-plan publication attempt was accepted", err)
	}
	failedBefore, err := run.Failed("JOB_REANALYSIS_PREPARATION_FAILED", false, false)
	if err != nil || decodedEvent(t, failedBefore).Sequence != 2 || strings.Contains(string(failedBefore.Bytes()), "private@example.test") {
		t.Fatal("pre-plan failure event invalid", err)
	}
	if ClassifyPublication(decodedEvent(t, failedBefore), nextBytes, true) != PublicationUnknown {
		t.Fatal("a run without a durable plan invented publication state")
	}
	run = fixedRun(t, "")
	if _, err := run.Started(); err != nil {
		t.Fatal(err)
	}
	plan, err := run.Planned(prior, next)
	if err != nil {
		t.Fatal(err)
	}
	witness := decodedEvent(t, plan)
	for _, test := range []struct {
		name    string
		current []byte
		present bool
		want    PublicationState
	}{
		{"prior", priorBytes, true, PublicationNotPublished},
		{"next", nextBytes, true, PublicationPublished},
		{"third", []byte("another writer's card"), true, PublicationUnknown},
		{"missing", nil, false, PublicationUnknown},
	} {
		if got := ClassifyPublication(witness, test.current, test.present); got != test.want {
			t.Errorf("%s: %s, want %s", test.name, got, test.want)
		}
	}
	failedAfter, err := run.Failed("JOB_REANALYSIS_COMMITTED_UNVERIFIED", true, true)
	if err != nil {
		t.Fatal(err)
	}
	parsed := decodedEvent(t, failedAfter)
	if parsed.Sequence != 3 || !parsed.CommitAttempted || !parsed.Committed {
		t.Fatal("committed failure lost publication facts", parsed)
	}
	if _, err := Interrupted(decodedEvent(t, failedBefore), PublicationUnknown); err != ErrState {
		t.Fatal("pre-plan crash was misclassified as unknown", err)
	}
	startedRun := fixedRun(t, "")
	startedEvent, err := startedRun.Started()
	if err != nil {
		t.Fatal(err)
	}
	beforePlan, err := Interrupted(decodedEvent(t, startedEvent), PublicationNotPublished)
	if err != nil || decodedEvent(t, beforePlan).PublicationState != PublicationNotPublished {
		t.Fatal("pre-plan interruption was not explicit", err)
	}
	unknown, err := Interrupted(witness, PublicationUnknown)
	if err != nil {
		t.Fatal(err)
	}
	interrupted := decodedEvent(t, unknown)
	if interrupted.Sequence != 3 || interrupted.PublicationState != PublicationUnknown || interrupted.CommitAttempted || interrupted.Committed {
		t.Fatal("interrupted run invented commit facts", interrupted)
	}
	if _, err := run.Failed("JOB_REANALYSIS_INTERRUPTED", false, false); err != ErrState {
		t.Fatal("normal failure constructor accepted an interruption", err)
	}
}

func TestSavedJDRunAlreadyCurrentHasNoPublicationPlan(t *testing.T) {
	run := fixedRun(t, "")
	started, err := run.Started()
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := run.Unchanged()
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodedEvent(t, unchanged)
	previous := decodedEvent(t, started)
	if !contiguous(&previous, decoded) || decoded.Sequence != 2 || decoded.Event != "completed" || decoded.Status != "success" || decoded.HasPlan || decoded.CommitAttempted || decoded.Committed {
		t.Fatal("no-op invented publication", decoded)
	}
	if _, err := run.Planned(sha256.Sum256([]byte("old")), sha256.Sum256([]byte("new"))); err != ErrState {
		t.Fatal("completed no-op accepted a late publication plan", err)
	}
	if _, err := Interrupted(decoded, PublicationNotPublished); err != ErrState {
		t.Fatal("terminal no-op was reopened as interrupted", err)
	}
	forged := bytes.Replace(unchanged.Bytes(), []byte(`"commit_attempted":false`), []byte(`"commit_attempted":true`), 1)
	if _, err := Decode(forged); err != ErrEvent {
		t.Fatal("no-op with a claimed write was accepted", err)
	}
}

func TestSavedJDRunUncertainWriteDoesNotClaimRollback(t *testing.T) {
	run := fixedRun(t, "")
	if _, err := run.Started(); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Planned(sha256.Sum256([]byte("old")), sha256.Sum256([]byte("new"))); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Failed("JOB_REANALYSIS_WRITE_UNCERTAIN", false, false); err != ErrState {
		t.Fatal("uncertain write without an attempt was accepted", err)
	}
	if _, err := run.Failed("JOB_REANALYSIS_INPUT_CHANGED", true, false); err != ErrState {
		t.Fatal("precommit error claimed an uncertain publication", err)
	}
	unknown, err := run.Failed("JOB_REANALYSIS_WRITE_UNCERTAIN", true, false)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodedEvent(t, unknown)
	if !decoded.CommitAttempted || decoded.Committed || decoded.PublicationState != PublicationUnknown {
		t.Fatal("unconfirmed write did not retain unknown publication", decoded)
	}
	withoutState := bytes.Replace(unknown.Bytes(), []byte(`,"publication_state":"unknown"`), nil, 1)
	if bytes.Equal(withoutState, unknown.Bytes()) {
		t.Fatal("unknown publication marker absent")
	}
	if _, err := Decode(withoutState); err != ErrEvent {
		t.Fatal("unconfirmed write without unknown state was accepted", err)
	}
}
