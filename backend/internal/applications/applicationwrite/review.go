package applicationwrite

import (
	"context"
	"database/sql"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applications/applicationreview"
)

// Review exposes the complete persisted assessment/checklist boundary to
// first-party services. It is not an unauthenticated HTTP or capture API and
// does not replace the higher-level JD source/fingerprint validation.
func (s *Service) Review(ctx context.Context, request applicationreview.Request) (*Outcome, *Error) {
	return s.run(ctx, "", func(_ *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			result, p := applicationreview.ApplyInTransaction(ctx, tx, request, s.now())
			if p != nil {
				return nil, &Error{Stage: p.Stage, Code: p.Code, SourceClass: p.SourceClass, SourceMessage: p.SourceMessage}
			}
			return result, nil
		}, nil
	})
}
