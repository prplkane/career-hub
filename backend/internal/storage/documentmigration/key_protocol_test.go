package documentmigration

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/storage/documents/keys"
)

type migrationKeyBackend struct {
	values            map[string][]byte
	trace             *[]string
	deleteTargets     []string
	writeErr, readErr error
	deleteErr         error
	retainOnDelete    bool
}

func (b *migrationKeyBackend) Write(target string, secret []byte) error {
	*b.trace = append(*b.trace, "credential_write")
	b.values[target] = bytes.Clone(secret)
	return b.writeErr
}

func (b *migrationKeyBackend) Read(target string) ([]byte, bool, error) {
	*b.trace = append(*b.trace, "credential_read")
	value, present := b.values[target]
	return bytes.Clone(value), present, b.readErr
}

func (b *migrationKeyBackend) Delete(target string) (bool, error) {
	*b.trace = append(*b.trace, "credential_delete")
	b.deleteTargets = append(b.deleteTargets, target)
	if b.deleteErr != nil {
		return false, b.deleteErr
	}
	_, present := b.values[target]
	if !b.retainOnDelete {
		delete(b.values, target)
	}
	return present, nil
}

func migrationRandom(size int) ([]byte, error) {
	if size == 16 {
		return bytes.Repeat([]byte{0x22}, size), nil
	}
	return bytes.Repeat([]byte{0x33}, size), nil
}

func initialMigrationJournal() Journal {
	return Journal{MigrationID: recoveryMigrationID, State: JournalRecoveryVerified}
}

func TestJournaledKeyProvesBothTransitionsBeforeReturningKey(t *testing.T) {
	trace := []string{}
	backend := &migrationKeyBackend{values: map[string][]byte{}, trace: &trace}
	advance := func(current, next Journal) (journalWriteOutcome, *Error) {
		trace = append(trace, "journal:"+string(next.State))
		if !current.CanAdvanceTo(next) {
			t.Fatal("invalid transition reached writer")
		}
		return journalWritten, nil
	}
	result, problem := createJournaledKey("stable", backend, migrationRandom, "dh_11111111111111111111111111111111", initialMigrationJournal(), advance)
	if problem != nil || result.JournalUnknown || result.LastProven.State != JournalKeyCreated ||
		len(result.Master.Key) != keys.KeyBytes || result.Master.State.ActiveKeyID != "key_22222222222222222222222222222222" {
		t.Fatalf("unexpected key result: %+v / %#v", result, problem)
	}
	want := []string{"journal:key_planned", "credential_write", "journal:key_created"}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("ordering changed: %v", trace)
	}
}

func TestJournaledKeyNeverWritesBeforeProvenPlan(t *testing.T) {
	for name, outcome := range map[string]journalWriteOutcome{
		"not written":        journalNotWritten,
		"unknown":            journalWriteUnknown,
		"unrecognized":       journalWriteOutcome(99),
		"written with error": journalWritten,
	} {
		t.Run(name, func(t *testing.T) {
			trace := []string{}
			backend := &migrationKeyBackend{values: map[string][]byte{}, trace: &trace}
			advance := func(_, _ Journal) (journalWriteOutcome, *Error) {
				trace = append(trace, "journal")
				if name == "written with error" {
					return outcome, fail(CodeApplyFailed)
				}
				return outcome, nil
			}
			result, problem := createJournaledKey(
				"stable", backend, migrationRandom, "dh_11111111111111111111111111111111",
				initialMigrationJournal(), advance,
			)
			if problem == nil || problem.Code != CodeApplyFailed || len(result.Master.Key) != 0 || len(backend.values) != 0 ||
				len(trace) != 1 || trace[0] != "journal" {
				t.Fatalf("unproven plan reached credential write: %+v / %#v / %v", result, problem, trace)
			}
			if result.JournalUnknown != (outcome != journalNotWritten && outcome != journalWritten) {
				t.Fatal("unknown journal result was not retained")
			}
			if outcome == journalWritten && result.LastProven.State != JournalKeyPlanned {
				t.Fatal("proven planned journal was forgotten")
			}
		})
	}
}

