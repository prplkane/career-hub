package applicationwrite

import (
	"context"
	"database/sql"
	"sync/atomic"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/savedassessment"
)

type newJobAssessmentReceipt struct {
	operationID, jobID string
	intentSHA256       [32]byte
	consumed           atomic.Bool
}

func capturedNewJobRow(ctx context.Context, tx *sql.Tx, intent newJobIntent, card cataloginput.Value) *Error {
	var company, title, sourceURL, fingerprint, status string
	if err := tx.QueryRowContext(ctx, `SELECT company,title,source_url,content_fingerprint,current_status
		FROM job_applications WHERE job_id=?`, intent.JobID).Scan(&company, &title, &sourceURL, &fingerprint, &status); err != nil {
		if ctx.Err() != nil {
			return canceled()
		}
		return failed("ledger", "JOB_INTAKE_CAPTURED_ROW_CHANGED")
	}
	if status != "captured" || company != newJobCardField(card, "company") ||
		title != newJobCardField(card, "title") || sourceURL != newJobCardField(card, "source_url") ||
		fingerprint != intent.ContentFingerprint {
		return failed("ledger", "JOB_INTAKE_CAPTURED_ROW_CHANGED")
	}
	return nil
}

// AssessNewJobAfterLedger is an unconnected, host-only third phase. A
// confirmed same-call Captured commit or the explicit incomplete-recovery
// owner may mint its private one-use capability. The exact pending intent/files
// and Captured SQL row are rechecked under the new gate;
// assessment is advisory and never marks an application Applied or settles the
// intent. An uncertain assessment commit is not safe to retry blindly.
func (s *Service) AssessNewJobAfterLedger(ctx context.Context, resourceRoot string, month assessmentcontext.Month, ledger *Outcome) (*Outcome, *Error) {
	if s == nil || s.expectedHome == nil || s.resourceRoot == "" || resourceRoot != s.resourceRoot ||
		ledger == nil || ledger.newJobLedgerReceipt == nil || !month.Valid() {
		return emptyFailure(failed("input", "JOB_INTAKE_ASSESSMENT_REQUEST_INVALID"))
	}
	if ctx == nil || ctx.Err() != nil {
		return &Outcome{RecoveryRequired: true}, canceled()
	}
	receipt := ledger.newJobLedgerReceipt
	if !receipt.consumed.CompareAndSwap(false, true) {
		return &Outcome{RecoveryRequired: true}, failed("input", "JOB_INTAKE_ASSESSMENT_ALREADY_ATTEMPTED")
	}
	initialStatus := ""
	out, problem := s.runMode(ctx, func(reads *sourceReads) (mutation, *Error) {
		intent, card, witnessErr := readNewJobLedgerWitness(reads, receipt.operationID, receipt.jobID, receipt.intentSHA256)
		if witnessErr != nil {
			return nil, witnessErr
		}
		initialStatus = intent.InitialStatus
		assessment, prepareErr := s.prepareSavedAssessment(reads, resourceRoot, savedassessment.Request{
			Operation: "load", Fields: map[string]any{"job_id": intent.JobID, "register_application": false}, Month: month,
		})
		if prepareErr != nil {
			return nil, prepareErr
		}
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			if e := capturedNewJobRow(ctx, tx, intent, card); e != nil {
				return nil, e
			}
			return assessment(ctx, tx)
		}, nil
	}, "new_job")
	if out != nil {
		out.RecoveryRequired = true
		if problem == nil && out.Committed && !out.CleanupFailed && initialStatus == "captured" && len(out.Result) > 0 {
			out.newJobTerminalCapability = &newJobTerminalCapability{operationID: receipt.operationID,
				jobID: receipt.jobID, intentSHA256: receipt.intentSHA256, expectedState: "assessed"}
		}
		if problem == nil && !out.CleanupFailed && (out.Committed || !out.CommitAttempted) &&
			initialStatus == "applied" && len(out.Result) > 0 {
			out.newJobAssessmentReceipt = &newJobAssessmentReceipt{operationID: receipt.operationID,
				jobID: receipt.jobID, intentSHA256: receipt.intentSHA256}
		}
	}
	return out, problem
}
