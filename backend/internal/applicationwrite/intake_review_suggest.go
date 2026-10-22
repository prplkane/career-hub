package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/intakereview"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

// SuggestIntakeReview owns one optimistic deterministic-card publication. It
// requires an existing review and never creates, applies, or removes a review.
func (s *Service) SuggestIntakeReview(ctx context.Context, sourceID, expectedRevision string) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || s.checkpoint == nil || s.closeRoot == nil ||
		!intakeReviewSourceID(sourceID) || !intakeReviewRevision(expectedRevision) {
		return out, failed("input", "REVIEW_SUGGEST_REQUEST_INVALID")
	}
	if s.expectedHome == nil {
		return out, failed("admission", "REVIEW_SUGGEST_HOME_PIN_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return out, failed("canceled", "REVIEW_SUGGEST_CANCELED")
	}
	defer release()
	root, openErr := runtimepaths.Open(s.root)
	if openErr != nil {
		return out, failed("path", "REVIEW_SUGGEST_ROOT_UNAVAILABLE")
	}
	defer func() {
		if closeErr := s.closeRoot(root); closeErr != nil {
			out.CleanupFailed, out.CleanupCode = true, "REVIEW_SUGGEST_ROOT_CLOSE_FAILED"
			if problem == nil {
				problem = failed("cleanup", out.CleanupCode)
			}
		}
		if problem != nil {
			out.Result = nil
		}
	}()
	home, readyErr := s.ready(root, nil)
	if readyErr != nil {
		return out, readyErr
	}
	extractionPath, reviewPath, approvalPath := intakeReviewPaths(sourceID)
	extractionRaw, present, readErr := root.ReadSingleLinkFile(extractionPath, intakeread.MaxExtractionBytes)
	if readErr != nil {
		return out, failed("source", "REVIEW_SUGGEST_EXTRACTION_UNSAFE")
	}
	if !present {
		return out, failed("source", "REVIEW_SUGGEST_EXTRACTION_NOT_FOUND")
	}
	extraction, parseErr := intakeread.ParseExtraction(extractionRaw)
	if parseErr != nil || extraction.Source.ID != sourceID {
		return out, failed("source", "REVIEW_SUGGEST_EXTRACTION_INVALID")
	}
	approvalPresent, pathErr := root.Exists(approvalPath)
	if pathErr != nil {
		return out, failed("source", "REVIEW_SUGGEST_APPROVAL_UNSAFE")
	}
	if approvalPresent {
		return out, failed("state", "REVIEW_SUGGEST_APPLIED_READ_ONLY")
	}
	reviewRaw, reviewPresent, reviewErr := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
	if reviewErr != nil {
		return out, failed("source", "REVIEW_SUGGEST_REVIEW_UNSAFE")
	}
	if !reviewPresent {
		return out, failed("source", "REVIEW_SUGGEST_REVIEW_NOT_FOUND")
	}
	current, revisionErr := intakereview.Revision(reviewRaw)
	if revisionErr != nil {
		return out, failed("source", "REVIEW_SUGGEST_REVIEW_INVALID")
	}
	if current != expectedRevision {
		return out, failed("revision", "REVIEW_SUGGEST_REVISION_CONFLICT")
	}
	suggested, suggestErr := intakereview.Suggest(extractionRaw, reviewRaw)
	if suggestErr != nil {
		return out, failed("suggest", suggestErr.Code)
	}
	if _, readyErr = s.ready(root, home); readyErr != nil {
		return out, readyErr
	}
	if !reviewInputsUnchanged(root, extractionPath, extractionRaw, reviewPath, reviewRaw, true, approvalPath) {
		return out, failed("revision", "REVIEW_SUGGEST_INPUT_CHANGED")
	}
	if ctx.Err() != nil {
		return out, failed("canceled", "REVIEW_SUGGEST_CANCELED")
	}
	out.CommitAttempted = true
	committed, mutationErr := s.reviewMutations.replaceFile(root, reviewPath, reviewRaw, suggested.Bytes)
	out.Committed = committed
	if mutationErr != nil {
		if !committed && (mutationErr.Code == "FILE_CONFLICT" || mutationErr.Code == "FILE_MISSING") {
			out.CommitAttempted = false
			return out, failed("revision", "REVIEW_SUGGEST_REVISION_CONFLICT")
		}
		return out, failed("write", "REVIEW_SUGGEST_WRITE_UNCERTAIN")
	}
	if !committed {
		return out, failed("write", "REVIEW_SUGGEST_WRITE_UNCERTAIN")
	}
	if checkpointErr := s.checkpoint("intake_review_suggested"); checkpointErr != nil {
		return out, checkpointErr
	}
	readback, readbackPresent, readbackErr := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
	if readbackErr != nil || !readbackPresent || !bytes.Equal(readback, suggested.Bytes) {
		return out, failed("verification", "REVIEW_SUGGEST_COMMITTED_UNVERIFIED")
	}
	if _, readyErr = s.ready(root, home); readyErr != nil {
		return out, failed("verification", "REVIEW_SUGGEST_COMMITTED_UNVERIFIED")
	}
	result, encodeErr := json.Marshal(struct {
		SourceID       string                       `json:"source_id"`
		ReviewRevision string                       `json:"review_revision"`
		Suggestion     intakereview.SuggestionStats `json:"suggestion"`
	}{sourceID, suggested.Revision, suggested.Stats})
	if encodeErr != nil {
		return out, failed("response", "REVIEW_SUGGEST_COMMITTED_UNVERIFIED")
	}
	out.Result = result
	return out, nil
}
