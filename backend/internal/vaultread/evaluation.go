package vaultread

import (
	"context"
	"time"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/jobevidence"
)

// EvaluationEvidence contains only the source evaluator's candidate inputs.
// It is an owned per-operation value, not a cached verdict or an HTTP payload.
type EvaluationEvidence struct {
	Index          jobevidence.Index
	DurationMonths map[string]float64
	Eligibility    cataloginput.Value
}

// PrepareEvaluation loads the fixed eight Vault files once and runs complete
// manual/calendar/reference validation before building any evidence. The caller
// supplies managed readers, owns admission/writer coordination, and must recheck
// its observed read set before publication. No source handle is closed here.
func PrepareEvaluation(ctx context.Context, source reader, today time.Time) (*EvaluationEvidence, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, canceled()
	}
	if source == nil {
		return nil, &Error{Stage: "read", Code: "VAULT_READ_FAILED"}
	}
	if today.Year() < 1 || today.Year() > 9999 {
		return nil, &Error{Stage: "context", Code: "EVIDENCE_MONTH_INVALID"}
	}
	state, failure := loadVault(ctx, source)
	if failure != nil {
		return nil, failure
	}
	if failure := validateVault(ctx, state); failure != nil {
		return nil, failure
	}
	index, err := jobevidence.BuildIndex(ctx, jobevidence.Documents{
		Experience: state.documents[experienceIndex], Projects: state.documents[projectIndex],
		Education: state.documents[educationIndex], Skills: state.documents[skillIndex],
	})
	if err != nil {
		return nil, &Error{Stage: "evidence", Code: err.Code}
	}
	durations, err := jobevidence.ExperienceDurations(ctx, state.documents[experienceIndex], today)
	if err != nil {
		return nil, &Error{Stage: "duration", Code: err.Code}
	}
	if ctx.Err() != nil {
		return nil, canceled()
	}
	return &EvaluationEvidence{Index: index, DurationMonths: durations, Eligibility: state.documents[eligibilityIndex]}, nil
}
