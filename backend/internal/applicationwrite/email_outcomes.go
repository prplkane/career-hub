package applicationwrite

import (
	"context"
	"database/sql"
	"encoding/json"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/emailcandidates"
	"careerhub.local/backend/internal/emailoutcomes"
)

// RecordEmailApplicationOutcome borrows the same native Data Home, operation
// gate and SQLite transaction as every other application mutation. It is not
// an installed route; only an approved email importer may call it at cutover.
func (s *Service) RecordEmailApplicationOutcome(
	ctx context.Context, fingerprint string, raw emailcandidates.ApplicationOutcome, observedAt string,
) (*Outcome, *Error) {
	return s.run(ctx, "", func(_ *sourceReads) (mutation, *Error) {
		recordedAt := s.now()
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			applyStatus := func(ctx context.Context, tx *sql.Tx, jobID, status, occurredAt, recordedAt, note string) (bool, error) {
				result, problem := applications.StatusInTransaction(ctx, tx, jobID, status, occurredAt, recordedAt, note)
				if problem != nil {
					return false, problem
				}
				return result != nil && result.Changed, nil
			}
			result, changed, err := emailoutcomes.RecordInTransaction(
				ctx, tx, fingerprint, raw, observedAt, recordedAt, applyStatus)
			if err != nil {
				if store, ok := err.(*emailoutcomes.StoreError); ok {
					return nil, failed("email_outcome", store.Code)
				}
				return nil, failed("email_outcome", "EMAIL_IMPORT_OUTCOME_FAILED")
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return nil, failed("email_outcome", "APPLICATION_LEDGER_RESPONSE_UNSUPPORTED")
			}
			return &applications.MutationResult{Result: encoded, Changed: changed}, nil
		}, nil
	})
}

// LinkEmailApplicationOutcome is an explicit user action. The outcome revision,
// status history and returned application detail are checked in the same owned
// transaction; an uncertain commit must remain uncertain to the caller.
func (s *Service) LinkEmailApplicationOutcome(ctx context.Context, outcomeID, jobID string, expectedRevision int64) (*Outcome, *Error) {
	return s.run(ctx, "", func(_ *sourceReads) (mutation, *Error) {
		recordedAt := s.now()
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			applyStatus := func(ctx context.Context, tx *sql.Tx, selectedJob, status, occurredAt, recordedAt, note string) (bool, error) {
				result, problem := applications.StatusInTransaction(ctx, tx, selectedJob, status, occurredAt, recordedAt, note)
				if problem != nil {
					return false, problem
				}
				return result != nil && result.Changed, nil
			}
			update, err := emailoutcomes.LinkInTransaction(ctx, tx, outcomeID, jobID, expectedRevision, recordedAt, applyStatus)
			if err != nil {
				if store, ok := err.(*emailoutcomes.StoreError); ok {
					return nil, failed("email_outcome_link", store.Code)
				}
				return nil, failed("email_outcome_link", "EMAIL_OUTCOME_LINK_FAILED")
			}
			application, problem := applications.ApplicationInTransaction(ctx, tx, jobID)
			if problem != nil || application == nil {
				return nil, failed("email_outcome_link", "EMAIL_OUTCOME_LINK_RESPONSE_FAILED")
			}
			encoded, err := json.Marshal(struct {
				Update      emailoutcomes.Result `json:"update"`
				Application *applications.Detail `json:"application"`
			}{update, application})
			if err != nil {
				return nil, failed("email_outcome_link", "EMAIL_OUTCOME_LINK_RESPONSE_FAILED")
			}
			return &applications.MutationResult{Result: encoded, Changed: true}, nil
		}, nil
	})
}