func TestJournaledKeyRejectsInvalidAuthorityBeforeRandomness(t *testing.T) {
	trace := []string{}
	backend := &migrationKeyBackend{values: map[string][]byte{}, trace: &trace}
	randomCalls, journalCalls := 0, 0
	for _, current := range []Journal{
		{MigrationID: "bad", State: JournalRecoveryVerified},
		{MigrationID: recoveryMigrationID, State: JournalRecoveryVerified, DataHomeID: new(string)},
	} {
		result, problem := createJournaledKey("stable", backend, func(int) ([]byte, error) {
			randomCalls++
			return nil, nil
		}, "dh_11111111111111111111111111111111", current, func(_, _ Journal) (journalWriteOutcome, *Error) {
			journalCalls++
			return journalWritten, nil
		})
		if problem == nil || problem.Code != CodeApplyFailed || len(result.Master.Key) != 0 {
			t.Fatalf("invalid authority was accepted: %+v / %#v", result, problem)
		}
	}
	if randomCalls != 0 || journalCalls != 0 || len(trace) != 0 {
		t.Fatalf("invalid authority reached a side effect: rng=%d journal=%d trace=%v", randomCalls, journalCalls, trace)
	}
}

func TestCredentialWriteAndCreatedJournalUnknownRemainRecoverable(t *testing.T) {
	for name := range map[string]bool{"credential write error": true, "key_created unknown": true} {
		t.Run(name, func(t *testing.T) {
			trace := []string{}
			backend := &migrationKeyBackend{values: map[string][]byte{}, trace: &trace}
			if name == "credential write error" {
				backend.writeErr = errors.New("private synthetic write error")
			}
			calls := 0
			advance := func(_, _ Journal) (journalWriteOutcome, *Error) {
				calls++
				if name == "key_created unknown" && calls == 2 {
					return journalWriteUnknown, nil
				}
				return journalWritten, nil
			}
			result, problem := createJournaledKey(
				"stable", backend, migrationRandom, "dh_11111111111111111111111111111111", initialMigrationJournal(), advance,
			)
			if problem == nil || problem.Code != CodeApplyFailed || len(result.Master.Key) != 0 ||
				result.LastProven.State != JournalKeyPlanned || len(backend.values) != 1 {
				t.Fatalf("ambiguous write lost recovery facts: %+v / %#v", result, problem)
			}
			if result.JournalUnknown != (name == "key_created unknown") {
				t.Fatal("journal uncertainty mismatch")
			}
			plan := RecoveryPlan{
				Journal: result.LastProven, Channel: "stable",
				Actions: []RecoveryAction{RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal},
			}
			if recoveryProblem := discardPlannedRecoveryKey(plan, backend); recoveryProblem != nil || len(backend.values) != 0 {
				t.Fatalf("planned orphan was not removed: %#v", recoveryProblem)
			}
		})
	}
}

