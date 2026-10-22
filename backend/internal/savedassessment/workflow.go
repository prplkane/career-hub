package savedassessment

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"careerhub.local/backend/internal/applicationreview"
	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/jobevaluation"
)

type response struct {
	*jobevaluation.Result
	JobStatus       string          `json:"job_status"`
	Mode            string          `json:"assessment_mode"`
	ComputedAt      string          `json:"assessment_computed_at"`
	ContextRevision string          `json:"assessment_context_revision"`
	Revision        string          `json:"assessment_revision"`
	Resolutions     json.RawMessage `json:"user_resolutions"`
}
type Result struct {
	Assessment response `json:"assessment"`
	CacheHit   bool     `json:"cache_hit"`
}

func reviewError(p *applicationreview.Error) *Error {
	if p == nil {
		return nil
	}
	return &Error{p.Stage, p.Code, p.SourceClass, p.SourceMessage}
}
func review(ctx context.Context, tx *sql.Tx, op string, fields map[string]any, clock string) (json.RawMessage, *Error) {
	result, p := applicationreview.ApplyInTransaction(ctx, tx, applicationreview.Request{Operation: op, Fields: fields}, clock)
	if p != nil {
		return nil, reviewError(p)
	}
	return result.Result, nil
}

// ApplyInTransaction consumes only a freshly prepared private artifact. All SQL
// registration/cache/history writes and the final bounded response are inside
// the caller's single transaction. Any later failure requires rollback, not a
// compensating deletion or a partial success. No file writes occur.
func (p *Prepared) ApplyInTransaction(ctx context.Context, tx *sql.Tx, clock string) (*applications.MutationResult, *Error) {
	if er := active(ctx); er != nil {
		return nil, er
	}
	if p == nil || p.evaluate == nil || tx == nil {
		return nil, fail("CONFIGURATION_INVALID")
	}
	t, er := time.Parse("2006-01-02T15:04:05Z", clock)
	if er != nil || t.Year() < 1 || t.Year() > 9999 || t.Format("2006-01-02T15:04:05Z") != clock {
		return nil, fail("CLOCK_INVALID")
	}
	changed := false
	if p.register {
		registration, e := applications.RegisterInTransaction(ctx, tx, []applications.JobSnapshot{p.snapshot}, clock)
		if e != nil {
			return nil, fail(e.Code)
		}
		changed = registration.Changed
	}
	stored, re := applicationreview.ReadVersionedAssessment(ctx, tx, p.id)
	if re != nil {
		return nil, reviewError(re)
	}
	var assessment *jobevaluation.Result
	if stored != nil {
		assessment = p.restore(*stored)
	}
	cached := assessment != nil
	if !cached {
		evaluated, evaluationError := p.evaluate(ctx, p.input)
		if evaluationError != nil {
			return nil, fail(evaluationError.Code)
		}
		if evaluated == nil {
			return nil, fail("EVALUATOR_UNAVAILABLE")
		}
		assessment = evaluated
		record := p.record(clock)
		raw, e := pack(record, evaluated, p.status)
		if e != nil {
			return nil, e
		}
		record.Artifact = raw
		stored, re = applicationreview.SaveVersionedAssessment(ctx, tx, record)
		if re != nil {
			return nil, reviewError(re)
		}
		changed = true
	}
	revision, re := applicationreview.ResolutionRevision(stored.Identity())
	if re != nil {
		return nil, reviewError(re)
	}
	if p.operation == "resolve" {
		if p.expected != revision {
			return nil, fail("REVISION_CONFLICT")
		}
		if !findingExists(assessment, p.finding) {
			return nil, fail("FINDING_NOT_FOUND")
		}
		if _, err := review(ctx, tx, "save_resolution", map[string]any{"job_id": p.id, "finding_key": p.finding, "resolution": p.resolution, "note": p.note}, clock); err != nil {
			return nil, err
		}
		changed = true
	}
	resolutions, err := review(ctx, tx, "list_resolutions", map[string]any{"job_id": p.id, "current_assessment_revision": revision}, clock)
	if err != nil {
		return nil, err
	}
	result := Result{Assessment: response{Result: assessment, JobStatus: p.status, Mode: "advisory_only",
		ComputedAt: stored.ComputedAt, ContextRevision: stored.ContextRevision, Revision: revision, Resolutions: resolutions}, CacheHit: cached}
	raw, er := json.Marshal(result)
	if er != nil || len(raw) > applications.MaxResultBytes {
		return nil, fail("RESPONSE_LIMIT")
	}
	if err := active(ctx); err != nil {
		return nil, err
	}
	return &applications.MutationResult{Result: raw, Changed: changed}, nil
}
