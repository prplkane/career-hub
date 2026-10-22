package applicationwrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/discovery"
)

// FinalizeDiscoveryPromotion links an already captured/assessed job and applies
// the user's explicit action in ONE SQL transaction. Intake files are a prior,
// independently recoverable effect; failure here does not roll them back.
func (s *Service) FinalizeDiscoveryPromotion(ctx context.Context, id string, revision any, jobID, action string) (*Outcome, *Error) {
	if !slices.Contains([]string{"save_job", "prepare_application", "record_as_applied"}, action) {
		return emptyFailure(failed("request", "DISCOVERY_PROMOTION_INVALID"))
	}
	return s.run(ctx, jobID, func(reads *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			now := s.now()
			linked, linkErr := discovery.EditInTransaction(ctx, tx, reads, discovery.EditRequest{
				Operation: "promote", DiscoveryID: id, ExpectedRevision: revision, JobID: jobID,
			}, now)
			if linkErr != nil {
				return nil, discoveryProblem(linkErr)
			}
			current, readErr := applications.SessionJobInTransaction(ctx, tx, jobID)
			if readErr != nil {
				return nil, applicationProblem(readErr, jobID)
			}
			changed := linked.Changed
			if slices.Contains([]string{"captured", "reviewed", "preparing"}, current.Status) {
				var status *applications.MutationResult
				var problem *applications.Error
				switch action {
				case "prepare_application":
					status, problem = applications.StartInTransaction(ctx, tx, jobID, now, now)
				case "record_as_applied":
					status, problem = applications.StatusInTransaction(ctx, tx, jobID, "applied", now, now, "")
				}
				if problem != nil {
					return nil, applicationProblem(problem, jobID)
				}
				if status != nil {
					changed = changed || status.Changed
				}
			}
			application, readErr := applications.ApplicationInTransaction(ctx, tx, jobID)
			if readErr != nil {
				return nil, applicationProblem(readErr, jobID)
			}
			raw, err := json.Marshal(map[string]any{"discovery": linked.Result, "application": application})
			if err != nil {
				return nil, failed("result", "DISCOVERY_PROMOTION_RESULT_INVALID")
			}
			return &applications.MutationResult{Result: raw, Changed: changed}, nil
		}, nil
	})
}
