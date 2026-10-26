package applicationwrite

import (
	"context"
	"database/sql"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applications/applicationreview"
)

// ApplyNewJobAfterAssessment is host-only and unconnected. Only an explicit
// Applied intent sealed before the file phase and a positively completed
// same-call assessment or an explicit exact-state recovery can reach this
// fourth phase. It rechecks exact pending files, the Captured row and the
// stored assessment's card fingerprint before
// the existing status owner writes Applied plus its resume snapshot atomically.
// The pending intent remains until a separate reviewed terminal phase.
func (s *Service) ApplyNewJobAfterAssessment(ctx context.Context, assessment *Outcome) (*Outcome, *Error) {
	if s == nil || s.expectedHome == nil || assessment == nil || assessment.newJobAssessmentReceipt == nil {
		return emptyFailure(failed("input", "JOB_INTAKE_APPLIED_REQUEST_INVALID"))
	}
	if ctx == nil || ctx.Err() != nil {
		return &Outcome{RecoveryRequired: true}, canceled()
	}
	receipt := assessment.newJobAssessmentReceipt
	if !receipt.consumed.CompareAndSwap(false, true) {
		return &Outcome{RecoveryRequired: true}, failed("input", "JOB_INTAKE_APPLIED_ALREADY_ATTEMPTED")
	}
	out, problem := s.runMode(ctx, func(reads *sourceReads) (mutation, *Error) {
		intent, card, witnessErr := readNewJobLedgerWitness(reads, receipt.operationID, receipt.jobID, receipt.intentSHA256)
		if witnessErr != nil {
			return nil, witnessErr
		}
		if intent.InitialStatus != "applied" {
			return nil, failed("intent", "JOB_INTAKE_APPLIED_INTENT_MISSING")
		}
		now := s.now()
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			if e := capturedNewJobRow(ctx, tx, intent, card); e != nil {
				return nil, e
			}
			stored, readErr := applicationreview.ReadVersionedAssessment(ctx, tx, intent.JobID)
			if readErr != nil || stored == nil || stored.JobFingerprint != intent.CardSHA256 {
				if ctx.Err() != nil {
					return nil, canceled()
				}
				return nil, failed("assessment", "JOB_INTAKE_ASSESSMENT_CHANGED")
			}
			result, statusErr := applications.StatusInTransaction(ctx, tx, intent.JobID, "applied", now, now, "", nil)
			if statusErr != nil {
				return nil, applicationProblem(statusErr, intent.JobID)
			}
			if result == nil || !result.Changed {
				return nil, failed("ledger", "JOB_INTAKE_APPLIED_RESULT_INVALID")
			}
			return result, nil
		}, nil
	}, "new_job")
	if out != nil {
		out.RecoveryRequired = true
		if problem == nil && out.Committed && !out.CleanupFailed {
			out.newJobTerminalCapability = &newJobTerminalCapability{operationID: receipt.operationID,
				jobID: receipt.jobID, intentSHA256: receipt.intentSHA256, expectedState: "applied"}
		}
	}
	return out, problem
}
