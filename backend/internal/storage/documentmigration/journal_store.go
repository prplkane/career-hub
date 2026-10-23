package documentmigration

import (
	"bytes"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type journalFileStore interface {
	RequireProcessOwnership() *runtimepaths.Error
	ReadSingleLinkFile(string, int64) ([]byte, bool, *runtimepaths.Error)
	CreateFile(string, []byte) (bool, *runtimepaths.Error)
	ReplaceFile(string, []byte, []byte) (bool, *runtimepaths.Error)
	RemoveFile(string, []byte) (bool, *runtimepaths.Error)
}

var _ journalFileStore = (*runtimepaths.Root)(nil)

type journalRemovalOutcome uint8

const (
	journalNotRemoved journalRemovalOutcome = iota
	journalRemoved
	journalRemovalUnknown
)

// publishJournal is the sole no-replace publication bridge for a new migration
// journal. A reported mutation is trusted only after the exact canonical bytes
// are observed again under the same process claim.
func publishJournal(store journalFileStore, initial Journal) (journalWriteOutcome, *Error) {
	raw, problem := initial.CanonicalBytes()
	if store == nil || problem != nil || initial.State != JournalRecoveryVerified || !validRecoveryJournalIdentity(initial) ||
		store.RequireProcessOwnership() != nil {
		return journalNotWritten, fail(CodeApplyFailed)
	}
	created, pathProblem := store.CreateFile(datahome.DocumentRecoveryPath, raw)
	return classifyJournalWrite(store, nil, raw, created, pathProblem)
}

// advanceJournal replaces only the exact proven predecessor and never infers a
// transition from a path error. The returned fact deliberately separates a
// proven old value, a proven new value, and an ambiguous observation.
func advanceJournal(store journalFileStore, current, next Journal) (journalWriteOutcome, *Error) {
	currentRaw, currentProblem := current.CanonicalBytes()
	nextRaw, nextProblem := next.CanonicalBytes()
	if store == nil || currentProblem != nil || nextProblem != nil || !current.CanAdvanceTo(next) ||
		store.RequireProcessOwnership() != nil {
		return journalNotWritten, fail(CodeApplyFailed)
	}
	committed, pathProblem := store.ReplaceFile(datahome.DocumentRecoveryPath, currentRaw, nextRaw)
	return classifyJournalWrite(store, currentRaw, nextRaw, committed, pathProblem)
}

// removeJournal removes only the exact final/recovery journal. The caller may
// report a proven removal even when a later native check failed, but an absent
// file without an attributed removal remains unknown rather than guessed.
func removeJournal(store journalFileStore, current Journal) (journalRemovalOutcome, *Error) {
	raw, problem := current.CanonicalBytes()
	if store == nil || problem != nil || !validRecoveryJournalIdentity(current) ||
		store.RequireProcessOwnership() != nil {
		return journalNotRemoved, fail(CodeRecoveryFailed)
	}
	removed, pathProblem := store.RemoveFile(datahome.DocumentRecoveryPath, raw)
	observed, present, observationProblem := observeJournal(store)
	if observationProblem != nil {
		return journalRemovalUnknown, fail(CodeRecoveryFailed)
	}
	var outcome journalRemovalOutcome
	switch {
	case !present && removed:
		outcome = journalRemoved
	case present && !removed && bytes.Equal(observed, raw):
		outcome = journalNotRemoved
	default:
		outcome = journalRemovalUnknown
	}
	if pathProblem != nil || outcome != journalRemoved {
		return outcome, fail(CodeRecoveryFailed)
	}
	return outcome, nil
}

func classifyJournalWrite(store journalFileStore, current, next []byte, changed bool, pathProblem *runtimepaths.Error) (journalWriteOutcome, *Error) {
	observed, present, observationProblem := observeJournal(store)
	if observationProblem != nil {
		return journalWriteUnknown, fail(CodeApplyFailed)
	}
	var outcome journalWriteOutcome
	switch {
	case changed && present && bytes.Equal(observed, next):
		outcome = journalWritten
	case !changed && current != nil && present && bytes.Equal(observed, current):
		outcome = journalNotWritten
	case !changed && current == nil && !present:
		outcome = journalNotWritten
	default:
		outcome = journalWriteUnknown
	}
	if pathProblem != nil || outcome != journalWritten {
		return outcome, fail(CodeApplyFailed)
	}
	return outcome, nil
}

func observeJournal(store journalFileStore) ([]byte, bool, *Error) {
	if store.RequireProcessOwnership() != nil {
		return nil, false, fail(CodeRecoveryFailed)
	}
	raw, present, pathProblem := store.ReadSingleLinkFile(datahome.DocumentRecoveryPath, MaxJournalBytes)
	if pathProblem != nil || store.RequireProcessOwnership() != nil {
		return nil, false, fail(CodeRecoveryFailed)
	}
	return raw, present, nil
}
