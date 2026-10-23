package applicationwrite

import (
	"context"
	"database/sql"
	"encoding/json"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/intake/resumelibrary"
)

// RecordSubmissionRequest records an external action, never performs one.
// A nil ResumeID preserves the existing selection; it does not clear it.
type RecordSubmissionRequest struct {
	JobID              string
	ConfirmedSubmitted bool
	ResumeID           *string
	OccurredAt         *string
	Note               string
}

type SubmissionReceipt struct {
	SchemaVersion                 int                               `json:"schema_version"`
	ContractVersion               int                               `json:"contract_version"`
	Result                        string                            `json:"result"`
	JobID                         string                            `json:"job_id"`
	Status                        applications.Value                `json:"status"`
	AppliedResume                 applications.AppliedResumeHistory `json:"applied_resume"`
	ExternalActionPerformedByTool bool                              `json:"external_action_performed_by_tool"`
}

// RecordSubmission composes existing ledger primitives under one owning
// transaction. Unlike generic job UI actions it never registers a missing job.
// Unlike the Python adapter's two commits, failed resume/event composition rolls
// back together. No source JD, resume file, session, or material is published.
func (s *Service) RecordSubmission(ctx context.Context, r RecordSubmissionRequest) (*Outcome, *Error) {
	if !r.ConfirmedSubmitted {
		return emptyFailure(failed("input", "AGENT_SUBMISSION_CONFIRMATION_REQUIRED"))
	}
	if s == nil || s.now == nil {
		return emptyFailure(failed("configuration", "APPLICATION_LEDGER_UNAVAILABLE"))
	}
	if p := boundedText(r.JobID, applications.MaxIDBytes); p != nil {
		return emptyFailure(p)
	}
	// Freeze optional caller values and bound the transport without prematurely
	// parsing a timestamp ignored by the source's already-recorded no-op branch.
	var resume, occurred any
	size := len(r.JobID) + len(r.Note)
	for _, item := range []struct {
		value  *string
		limit  int
		target *any
	}{
		{r.ResumeID, applications.MaxIDBytes, &resume}, {r.OccurredAt, 256, &occurred},
	} {
		if item.value != nil {
			value := *item.value
			if p := boundedText(value, item.limit); p != nil {
				return emptyFailure(p)
			}
			*item.target = value
			size += len(value)
		}
	}
	if p := boundedText(r.Note, applications.MaxRequestBytes); p != nil {
		return emptyFailure(p)
	}
	if p := requestSize(size); p != nil {
		return emptyFailure(p)
	}
	return s.run(ctx, r.JobID, func(reads *sourceReads) (mutation, *Error) {
		reads.resumeReads = &resumeReads{owner: reads}
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			before, p := applications.ApplicationInTransaction(ctx, tx, r.JobID)
			if p != nil {
				return nil, applicationProblem(p, r.JobID)
			}
			applied := before.HasAppliedEvent()
			noChange := applied && (resume == nil || before.HasSubmittedResume(resume.(string)) && trim(r.Note) == "")
			after := before
			changed := false
			if !noChange {
				now := s.now()
				applyResume := func(operation string) *Error {
					fields := map[string]any{"job_id": r.JobID, "resume_id": resume}
					if operation == "submitted" {
						fields["occurred_at"], fields["note"] = occurred, r.Note
					}
					out, p := resumelibrary.ApplyInTransaction(ctx, tx, resumelibrary.Request{Operation: operation, Fields: fields}, now, s.resumeEntropy, reads.resumeReads)
					if p != nil {
						return resumeProblem(p)
					}
					// These fixed reference-only operations must never acquire file effects.
					if out.File != nil || out.IsFile || len(out.Bytes) != 0 {
						return failed("write", "AGENT_SUBMISSION_UNEXPECTED_FILE_EFFECT")
					}
					changed = changed || out.Mutation.Changed
					return nil
				}
				if resume != nil {
					if p := applyResume("select"); p != nil {
						return nil, p
					}
				}
				if applied {
					if p := applyResume("submitted"); p != nil {
						return nil, p
					}
				} else {
					event, p := normalizeTimestamp(occurred, now)
					if p != nil {
						return nil, p
					}
					out, ae := applications.StatusInTransaction(ctx, tx, r.JobID, "applied", event, now, r.Note, nil)
					if ae != nil {
						return nil, applicationProblem(ae, r.JobID)
					}
					changed = changed || out.Changed
				}
				after, p = applications.ApplicationInTransaction(ctx, tx, r.JobID)
				if p != nil {
					return nil, applicationProblem(p, r.JobID)
				}
			}
			result := "recorded"
			if noChange {
				result = "no_change_needed"
			}
			raw, err := json.Marshal(SubmissionReceipt{1, 4, result, r.JobID, after.CurrentStatus, after.AppliedResume, false})
			if err != nil || len(raw) > applications.MaxResultBytes {
				return nil, failed("result", "APPLICATION_LEDGER_RESULT_LIMIT")
			}
			return &applications.MutationResult{Result: raw, Changed: changed}, nil
		}, nil
	})
}
