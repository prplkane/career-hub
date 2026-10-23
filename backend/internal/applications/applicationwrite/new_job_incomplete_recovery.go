package applicationwrite

import (
	"context"
	"fmt"
	"io"

	"careerhub.local/backend/internal/jobs/assessmentcontext"
)

// NewJobRecoveryStepOutcome retains the exact observed starting state and the
// result of at most one resumed phase. A later explicit call must observe the
// durable state again before it may advance another phase.
type NewJobRecoveryStepOutcome struct {
	Observation *NewJobRecoveryObservation
	Plan        *NewJobRecoveryPlan
	Step        string
	Ledger      *Outcome
	Assessment  *Outcome
	Applied     *Outcome
	Terminal    *NewJobTerminalOutcome
}

func (NewJobRecoveryStepOutcome) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted new job recovery step outcome]")
}

// RecoverIncompleteNewJobStep explicitly resumes the one phase displayed by a
// read-only recovery plan. It rejects invalid, stale and non-advanceable plan
// IDs. Every resumed owner still reopens and rechecks the pending intent,
// raw/card bytes and guarded SQL state before its effect.
func (s *Service) RecoverIncompleteNewJobStep(ctx context.Context, expectedPlanID, resourceRoot string,
	month assessmentcontext.Month) (out *NewJobRecoveryStepOutcome, problem *Error) {
	out = &NewJobRecoveryStepOutcome{}
	if s == nil || s.expectedHome == nil || ctx == nil {
		return out, failed("configuration", "JOB_INTAKE_RECOVERY_UNAVAILABLE")
	}
	if !validNewJobRecoveryPlanID(expectedPlanID) {
		return out, failed("input", "JOB_INTAKE_RECOVERY_PLAN_INVALID")
	}
	observed, observeErr := s.ObserveNewJobRecovery(ctx)
	out.Observation = observed
	if observeErr != nil {
		return out, observeErr
	}
	plan, planErr := planIncompleteNewJobRecovery(observed)
	out.Plan = plan
	if planErr != nil {
		return out, planErr
	}
	if plan.PlanID != expectedPlanID {
		return out, failed("observation", "JOB_INTAKE_RECOVERY_PLAN_STALE")
	}
	if !plan.CanAdvance {
		switch plan.ReasonCode {
		case "SCHEMA_PREPARATION_REQUIRED":
			return out, failed("schema", plan.ReasonCode)
		case "JOB_INTAKE_RECOVERY_FILES_INCOMPLETE":
			return out, failed("files", plan.ReasonCode)
		default:
			return out, failed("observation", "JOB_INTAKE_RECOVERY_STATE_CONFLICT")
		}
	}

	out.Step = plan.NextStep
	switch plan.NextStep {
	case "ledger":
		phase := recoveredNewJobFilePhase(observed)
		out.Ledger, problem = s.RegisterNewJobAfterFiles(ctx, phase)
	case "assessment":
		ledger := &Outcome{RecoveryRequired: true, newJobLedgerReceipt: &newJobLedgerReceipt{
			operationID: observed.OperationID, jobID: observed.JobID, intentSHA256: observed.intentSHA256,
		}}
		out.Assessment, problem = s.AssessNewJobAfterLedger(ctx, resourceRoot, month, ledger)
	case "applied":
		assessment := &Outcome{RecoveryRequired: true, newJobAssessmentReceipt: &newJobAssessmentReceipt{
			operationID: observed.OperationID, jobID: observed.JobID, intentSHA256: observed.intentSHA256,
		}}
		out.Applied, problem = s.ApplyNewJobAfterAssessment(ctx, assessment)
	case "terminal":
		if observed.ReceiptState == "exact" {
			out.Terminal, problem = s.RecoverNewJobTerminal(ctx)
			break
		}
		out.Terminal, problem = s.FinalizeNewJobAfterSuccess(ctx,
			recoveredNewJobTerminalPhase(observed, observed.LedgerState))
	default:
		out.Step = ""
		return out, failed("observation", "JOB_INTAKE_RECOVERY_STATE_INVALID")
	}
	return out, problem
}

func recoveredNewJobFilePhase(observed *NewJobRecoveryObservation) *NewJobFilePhaseOutcome {
	return &NewJobFilePhaseOutcome{
		OperationID: observed.OperationID, JobID: observed.JobID, IntentSHA256: observed.intentSHA256,
		RecoveryRequired: true,
		recoveryReceipt: &newJobPhaseReceipt{operationID: observed.OperationID, jobID: observed.JobID,
			intentSHA256: observed.intentSHA256},
	}
}

func recoveredNewJobTerminalPhase(observed *NewJobRecoveryObservation, expected string) *Outcome {
	return &Outcome{RecoveryRequired: true, newJobTerminalCapability: &newJobTerminalCapability{
		operationID: observed.OperationID, jobID: observed.JobID,
		intentSHA256: observed.intentSHA256, expectedState: expected,
	}}
}

func newJobRecoveryTerminalState(observed *NewJobRecoveryObservation) bool {
	return observed != nil &&
		((observed.InitialStatus == "captured" && observed.LedgerState == "assessed") ||
			(observed.InitialStatus == "applied" && observed.LedgerState == "applied"))
}