func TestRecoveryKeyDiscardRequiresAuthorizationAndConfirmedAbsence(t *testing.T) {
	homeID, keyID := "dh_11111111111111111111111111111111", "key_22222222222222222222222222222222"
	journal := Journal{MigrationID: recoveryMigrationID, State: JournalKeyPlanned, DataHomeID: &homeID, KeyID: &keyID}
	target := "CareerHub/stable/document-key/" + homeID + "/" + keyID

	t.Run("delete error stops before read", func(t *testing.T) {
		trace := []string{}
		backend := &migrationKeyBackend{values: map[string][]byte{target: bytes.Repeat([]byte{1}, keys.KeyBytes)}, trace: &trace, deleteErr: errors.New("private")}
		problem := discardPlannedRecoveryKey(RecoveryPlan{Journal: journal, Channel: "stable", Actions: []RecoveryAction{RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal}}, backend)
		if problem == nil || problem.Code != CodeRecoveryFailed || !reflect.DeepEqual(trace, []string{"credential_delete"}) {
			t.Fatalf("delete error was guessed through: %#v / %v", problem, trace)
		}
	})

	t.Run("remaining key is not confirmed", func(t *testing.T) {
		trace := []string{}
		backend := &migrationKeyBackend{values: map[string][]byte{target: bytes.Repeat([]byte{1}, keys.KeyBytes)}, trace: &trace, retainOnDelete: true}
		problem := discardPlannedRecoveryKey(RecoveryPlan{Journal: journal, Channel: "stable", Actions: []RecoveryAction{RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal}}, backend)
		if problem == nil || problem.Code != CodeRecoveryFailed || !reflect.DeepEqual(trace, []string{"credential_delete", "credential_read"}) {
			t.Fatalf("retained key was accepted: %#v / %v", problem, trace)
		}
	})

	t.Run("confirmation error retains a retry path", func(t *testing.T) {
		trace := []string{}
		backend := &migrationKeyBackend{
			values:  map[string][]byte{target: bytes.Repeat([]byte{1}, keys.KeyBytes)},
			trace:   &trace,
			readErr: errors.New("private"),
		}
		plan := RecoveryPlan{Journal: journal, Channel: "stable", Actions: []RecoveryAction{RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal}}
		if problem := discardPlannedRecoveryKey(plan, backend); problem == nil || problem.Code != CodeRecoveryFailed {
			t.Fatalf("confirmation error was accepted: %#v", problem)
		}
		backend.readErr = nil
		if problem := discardPlannedRecoveryKey(plan, backend); problem != nil ||
			!reflect.DeepEqual(trace, []string{"credential_delete", "credential_read", "credential_delete", "credential_read"}) {
			t.Fatalf("confirmation retry failed: %#v / %v", problem, trace)
		}
	})

	t.Run("missing authority makes no backend call", func(t *testing.T) {
		trace := []string{}
		backend := &migrationKeyBackend{values: map[string][]byte{}, trace: &trace}
		for _, plan := range []RecoveryPlan{
			{Journal: journal, Channel: "stable"},
			{Journal: journal, Channel: "stable", Actions: []RecoveryAction{RecoveryRestoreMarker, RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal}},
			{Journal: journal, Channel: "stable", Actions: []RecoveryAction{RecoveryDiscardKey, RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal}},
			{Journal: Journal{MigrationID: recoveryMigrationID, State: JournalRecoveryVerified, DataHomeID: &homeID, KeyID: &keyID}, Channel: "stable", Actions: []RecoveryAction{RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal}},
		} {
			problem := discardPlannedRecoveryKey(plan, backend)
			if problem == nil || problem.Code != CodeRecoveryFailed || len(trace) != 0 {
				t.Fatalf("unauthorized discard reached backend: %#v / %v", problem, trace)
			}
		}
	})

	t.Run("verified channel selects exact credential target", func(t *testing.T) {
		trace := []string{}
		devTarget := "CareerHub/dev/document-key/" + homeID + "/" + keyID
		backend := &migrationKeyBackend{values: map[string][]byte{
			target:    bytes.Repeat([]byte{1}, keys.KeyBytes),
			devTarget: bytes.Repeat([]byte{2}, keys.KeyBytes),
		}, trace: &trace}
		plan := RecoveryPlan{Journal: journal, Channel: "dev", Actions: []RecoveryAction{RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal}}
		if problem := discardPlannedRecoveryKey(plan, backend); problem != nil ||
			len(backend.deleteTargets) != 1 || backend.deleteTargets[0] != devTarget ||
			len(backend.values[target]) != keys.KeyBytes {
			t.Fatalf("discard escaped verified channel: %#v / %v / %v", problem, backend.deleteTargets, backend.values)
		}
	})
}
