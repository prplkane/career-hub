package catalogtransaction

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"careerhub.local/backend/internal/catalogstore"
)

func TestUnchangedPrivateCatalogGuardIncludesAbsence(t *testing.T) {
	for _, missing := range []bool{false, true} {
		m, changes := fixture(missing)
		changes[1].CheckOnly = true
		changes[1].After = bytes.Clone(changes[1].Before)
		report, e := begin(context.Background(), m, testHomeID, changes, accept)
		if e != nil || !report.Completed || len(report.AppliedFiles) != 1 || report.AppliedFiles[0] != catalogstore.CustomGroupsPath {
			t.Fatalf("unchanged input overwritten: %#v %v", report, e)
		}
		if _, present := m.files[catalogstore.CustomSkillsPath]; present == missing {
			t.Fatal("absent input materialized or existing input removed")
		}
		m, changes = fixture(missing)
		changes[1].CheckOnly = true
		changes[1].After = bytes.Clone(changes[1].Before)
		raw, _ := encode(testHomeID, changes)
		m.files[JournalPath] = raw
		m.files[catalogstore.CustomSkillsPath] = []byte("external edit")
		report, e = resume(context.Background(), m, testHomeID, accept)
		if !errors.Is(e, ErrConflict) || !report.IntentRecorded || m.writeCount != 0 {
			t.Fatal("changed guard was accepted")
		}
	}
	_, changes := fixture(false)
	changes[0].CheckOnly = true
	if _, e := encode(testHomeID, changes); !errors.Is(e, ErrInput) {
		t.Fatal("mutating guard accepted")
	}
	changes[0].After = changes[0].Before
	changes[1].CheckOnly = true
	changes[1].After = changes[1].Before
	if _, e := encode(testHomeID, changes); !errors.Is(e, ErrInput) {
		t.Fatal("journal without any mutation accepted")
	}
}

func TestOwnedTransactionEntryPointsRefuseBeforeAnyStorage(t *testing.T) {
	_, changes := fixture(false)
	if _, e := BeginOwned(nil, nil, testHomeID, changes, accept); !errors.Is(e, ErrInput) {
		t.Fatal("nil context admitted")
	}
	if _, e := BeginOwned(context.Background(), nil, "invalid", changes, accept); !errors.Is(e, ErrInput) {
		t.Fatal("invalid home id admitted")
	}
	if _, e := BeginOwned(context.Background(), nil, testHomeID, changes, nil); !errors.Is(e, ErrInput) {
		t.Fatal("missing validator admitted")
	}
	if _, e := BeginOwned(context.Background(), nil, testHomeID, changes, accept); !errors.Is(e, ErrInput) {
		t.Fatal("unowned root admitted")
	}
	if _, e := RecoverOwned(context.Background(), nil, testHomeID, nil); !errors.Is(e, ErrInput) {
		t.Fatal("missing recovery validator admitted")
	}
	if _, e := RecoverOwned(context.Background(), nil, testHomeID, accept); !errors.Is(e, ErrInput) {
		t.Fatal("unowned recovery admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, recover := range []bool{false, true} {
		var report Report
		var e error
		if recover {
			report, e = RecoverOwned(ctx, nil, testHomeID, accept)
		} else {
			report, e = BeginOwned(ctx, nil, testHomeID, changes, accept)
		}
		if !errors.Is(e, context.Canceled) || report.IntentRecorded || report.Completed {
			t.Fatal("cancellation entry facts lost")
		}
	}
}
