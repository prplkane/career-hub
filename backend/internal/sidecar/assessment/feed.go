package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/discovery/discoverypreview"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/sidecar/readonly"
)

// Review is the first-party detail projection. It may publish only the separate,
// disposable Go preview cache; it never promotes a job or marks an application.
func (s *Service) Review(ctx context.Context, id string) (*Result, error) {
	if s == nil || s.now == nil {
		return &Result{}, fail("ASSESSMENT_SERVICE_INVALID")
	}
	now := s.now()
	return s.assess(ctx, id, assessmentcontext.Month{Year: now.Year(), Number: int(now.Month())}, true)
}

func (s *Service) finishReview(ctx context.Context, id string, first *discovery.Detail, result *Result) (json.RawMessage, error) {
	again, err := s.workspace.DiscoveryFeedDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	return encodeReview(ctx, first, again, result)
}

func encodeReview(ctx context.Context, first, again *discovery.Detail, result *Result) (json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if first == nil || !reflect.DeepEqual(first, again) {
		return nil, fail("ASSESSMENT_REVIEW_INPUT_CHANGED")
	}
	if result == nil || result.Preview == nil {
		return nil, fail("ASSESSMENT_PREVIEW_INVALID")
	}
	preview := result.Preview
	contextName := preview.Receipt.Availability
	if contextName == "available" {
		contextName = "current_candidate_context"
	}
	detail := *first
	detail.Decision = currentDecision(detail.Record, result)
	// A Python cache timestamp is not evidence of when this native assessment
	// was computed. Unknown timestamps stay empty rather than being borrowed.
	raw, err := json.Marshal(struct {
		*discovery.Detail
		Review map[string]any `json:"review_assessment"`
	}{&detail, map[string]any{"receipt": preview.Receipt, "computed_at": result.ComputedAt, "context": contextName, "cache_hit": preview.CacheHit, "application_instructions": preview.Instructions, "contact_candidates": preview.Contacts}})
	if err != nil || len(raw) > 4<<20 {
		return nil, fail("ASSESSMENT_REVIEW_RESULT_LIMIT")
	}
	return raw, nil
}

func feedContextUnavailable(err error) bool {
	var problem *readonly.Error
	if !errors.As(err, &problem) {
		return false
	}
	switch problem.Code {
	case "EVIDENCE_VAULT_INVALID", "EVIDENCE_CATALOG_INVALID", "EVIDENCE_CONCEPT_INVALID", "DISCOVERY_PREVIEW_POLICY_INVALID":
		return true
	}
	// Integrity, ownership, cancellation and cleanup failures are not missing
	// candidate context and must not become successful detail responses.
	return false
}

func unavailableFeedPreview(detail *discovery.Detail) (*readonly.DiscoveryPreview, error) {
	if detail == nil || !discoverypreview.Eligible(detail.ContentScope) {
		return nil, fail("ASSESSMENT_PREVIEW_INVALID")
	}
	instructions, err := applicationanalysis.ExtractInstructions(detail.SourceText)
	if err != nil {
		return nil, err
	}
	contacts, err := applicationanalysis.ExtractContacts(detail.SourceText)
	if err != nil {
		return nil, err
	}
	receipt, err := discoverypreview.Unavailable(detail.ContentScope, "context_unavailable", instructions.ItemCount, contacts.CandidateCount)
	if err != nil {
		return nil, err
	}
	return &readonly.DiscoveryPreview{Receipt: receipt,
		Instructions: discoverypreview.Instructions{InstructionResult: instructions, Eligible: true, Scope: detail.ContentScope, Message: "Review every exact source-backed step before applying."},
		Contacts:     discoverypreview.Contacts{ContactResult: contacts, Eligible: true, Scope: detail.ContentScope, Message: "Review exact source evidence before saving a contact later."}}, nil
}
