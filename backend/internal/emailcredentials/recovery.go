package emailcredentials

import "errors"

type DatabaseSide uint8

const (
	BeforeCommit DatabaseSide = iota + 1
	AfterCommit
)

type CleanupAction uint8

const (
	CleanupNone CleanupAction = iota
	CleanupNew
	CleanupOld
)

var (
	ErrRecoveryStateChanged       = errors.New("EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED")
	ErrRecoveryObservationInvalid = errors.New("EMAIL_CREDENTIAL_OBSERVATION_INVALID")
	ErrRecoveryCredentialMissing  = errors.New("EMAIL_CREDENTIAL_RECOVERY_REQUIRED")
)

// CredentialObservation must come from successful reads of the exact targets
// named by a validated intent. Nil means unread or inapplicable, never absent.
// Initial configure and legacy promotion have no OldFound; disconnect/forget
// have no NewFound. Promotion never reads or retires the shared v1 target.
type CredentialObservation struct {
	OldFound *bool
	NewFound *bool
}

// RecoveryPlan identifies a cleanup side, not permission to perform I/O.
// A future coordinator must hold native ownership and the email gate, pin
// paths, recheck the intent/database/secret observations, perform any required
// legacy-v1 ownership preflight, and remove the exact intent only after proven
// cleanup. No target or credential value is copied into this result.
type RecoveryPlan struct {
	DatabaseSide            DatabaseSide
	Cleanup                 CleanupAction
	LegacyPreflightRequired bool
}

// PlanRecovery applies the exact before/after truth table to already observed
// state. A third database state or missing active/new credential fails closed.
func PlanRecovery(intent Intent, current RowWitness, presence CredentialObservation) (RecoveryPlan, error) {
	if !validIntent(intent) {
		return RecoveryPlan{}, ErrIntentInvalid
	}
	newGeneration := intent.Operation == InitialConfigure || intent.Operation == Rotate || intent.Operation == PromoteLegacy
	observeOld := intent.Before.Present && intent.Operation != PromoteLegacy
	return planRecovery(intent.Before, intent.After, current, presence, newGeneration, observeOld)
}

// Callers validate their versioned intent before sharing this state-only table.
func planRecovery(before, after, current RowWitness, presence CredentialObservation, newGeneration, observeOld bool) (RecoveryPlan, error) {
	if (presence.OldFound != nil) != observeOld ||
		(presence.NewFound != nil) != newGeneration {
		return RecoveryPlan{}, ErrRecoveryObservationInvalid
	}
	var side DatabaseSide
	switch current {
	case before:
		side = BeforeCommit
	case after:
		side = AfterCommit
	default:
		return RecoveryPlan{}, ErrRecoveryStateChanged
	}
	if side == BeforeCommit {
		if observeOld && before.Enabled && !*presence.OldFound {
			return RecoveryPlan{}, ErrRecoveryCredentialMissing
		}
		if newGeneration && *presence.NewFound {
			return RecoveryPlan{DatabaseSide: side, Cleanup: CleanupNew}, nil
		}
		return RecoveryPlan{DatabaseSide: side}, nil
	}
	if newGeneration && !*presence.NewFound {
		return RecoveryPlan{}, ErrRecoveryCredentialMissing
	}
	if observeOld && *presence.OldFound {
		return RecoveryPlan{
			DatabaseSide: side, Cleanup: CleanupOld,
			LegacyPreflightRequired: before.SecretName == LegacyName,
		}, nil
	}
	return RecoveryPlan{DatabaseSide: side}, nil
}
