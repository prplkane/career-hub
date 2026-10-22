package materialwrite

import (
	"context"
	"database/sql"

	"careerhub.local/backend/internal/applicationtexts"
)

// SaveApplicationText reuses the existing observed single-file publication owner.
// It never commits SQL, advances status or edits approved/submitted text history.
func (s *Service) SaveApplicationText(ctx context.Context, job string, input applicationtexts.Draft) (*Outcome, *Error) {
	draft, e := applicationtexts.CopyDraft(input)
	if e != nil {
		return &Outcome{}, failed("input", e.Code)
	}
	return s.run(ctx, "", func(ctx context.Context, tx *sql.Tx, data, _ *reads, _, _ string) (*operationProposal, *Error) {
		proposal, e := applicationtexts.PrepareSave(ctx, tx, data, job, draft)
		if e != nil {
			return nil, failed("application_text", e.Code)
		}
		return &operationProposal{Proposal: proposal}, nil
	})
}
