package applicationwrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"careerhub.local/backend/internal/applications"
)

func (s *Service) RegisterJobs(ctx context.Context, jobs []map[string]any) (*Outcome, *Error) {
	return s.register(ctx, jobs, false)
}
func (s *Service) RegisterJob(ctx context.Context, job map[string]any) (*Outcome, *Error) {
	return s.register(ctx, []map[string]any{job}, true)
}
func (s *Service) register(ctx context.Context, inputs []map[string]any, single bool) (*Outcome, *Error) {
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		jobs, e := snapshots(inputs, reads)
		if e != nil {
			return nil, e
		}
		now := s.now()
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			result, e := applications.RegisterInTransaction(ctx, tx, jobs, now)
			if e == nil && single {
				var records []json.RawMessage
				if json.Unmarshal(result.Result, &records) != nil || len(records) != 1 {
					return nil, failed("result", "APPLICATION_LEDGER_RESPONSE_UNSUPPORTED")
				}
				result.Result = records[0] // Project BEFORE commit, never after.
			}
			return result, applicationProblem(e, "")
		}, nil
	})
}

// IDs here are exact SQLite keys, not registration IDs: never trim them.
func (s *Service) SetDecision(ctx context.Context, id, decision string, notes any) (*Outcome, *Error) {
	if !slices.Contains([]string{"apply", "consider", "skip"}, decision) {
		return emptyFailure(invalid("decision must be one of: apply, consider, skip"))
	}
	note, e := notesInput(notes, true)
	if e != nil {
		return emptyFailure(e)
	}
	if e = boundedText(id, applications.MaxIDBytes); e != nil {
		return emptyFailure(e)
	}
	size := len(id) + len(decision)
	if note != nil {
		size += len(*note)
	}
	if e = requestSize(size); e != nil {
		return emptyFailure(e)
	}
	return s.run(ctx, id, func(_ *sourceReads) (mutation, *Error) {
		now := s.now()
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			result, e := applications.DecisionInTransaction(ctx, tx, id, decision, note, now)
			return result, applicationProblem(e, id)
		}, nil
	})
}

func (s *Service) SetStatus(ctx context.Context, id, status string, occurredAt, note any) (*Outcome, *Error) {
	if !slices.Contains([]string{"applied", "captured", "closed", "interview", "offer", "preparing", "rejected", "withdrawn"}, status) {
		return emptyFailure(invalid("status must be one of: applied, captured, closed, interview, offer, preparing, rejected, withdrawn"))
	}
	text, e := notesInput(note, false)
	if e != nil {
		return emptyFailure(e)
	}
	return s.timed(ctx, id, occurredAt, len(status)+len(*text), func(ctx context.Context, tx *sql.Tx, event, now string) (*applications.MutationResult, *applications.Error) {
		return applications.StatusInTransaction(ctx, tx, id, status, event, now, *text)
	})
}
func (s *Service) StartApplication(ctx context.Context, id string, occurredAt any) (*Outcome, *Error) {
	return s.timed(ctx, id, occurredAt, 0, func(ctx context.Context, tx *sql.Tx, event, now string) (*applications.MutationResult, *applications.Error) {
		return applications.StartInTransaction(ctx, tx, id, event, now)
	})
}
func (s *Service) timed(ctx context.Context, id string, occurredAt any, extraBytes int,
	apply func(context.Context, *sql.Tx, string, string) (*applications.MutationResult, *applications.Error)) (*Outcome, *Error) {
	if s == nil || s.now == nil {
		return emptyFailure(failed("configuration", "APPLICATION_LEDGER_UNAVAILABLE"))
	}
	now := ""
	if occurredAt == nil {
		now = s.now()
	}
	event, e := normalizeTimestamp(occurredAt, now)
	if e != nil {
		return emptyFailure(e)
	}
	recorded := s.now()
	if e = boundedText(id, applications.MaxIDBytes); e != nil {
		return emptyFailure(e)
	}
	if text, ok := occurredAt.(string); ok {
		extraBytes += len(text)
	}
	if e = requestSize(len(id) + extraBytes); e != nil {
		return emptyFailure(e)
	}
	return s.run(ctx, id, func(_ *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			result, e := apply(ctx, tx, event, recorded)
			return result, applicationProblem(e, id)
		}, nil
	})
}
