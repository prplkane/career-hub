package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// SaveIntakeReview persists an explicit user's draft, not inferred approval.
// Normalization preserves all strict-model fields and source-compatible defaults.
func (s *Service) SaveIntakeReview(ctx context.Context, id, expectedRevision string, candidate []byte) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || s.expectedHome == nil || s.closeRoot == nil || s.checkpoint == nil || s.now == nil || s.newIntakeID == nil {
		return out, failed("admission", "REVIEW_UI_SAVE_UNAVAILABLE")
	}
	if !intakeReviewSourceID(id) || !intakeReviewRevision(expectedRevision) || len(candidate) == 0 || len(candidate) > 1<<20 {
		return out, failed("validation", "REVIEW_UI_SAVE_FAILED")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return out, failed("context", "REVIEW_UI_SAVE_CANCELED")
	}
	defer release()
	root, pe := runtimepaths.Open(s.root)
	if pe != nil {
		return out, failed("path", "REVIEW_UI_SAVE_UNAVAILABLE")
	}
	defer func() {
		if s.closeRoot(root) != nil {
			out.CleanupFailed, out.CleanupCode = true, "REVIEW_UI_SAVE_CLEANUP_FAILED"
			if problem == nil {
				problem = failed("cleanup", out.CleanupCode)
			}
		}
		if problem != nil {
			out.Result = nil
		}
		if problem == nil || problem.Code != "REVIEW_INTEGRITY_FAILED" || out.CleanupFailed {
			out.ReviewValidation = nil
		}
	}()
	home, readyErr := s.ready(root, nil)
	if readyErr != nil {
		return out, failed("admission", "REVIEW_UI_SAVE_UNAVAILABLE")
	}
	finish, problem := s.beginIntakeOperation(root, out, "review_save")
	if problem != nil {
		return out, problem
	}
	defer func() { problem = finish(problem) }()
	extractionPath, reviewPath, approvalPath := intakeReviewPaths(id)
	extractionRaw, present, pe := root.ReadSingleLinkFile(extractionPath, intakeread.MaxExtractionBytes)
	if pe != nil || !present {
		return out, failed("source", "REVIEW_UI_SAVE_UNAVAILABLE")
	}
	extraction, parseError := intakeread.ParseExtraction(extractionRaw)
	if parseError != nil || extraction.Source.ID != id {
		return out, failed("source", "REVIEW_UI_SAVE_UNAVAILABLE")
	}
	previous, present, pe := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
	if pe != nil || !present {
		return out, failed("review", "REVIEW_UI_SAVE_UNAVAILABLE")
	}
	if approved, pe := root.Exists(approvalPath); pe != nil {
		return out, failed("path", "REVIEW_UI_SAVE_UNAVAILABLE")
	} else if approved {
		return out, failed("guard", "REVIEW_UI_SAVE_APPLIED_READ_ONLY")
	}
	current, revisionError := intakereview.Revision(previous)
	if revisionError != nil {
		return out, failed("review", "REVIEW_UI_SAVE_UNAVAILABLE")
	}
	if current != expectedRevision {
		return out, failed("revision", "REVIEW_REVISION_CONFLICT")
	}
	normalized, normalizeError := intakereview.Normalize(candidate)
	if normalizeError != nil || len(normalized) > intakeread.MaxReviewBytes {
		return out, failed("validation", "REVIEW_UI_SAVE_FAILED")
	}
	review, parseError := intakeread.ParseReview(normalized)
	if parseError != nil {
		return out, failed("validation", "REVIEW_UI_SAVE_FAILED")
	}
	validation := intakeread.ValidateReview(extraction, review)
	if validation.Status == "error" {
		if !reviewInputsUnchanged(root, extractionPath, extractionRaw, reviewPath, previous, true, approvalPath) {
			return out, failed("revision", "REVIEW_REVISION_CONFLICT")
		}
		if _, e := s.ready(root, home); e != nil {
			return out, failed("admission", "REVIEW_UI_SAVE_UNAVAILABLE")
		}
		out.ReviewValidation = &validation
		return out, failed("validation", "REVIEW_INTEGRITY_FAILED")
	}
	// Never accept a client-edited source path/type even if block links match.
	if review.Source != extraction.Source {
		return out, failed("validation", "REVIEW_UI_SAVE_FAILED")
	}
	if e := s.checkpoint("intake_review_save_prepared"); e != nil {
		return out, failed("revision", "REVIEW_REVISION_CONFLICT")
	}
	if _, e := s.ready(root, home); e != nil {
		return out, failed("admission", "REVIEW_UI_SAVE_UNAVAILABLE")
	}
	if !reviewInputsUnchanged(root, extractionPath, extractionRaw, reviewPath, previous, true, approvalPath) {
		return out, failed("revision", "REVIEW_REVISION_CONFLICT")
	}
	if ctx.Err() != nil {
		return out, failed("context", "REVIEW_UI_SAVE_CANCELED")
	}
	out.CommitAttempted = true
	committed, pe := s.reviewMutations.replaceFile(root, reviewPath, previous, normalized)
	out.Committed = committed
	if pe != nil || !committed {
		if !committed && pe != nil && (pe.Code == "FILE_CONFLICT" || pe.Code == "FILE_MISSING") {
			out.CommitAttempted = false
			return out, failed("revision", "REVIEW_REVISION_CONFLICT")
		}
		return out, failed("write", "REVIEW_UI_SAVE_WRITE_UNCERTAIN")
	}
	if e := s.checkpoint("intake_review_saved"); e != nil {
		return out, failed("verification", "REVIEW_UI_SAVE_COMMITTED_UNVERIFIED")
	}
	readback, present, pe := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
	if pe != nil || !present || !bytes.Equal(readback, normalized) || !reviewInputsUnchanged(root, extractionPath, extractionRaw, reviewPath, normalized, true, approvalPath) {
		return out, failed("verification", "REVIEW_UI_SAVE_COMMITTED_UNVERIFIED")
	}
	if _, e := s.ready(root, home); e != nil {
		return out, failed("verification", "REVIEW_UI_SAVE_COMMITTED_UNVERIFIED")
	}
	revision, revisionError := intakereview.Revision(normalized)
	if revisionError != nil {
		return out, failed("verification", "REVIEW_UI_SAVE_COMMITTED_UNVERIFIED")
	}
	out.Result, _ = json.Marshal(struct {
		SourceID       string `json:"source_id"`
		ReviewRevision string `json:"review_revision"`
	}{id, revision})
	return out, nil
}
