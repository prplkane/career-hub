package applicationwrite

import (
	"bytes"
	"context"
	"database/sql"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/intake/resumeplan"
)

// ResumePlan uses the shared owned transaction boundary. Reads roll back a
// write-free transaction; saves commit only after source/recovery rechecks and
// construction of the entire editor result. No route or installed runtime is
// switched by providing this service.
func (s *Service) ResumePlan(ctx context.Context, r resumeplan.Request) (*Outcome, *Error) {
	if len(r.Payload) > resumeplan.MaxPlanBytes {
		return emptyFailure(failed("input", "RESUME_PLAN_INPUT_LIMIT"))
	}
	r.Payload = bytes.Clone(r.Payload)
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			out, e := resumeplan.ApplyInTransaction(ctx, tx, reads, r, s.now(), s.resumeEntropy)
			if e != nil {
				return nil, &Error{Stage: e.Stage, Code: e.Code, SourceClass: e.SourceClass}
			}
			return out, nil
		}, nil
	})
}
