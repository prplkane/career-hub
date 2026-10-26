package emailcredentials

import "careerhub.local/backend/internal/storage/runtimepaths"

// PlanScopedRecovery requires the current native scope and validates the entire
// version-2 intent again. Adoption/rebinding never accepts OldFound, not even
// false: the foreign/unbound credential must not have been read at all.
// It identifies a cleanup side only; the owner must pin/recheck all effects.
func PlanScopedRecovery(intent ScopedIntent, scope runtimepaths.CredentialScope, current RowWitness, presence CredentialObservation) (RecoveryPlan, error) {
	if !scope.Valid() {
		return RecoveryPlan{}, ErrIntentInvalid
	}
	return planScopedRecovery(intent, scope.Fingerprint(), current, presence)
}

func planScopedRecovery(intent ScopedIntent, fingerprint string, current RowWitness, presence CredentialObservation) (RecoveryPlan, error) {
	if !validScopedIntent(intent, fingerprint) {
		return RecoveryPlan{}, ErrIntentInvalid
	}
	noOld := intent.Operation == AdoptUnbound || intent.Operation == RebindCopied
	newGeneration := intent.Operation == InitialConfigure || intent.Operation == Rotate || noOld
	return planRecovery(intent.Before, intent.After, current, presence, newGeneration, intent.Before.Present && !noOld)
}
