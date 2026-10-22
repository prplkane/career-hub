package applications

import (
	"context"
	"database/sql"
)

// ResumePlanApplicationInTransaction preserves GetApplication's complete
// bounded record validation before exposing just the two plan decision inputs.
func ResumePlanApplicationInTransaction(ctx context.Context, tx *sql.Tx, id string) (status, fingerprint string, problem *Error) {
	detail, e := ApplicationInTransaction(ctx, tx, id)
	if e != nil {
		return "", "", e
	}
	status, ok := detail.CurrentStatus.value.(string)
	fingerprint, valid := detail.ContentFingerprint.value.(string)
	if !ok || !valid {
		return "", "", failure("applications", "DATABASE_VALUE_UNSUPPORTED", nil)
	}
	return status, fingerprint, nil
}
