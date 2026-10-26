package emailconnection

import (
	"context"
	"database/sql"
	"errors"

	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/storage/datahome"
)

const (
	senderAllowed  = "allowed"
	senderDisabled = "disabled"
)

// SenderDispositionInput changes one exact sender only. A nil purpose uses
// Python's job-alert default; a nil policy retains an existing rule's policy
// or uses the permissive policy for a new rule. Non-nil policy JSON is parsed
// and canonicalized inside the transaction after the revision check.
type SenderDispositionInput struct {
	SenderAddress     string
	Disposition       string
	Purpose           *string
	SubjectPolicyJSON *string
	ExpectedRevision  int64
}

type SenderDispositionResult = ConnectionMutationResult
type senderDispositionHooks = ownedMutationHooks

type normalizedDisposition struct {
	sender, disposition, purpose string
	policyJSON                   *string
}

func normalizeDisposition(input SenderDispositionInput) (normalizedDisposition, *Error) {
	sender, valid := normalizeConnectionAddress(input.SenderAddress)
	if !valid || input.Disposition != senderAllowed && input.Disposition != senderDisabled {
		return normalizedDisposition{}, failure("EMAIL_CONNECTION_INVALID")
	}
	purpose := emailrouting.SenderPurposeJobAlerts
	if input.Purpose != nil {
		purpose = *input.Purpose
	}
	switch purpose {
	case emailrouting.SenderPurposeJobAlerts, emailrouting.SenderPurposeApplicationTracking, emailrouting.SenderPurposeBoth:
	default:
		return normalizedDisposition{}, failure("EMAIL_CONNECTION_INVALID")
	}
	var policyJSON *string
	if input.SubjectPolicyJSON != nil {
		raw := *input.SubjectPolicyJSON
		policyJSON = &raw
	}
	return normalizedDisposition{sender, input.Disposition, purpose, policyJSON}, nil
}

// SetSenderDisposition allows or disables one normalized exact sender in an
// already-owned Home. It reads no message body, credential or mailbox.
func SetSenderDisposition(ctx context.Context, dataRoot string, expected datahome.Info, input SenderDispositionInput) (SenderDispositionResult, *Error) {
	return setSenderDispositionWithHooks(ctx, dataRoot, expected, input, senderDispositionHooks{})
}

func setSenderDispositionWithHooks(ctx context.Context, dataRoot string, expected datahome.Info, input SenderDispositionInput, hooks senderDispositionHooks) (SenderDispositionResult, *Error) {
	values, problem := normalizeDisposition(input)
	if problem != nil {
		return SenderDispositionResult{}, problem
	}
	return ownedEmailMutation(ctx, dataRoot, expected, input.ExpectedRevision, hooks,
		func(ctx context.Context, tx *sql.Tx, _, stamp string) *Error {
			var createdAt, previousPurpose, previousPolicy string
			err := tx.QueryRowContext(ctx, `SELECT created_at, purpose, subject_policy_json
				FROM job_email_sender_rules WHERE connection_id='primary' AND sender_address=?`,
				values.sender).Scan(&createdAt, &previousPurpose, &previousPolicy)
			existing := err == nil
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return ownedMutationSQLProblem(ctx)
			}
			if !existing {
				createdAt = stamp
			}
			purpose := values.purpose
			if values.disposition == senderDisabled && existing {
				purpose = previousPurpose
			}
			policyJSON := emailrouting.EmptyPolicy().CanonicalJSON()
			if values.policyJSON != nil {
				policy, err := emailrouting.ParsePolicyJSON(*values.policyJSON)
				if err != nil {
					return failure("EMAIL_CONNECTION_INVALID")
				}
				policyJSON = policy.CanonicalJSON()
			} else if existing {
				policy, err := emailrouting.ParsePolicyJSON(previousPolicy)
				if err != nil {
					return failure("EMAIL_CONNECTION_INVALID")
				}
				policyJSON = policy.CanonicalJSON()
			}
			if values.disposition == senderAllowed {
				var otherAllowed int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_email_sender_rules
					WHERE connection_id='primary' AND disposition='allowed' AND sender_address<>?`,
					values.sender).Scan(&otherAllowed); err != nil {
					return ownedMutationSQLProblem(ctx)
				}
				if otherAllowed >= maxConnectionSenders {
					return failure("EMAIL_CONNECTION_INVALID")
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO job_email_sender_rules (
				connection_id, sender_address, disposition, purpose,
				subject_policy_json, created_at, updated_at
			) VALUES ('primary', ?, ?, ?, ?, ?, ?)
			ON CONFLICT(connection_id, sender_address) DO UPDATE SET
				disposition=excluded.disposition, purpose=excluded.purpose,
				subject_policy_json=excluded.subject_policy_json,
				updated_at=excluded.updated_at`, values.sender, values.disposition,
				purpose, policyJSON, createdAt, stamp); err != nil {
				return ownedMutationSQLProblem(ctx)
			}
			return nil
		})
}
