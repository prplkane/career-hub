package applicationwrite

import (
	"context"
	"database/sql"

	"careerhub.local/backend/internal/applicationchecklist"
	"careerhub.local/backend/internal/applications"
)

// Checklist loads/verifies one retained JD before composing its complete
// checklist workflow within this owner's transaction. No external adapter may
// replace this verification with a direct Review persistence call.
func (s *Service) Checklist(ctx context.Context, request applicationchecklist.Request) (*Outcome, *Error) {
	convert := func(p *applicationchecklist.Error) *Error {
		if p == nil {
			return nil
		}
		return &Error{Stage: p.Stage, Code: p.Code, SourceClass: p.SourceClass, SourceMessage: p.SourceMessage}
	}
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		prepared, p := applicationchecklist.Prepare(reads.ctx, reads, request)
		if p != nil {
			return nil, convert(p)
		}
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			result, p := prepared.ApplyInTransaction(ctx, tx, s.now())
			return result, convert(p)
		}, nil
	})
}
