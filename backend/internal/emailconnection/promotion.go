package emailconnection

import (
	"context"

	"careerhub.local/backend/internal/secretstore"
)

// PromoteLegacyInput is deliberately distinct from RotateInput. Promotion
// needs a fresh password and an already admitted startup Home, and must be
// invoked only by an explicit migration action after legacy-writer exclusion.
type PromoteLegacyInput RotateInput

// PromoteLegacyResult exposes no old-target deletion state: promotion never
// reads or retires the channel-scoped v1 credential.
type PromoteLegacyResult struct {
	IntentPublished   bool
	NewWriteAttempted bool
	CommitAttempted   bool
	Committed         bool
	IntentRemoved     bool
	RecoveryRequired  bool
	Revision          int64
}

// PromoteLegacy switches one owned Home's exact v1 row to a new local v3 target.
// It is native-only and has no installed or HTTP route. The caller must first
// exclude Python for this Home and satisfy the documented backup/cutover gates.
func PromoteLegacy(ctx context.Context, dataRoot string, backend secretstore.Backend, input PromoteLegacyInput) (PromoteLegacyResult, *Error) {
	return promoteLegacyWithHooks(ctx, dataRoot, backend, input, rotationHooks{})
}

func promoteLegacyWithHooks(ctx context.Context, dataRoot string, backend secretstore.Backend, input PromoteLegacyInput, hooks rotationHooks) (PromoteLegacyResult, *Error) {
	result, problem := rotateWithMode(ctx, dataRoot, backend, RotateInput(input), hooks, true)
	return nonDeletingResult(result), problem
}

// Reconnect requires explicit fresh-password input and an admitted startup Home.
// It replaces unbound v1/v2 or foreign-root v3 metadata, never reads/deletes the
// old credential, and refuses any existing recovery journal. No implicit save,
// ordinary rotation or snapshot invokes this action.
func Reconnect(ctx context.Context, dataRoot string, backend secretstore.Backend, input PromoteLegacyInput) (PromoteLegacyResult, *Error) {
	result, problem := rotateWithOperation(ctx, dataRoot, backend, RotateInput(input), rotationHooks{}, "reconnect")
	return nonDeletingResult(result), problem
}

func nonDeletingResult(result RotateResult) PromoteLegacyResult {
	return PromoteLegacyResult{
		IntentPublished:   result.IntentPublished,
		NewWriteAttempted: result.NewWriteAttempted,
		CommitAttempted:   result.CommitAttempted,
		Committed:         result.Committed,
		IntentRemoved:     result.IntentRemoved,
		RecoveryRequired:  result.RecoveryRequired,
		Revision:          result.Revision,
	}
}
