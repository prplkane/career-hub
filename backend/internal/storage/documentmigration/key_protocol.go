package documentmigration

import (
	"errors"

	"careerhub.local/backend/internal/storage/documents/keys"
)

const CodeApplyFailed = "DOCUMENT_MIGRATION_FAILED"

type journalWriteOutcome uint8

const (
	journalNotWritten journalWriteOutcome = iota
	journalWritten
	journalWriteUnknown
)

type journalAdvancer func(current, next Journal) (journalWriteOutcome, *Error)

type journaledKeyResult struct {
	Master         keys.MasterKey
	LastProven     Journal
	JournalUnknown bool
}

var errJournalAdvance = errors.New("document migration journal advance failed")

// createJournaledKey enforces journal-before-credential ordering. Only a
// proven key_planned write lets Manager reach its credential backend. Any
// credential error is treated as side-effect-unknown and left recoverable by
// the durable planned identity. A key is returned only after key_created is
// also proven; failed results retain no key bytes.
func createJournaledKey(channel string, backend keys.Backend, random keys.RandomBytes, dataHomeID string, current Journal, advance journalAdvancer) (result journaledKeyResult, problem *Error) {
	result.LastProven = current
	manager, managerError := keys.NewManager(channel, backend, random)
	_, currentProblem := current.CanonicalBytes()
	if managerError != nil || advance == nil || currentProblem != nil || current.State != JournalRecoveryVerified ||
		current.DataHomeID != nil || current.KeyID != nil {
		return result, fail(CodeApplyFailed)
	}
	master, creationError := manager.Create(dataHomeID, func(state keys.State) error {
		homeID, keyID := state.DataHomeID, state.ActiveKeyID
		planned := Journal{
			MigrationID: current.MigrationID, State: JournalKeyPlanned,
			DataHomeID: &homeID, KeyID: &keyID,
		}
		if !current.CanAdvanceTo(planned) {
			return errJournalAdvance
		}
		outcome, advanceProblem := advance(current, planned)
		recordJournalOutcome(&result, outcome, planned)
		if outcome != journalWritten || advanceProblem != nil {
			return errJournalAdvance
		}
		return nil
	})
	if creationError != nil {
		return result, fail(CodeApplyFailed)
	}
	createdHome, createdKey := master.State.DataHomeID, master.State.ActiveKeyID
	created := Journal{
		MigrationID: current.MigrationID, State: JournalKeyCreated,
		DataHomeID: &createdHome, KeyID: &createdKey,
	}
	if !result.LastProven.CanAdvanceTo(created) {
		clear(master.Key)
		return result, fail(CodeApplyFailed)
	}
	outcome, advanceProblem := advance(result.LastProven, created)
	recordJournalOutcome(&result, outcome, created)
	if outcome != journalWritten || advanceProblem != nil {
		clear(master.Key)
		return result, fail(CodeApplyFailed)
	}
	result.Master = master
	return result, nil
}

// discardPlannedRecoveryKey is the only document-migration bridge to
// DiscardUnpublished. Authorization requires the owned planner's explicit
// action and a complete canonical journal identity. A successful delete is
// followed by an absence read; any backend error retains the journal for a
// later explicit recovery attempt.
func discardPlannedRecoveryKey(plan RecoveryPlan, backend keys.Backend) *Error {
	if !authorizedRecoveryDiscard(plan) {
		return fail(CodeRecoveryFailed)
	}
	manager, managerError := keys.NewManager(plan.Channel, backend, nil)
	if managerError != nil {
		return fail(CodeRecoveryFailed)
	}
	state := keys.State{
		Version: keys.Version, Algorithm: keys.Algorithm,
		DataHomeID: *plan.Journal.DataHomeID, ActiveKeyID: *plan.Journal.KeyID,
	}
	if err := manager.DiscardUnpublished(state); err != nil {
		return fail(CodeRecoveryFailed)
	}
	key, err := manager.LoadKey(state.DataHomeID, state.ActiveKeyID)
	clear(key)
	var domain *keys.Error
	if errors.As(err, &domain) && domain.Code == keys.CodeUnavailable {
		return nil
	}
	return fail(CodeRecoveryFailed)
}

func authorizedRecoveryDiscard(plan RecoveryPlan) bool {
	if plan.Journal.DataHomeID == nil || plan.Journal.KeyID == nil ||
		plan.Channel != "dev" && plan.Channel != "stable" || len(plan.Actions) != 3 {
		return false
	}
	if _, problem := plan.Journal.CanonicalBytes(); problem != nil ||
		journalOrder[plan.Journal.State] < journalOrder[JournalKeyPlanned] || !validJournalIdentity(plan.Journal) {
		return false
	}
	return plan.Actions[0] == RecoveryDiscardKey && plan.Actions[1] == RecoveryWriteRecord &&
		plan.Actions[2] == RecoveryRemoveJournal
}

func recordJournalOutcome(result *journaledKeyResult, outcome journalWriteOutcome, next Journal) {
	switch outcome {
	case journalWritten:
		result.LastProven = next
	case journalNotWritten:
		// The writer proved that the previous journal is still authoritative.
	default:
		// Unknown and future values are conservatively side-effect-unknown.
		result.JournalUnknown = true
	}
}
