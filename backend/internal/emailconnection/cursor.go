package emailconnection

import (
	"context"
	"database/sql"

	"careerhub.local/backend/internal/datahome"
)

type CursorResetResult = ConnectionMutationResult
type cursorResetHooks = ownedMutationHooks

// ResetCursor changes only the provider position and connection revision in
// an already-owned schema-28 Home. Imported receipts remain as dedup history;
// only receipts filtered by the old subject policy are removed for rechecking.
// No credential, mailbox, HTTP route or schema is created here.
func ResetCursor(ctx context.Context, dataRoot string, expected datahome.Info, expectedRevision int64) (CursorResetResult, *Error) {
	return resetCursorWithHooks(ctx, dataRoot, expected, expectedRevision, cursorResetHooks{})
}

func resetCursorWithHooks(ctx context.Context, dataRoot string, expected datahome.Info, expectedRevision int64, hooks cursorResetHooks) (CursorResetResult, *Error) {
	return ownedEmailMutation(ctx, dataRoot, expected, expectedRevision, hooks,
		func(ctx context.Context, tx *sql.Tx, folder, _ string) *Error {
			if _, err := tx.ExecContext(ctx, `UPDATE job_email_sync_state
				SET uidvalidity='', last_uid=0, last_result='never',
					consecutive_failures=0, next_retry_at=NULL, last_error_code='',
					revision=revision+1
				WHERE connection_id='primary' AND folder=?`, folder); err != nil {
				return ownedMutationSQLProblem(ctx)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM job_email_message_receipts
				WHERE connection_id='primary' AND folder=?
					AND error_code='EMAIL_SUBJECT_POLICY_FILTERED'`, folder); err != nil {
				return ownedMutationSQLProblem(ctx)
			}
			return nil
		})
}
