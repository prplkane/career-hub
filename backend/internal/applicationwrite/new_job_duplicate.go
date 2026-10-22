package applicationwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobdigest"
	"careerhub.local/backend/internal/savedassessment"
)

type newJobDuplicateWitness struct {
	cardPath, rawPath string
	card, raw         []byte
}

func (w *newJobDuplicateWitness) unchanged(reads *sourceReads) *Error {
	if w == nil || reads == nil || reads.root == nil {
		return failed("witness", "JOB_INTAKE_DUPLICATE_WITNESS_REQUIRED")
	}
	for _, item := range []struct {
		path string
		raw  []byte
	}{{w.cardPath, w.card}, {w.rawPath, w.raw}} {
		current, present, err := reads.root.ReadSingleLinkFile(item.path, int64(len(item.raw)))
		if err != nil || !present || !bytes.Equal(current, item.raw) {
			return failed("source", "JOB_INTAKE_DUPLICATE_CHANGED")
		}
	}
	return nil
}

type duplicateJobResponse struct {
	Duplicate    bool            `json:"duplicate"`
	JobID        string          `json:"job_id"`
	Reason       string          `json:"reason"`
	Assessment   json.RawMessage `json:"assessment"`
	Application  json.RawMessage `json:"application"`
	StatusBefore *string         `json:"status_before"`
}

// ProcessExistingJobDuplicate is the no-file-write counterpart to fresh JD
// intake. A single-use receipt from the owned duplicate scan binds exact card
// and raw bytes. Registration refresh, advisory assessment and optional
// explicit Applied status share one SQLite transaction and preserve existing
// workflow fields through the native application owner.
func (s *Service) ProcessExistingJobDuplicate(ctx context.Context, resourceRoot string,
	month assessmentcontext.Month, phase *NewJobFilePhaseOutcome) (*Outcome, *Error) {
	if s == nil || s.expectedHome == nil || s.resourceRoot == "" || s.resourceRoot != resourceRoot ||
		phase == nil || phase.Duplicate == nil || phase.duplicateReceipt == nil || !month.Valid() {
		return emptyFailure(failed("input", "JOB_INTAKE_DUPLICATE_REQUEST_INVALID"))
	}
	if ctx == nil || ctx.Err() != nil {
		return emptyFailure(canceled())
	}
	receipt := phase.duplicateReceipt
	choice := receipt.choice
	if choice.CardPath != "jobs/normalized/"+choice.JobID+".yaml" {
		return emptyFailure(failed("source", "JOB_INTAKE_DUPLICATE_CARD_UNSUPPORTED"))
	}
	if !receipt.consumed.CompareAndSwap(false, true) {
		return emptyFailure(failed("input", "JOB_INTAKE_DUPLICATE_ALREADY_ATTEMPTED"))
	}
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		loaded, cardErr := jobcard.Read(reads.ctx, reads.root, choice.CardPath)
		if cardErr != nil || loaded == nil || loaded.Report == nil || !loaded.Report.SchemaValid() || !loaded.RawPresent ||
			loaded.CardFileSHA256 != choice.CardFileSHA256 || loaded.RawFileSHA256 != choice.RawFileSHA256 ||
			newJobCardField(loaded.Card, "id") != choice.JobID || newJobCardField(loaded.Card, "raw_jd_path") != choice.RawPath {
			return nil, failed("source", "JOB_INTAKE_DUPLICATE_CHANGED")
		}
		card, cardPresent, cardReadErr := reads.root.ReadSingleLinkFile(choice.CardPath, cataloginput.YAMLMaxInputBytes)
		raw, rawPresent, rawReadErr := reads.root.ReadSingleLinkFile(choice.RawPath, jobdigest.MaxSourceBytes)
		if cardReadErr != nil || rawReadErr != nil || !cardPresent || !rawPresent ||
			sha256.Sum256(card) != choice.CardFileSHA256 || sha256.Sum256(raw) != choice.RawFileSHA256 {
			return nil, failed("source", "JOB_INTAKE_DUPLICATE_CHANGED")
		}
		reads.duplicateNewJobWitness = &newJobDuplicateWitness{choice.CardPath, choice.RawPath,
			bytes.Clone(card), bytes.Clone(raw)}
		assessment, prepareErr := s.prepareSavedAssessment(reads, resourceRoot, savedassessment.Request{
			Operation: "load", Fields: map[string]any{"job_id": choice.JobID, "register_application": true}, Month: month,
		})
		if prepareErr != nil {
			return nil, prepareErr
		}
		now := s.now()
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			before, summaryErr := applications.SummariesInTransaction(ctx, tx, []string{choice.JobID})
			if summaryErr != nil {
				return nil, applicationProblem(summaryErr, choice.JobID)
			}
			var statusBefore *string
			if summary, exists := before[choice.JobID]; exists {
				raw, marshalErr := json.Marshal(summary.CurrentStatus)
				if marshalErr != nil || json.Unmarshal(raw, &statusBefore) != nil || statusBefore == nil {
					return nil, failed("result", "JOB_INTAKE_DUPLICATE_RESULT_INVALID")
				}
			}
			assessed, assessErr := assessment(ctx, tx)
			if assessErr != nil {
				return nil, assessErr
			}
			changed := assessed.Changed
			if choice.InitialStatus == "applied" {
				status, statusErr := applications.StatusInTransaction(ctx, tx, choice.JobID, "applied", now, now, "")
				if statusErr != nil {
					return nil, applicationProblem(statusErr, choice.JobID)
				}
				changed = changed || status.Changed
			}
			detail, detailErr := applications.ApplicationInTransaction(ctx, tx, choice.JobID)
			if detailErr != nil {
				return nil, applicationProblem(detailErr, choice.JobID)
			}
			application, marshalErr := json.Marshal(detail)
			if marshalErr != nil {
				return nil, failed("result", "JOB_INTAKE_DUPLICATE_RESULT_INVALID")
			}
			response, marshalErr := json.Marshal(duplicateJobResponse{true, choice.JobID, choice.Reason,
				assessed.Result, application, statusBefore})
			if marshalErr != nil || len(response) > applications.MaxResultBytes {
				return nil, failed("result", "JOB_INTAKE_DUPLICATE_RESULT_INVALID")
			}
			return &applications.MutationResult{Result: response, Changed: changed}, nil
		}, nil
	})
}
