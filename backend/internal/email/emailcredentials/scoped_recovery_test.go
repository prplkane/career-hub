package emailcredentials

import (
	"errors"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestScopedRecoveryNonDeletingAdoptionAndRebind(t *testing.T) {
	for _, operation := range []string{AdoptUnbound, RebindCopied} {
		intent := sampleScopedIntent(operation)
		for _, after := range []bool{false, true} {
			for _, found := range []bool{false, true} {
				current := intent.Before
				want := RecoveryPlan{DatabaseSide: BeforeCommit}
				if found {
					want.Cleanup = CleanupNew
				}
				var wantError error
				if after {
					current = intent.After
					want = RecoveryPlan{DatabaseSide: AfterCommit}
					if !found {
						want = RecoveryPlan{}
						wantError = ErrRecoveryCredentialMissing
					}
				}
				plan, err := planScopedRecovery(intent, scopedFingerprint, current, CredentialObservation{NewFound: observed(found)})
				if plan != want || !errors.Is(err, wantError) {
					t.Fatalf("%s after=%v found=%v: %+v %v", operation, after, found, plan, err)
				}
				// Even observing false for the old target violates this contract.
				for _, old := range []bool{false, true} {
					plan, err := planScopedRecovery(intent, scopedFingerprint, current, CredentialObservation{NewFound: observed(found), OldFound: observed(old)})
					if plan != (RecoveryPlan{}) || !errors.Is(err, ErrRecoveryObservationInvalid) {
						t.Fatal("old target observation admitted")
					}
				}
			}
		}
	}
}

func TestScopedRecoveryNormalTruthTable(t *testing.T) {
	for _, operation := range []string{InitialConfigure, Rotate, Disconnect, Forget} {
		intent := sampleScopedIntent(operation)
		for _, after := range []bool{false, true} {
			for _, oldFound := range []bool{false, true} {
				for _, newFound := range []bool{false, true} {
					current := intent.Before
					want := RecoveryPlan{DatabaseSide: BeforeCommit}
					var wantError error
					if after {
						current = intent.After
						want.DatabaseSide = AfterCommit
					}
					presence := CredentialObservation{}
					newGeneration := operation == InitialConfigure || operation == Rotate
					if intent.Before.Present {
						presence.OldFound = observed(oldFound)
					}
					if newGeneration {
						presence.NewFound = observed(newFound)
					}
					if !after && intent.Before.Present && intent.Before.Enabled && !oldFound || after && newGeneration && !newFound {
						want, wantError = RecoveryPlan{}, ErrRecoveryCredentialMissing
					} else if !after && newGeneration && newFound {
						want.Cleanup = CleanupNew
					} else if after && intent.Before.Present && oldFound {
						want.Cleanup = CleanupOld
					}
					plan, err := planScopedRecovery(intent, scopedFingerprint, current, presence)
					if plan != want || !errors.Is(err, wantError) {
						t.Fatalf("%s after=%v old=%v new=%v: %+v %v", operation, after, oldFound, newFound, plan, err)
					}
				}
			}
		}
	}
}

func TestScopedRecoveryRefusesForeignScopeAndAmbiguousDatabase(t *testing.T) {
	intent := sampleScopedIntent(RebindCopied)
	presence := CredentialObservation{NewFound: observed(true)}
	if plan, err := planScopedRecovery(intent, strings.Repeat("f", 64), intent.After, presence); plan != (RecoveryPlan{}) || !errors.Is(err, ErrIntentInvalid) {
		t.Fatal("foreign root recovery admitted")
	}
	if plan, err := planScopedRecovery(intent, scopedFingerprint, RowWitness{}, presence); plan != (RecoveryPlan{}) || !errors.Is(err, ErrRecoveryStateChanged) {
		t.Fatal("third database state admitted")
	}
	if plan, err := planScopedRecovery(intent, scopedFingerprint, intent.After, CredentialObservation{}); plan != (RecoveryPlan{}) || !errors.Is(err, ErrRecoveryObservationInvalid) {
		t.Fatal("unread new target admitted")
	}
	if plan, err := PlanScopedRecovery(intent, runtimepaths.CredentialScope{}, intent.After, presence); plan != (RecoveryPlan{}) || !errors.Is(err, ErrIntentInvalid) {
		t.Fatal("zero scope recovery admitted")
	}
	for _, before := range []string{LegacyName, intentV2, scopedGolden, "arbitrary-target"} {
		intent.Before.SecretName = before
		if validScopedIntent(intent, scopedFingerprint) {
			t.Fatal("rebind accepts non-foreign-v3 before pointer")
		}
	}
}
