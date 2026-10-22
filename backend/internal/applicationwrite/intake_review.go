package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/intakereview"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

type reviewFileMutations struct {
	create  func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
	replace func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error)
}

func (m reviewFileMutations) createFile(root *runtimepaths.Root, path string, content []byte) (bool, *runtimepaths.Error) {
	if m.create != nil {
		return m.create(root, path, content)
	}
	return root.CreateFile(path, content)
}

func (m reviewFileMutations) replaceFile(root *runtimepaths.Root, path string, expected, replacement []byte) (bool, *runtimepaths.Error) {
	if m.replace != nil {
		return m.replace(root, path, expected, replacement)
	}
	return root.ReplaceFile(path, expected, replacement)
}

// BuildIntakeReview owns one optimistic review inventory write. It never
// suggests cards, applies the review, or mutates the extraction/source.
func (s *Service) BuildIntakeReview(ctx context.Context, sourceID string, expectedRevision *string) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || s.checkpoint == nil || s.closeRoot == nil || !intakeReviewSourceID(sourceID) ||
		(expectedRevision != nil && !intakeReviewRevision(*expectedRevision)) {
		return out, failed("input", "REVIEW_BUILD_REQUEST_INVALID")
	}
	if s.expectedHome == nil {
		return out, failed("admission", "REVIEW_BUILD_HOME_PIN_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return out, failed("canceled", "REVIEW_BUILD_CANCELED")
	}
	defer release()
	root, openErr := runtimepaths.Open(s.root)
	if openErr != nil {
		return out, failed("path", "REVIEW_BUILD_ROOT_UNAVAILABLE")
	}
	defer func() {
		if closeErr := s.closeRoot(root); closeErr != nil {
			out.CleanupFailed, out.CleanupCode = true, "REVIEW_BUILD_ROOT_CLOSE_FAILED"
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
		return out, failed("source", "REVIEW_BUILD_EXTRACTION_UNSAFE")
	}
	if !present {
		return out, failed("source", "REVIEW_BUILD_EXTRACTION_NOT_FOUND")
	}
	extraction, parseErr := intakeread.ParseExtraction(extractionRaw)
	if parseErr != nil || extraction.Source.ID != sourceID {
		return out, failed("source", "REVIEW_BUILD_EXTRACTION_INVALID")
	}
	approvalPresent, pathErr := root.Exists(approvalPath)
	if pathErr != nil {
		return out, failed("source", "REVIEW_BUILD_APPROVAL_UNSAFE")
	}
	if approvalPresent {
		return out, failed("state", "REVIEW_BUILD_APPLIED_READ_ONLY")
	}
	reviewRaw, reviewPresent, reviewErr := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
	if reviewErr != nil {
		return out, failed("source", "REVIEW_BUILD_EXISTING_UNSAFE")
	}
	if reviewPresent {
		if expectedRevision == nil {
			return out, failed("revision", "REVIEW_BUILD_REVISION_REQUIRED")
		}
		current, revisionErr := intakereview.Revision(reviewRaw)
		if revisionErr != nil {
			return out, failed("source", "REVIEW_BUILD_EXISTING_INVALID")
		}
		if current != *expectedRevision {
			return out, failed("revision", "REVIEW_BUILD_REVISION_CONFLICT")
		}
	} else if expectedRevision != nil {
		return out, failed("revision", "REVIEW_BUILD_REVISION_CONFLICT")
	}
	built, buildErr := intakereview.Build(extractionRaw, optionalReview(reviewRaw, reviewPresent))
	if buildErr != nil {
		return out, failed("build", buildErr.Code)
	}
	if _, readyErr = s.ready(root, home); readyErr != nil {
		return out, readyErr
	}
	if !reviewInputsUnchanged(root, extractionPath, extractionRaw, reviewPath, reviewRaw, reviewPresent, approvalPath) {
		return out, failed("revision", "REVIEW_BUILD_INPUT_CHANGED")
	}
	if ctx.Err() != nil {
		return out, failed("canceled", "REVIEW_BUILD_CANCELED")
	}
	out.CommitAttempted = true
	var committed bool
	var mutationErr *runtimepaths.Error
	if reviewPresent {
		committed, mutationErr = s.reviewMutations.replaceFile(root, reviewPath, reviewRaw, built.Bytes)
	} else {
		committed, mutationErr = s.reviewMutations.createFile(root, reviewPath, built.Bytes)
	}
	out.Committed = committed
	if mutationErr != nil {
		if !committed && (mutationErr.Code == "FILE_CONFLICT" || mutationErr.Code == "FILE_MISSING" || mutationErr.Code == "FILE_EXISTS") {
			out.CommitAttempted = false
			return out, failed("revision", "REVIEW_BUILD_REVISION_CONFLICT")
		}
		return out, failed("write", "REVIEW_BUILD_WRITE_UNCERTAIN")
	}
	if !committed {
		return out, failed("write", "REVIEW_BUILD_WRITE_UNCERTAIN")
	}
	if checkpointErr := s.checkpoint("intake_review_committed"); checkpointErr != nil {
		return out, checkpointErr
	}
	readback, readbackPresent, readbackErr := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
	if readbackErr != nil || !readbackPresent || !bytes.Equal(readback, built.Bytes) {
		return out, failed("verification", "REVIEW_BUILD_COMMITTED_UNVERIFIED")
	}
	if _, readyErr = s.ready(root, home); readyErr != nil {
		return out, failed("verification", "REVIEW_BUILD_COMMITTED_UNVERIFIED")
	}
	result, encodeErr := json.Marshal(struct {
		SourceID               string `json:"source_id"`
		ReviewRevision         string `json:"review_revision"`
		NewDecisionCount       int    `json:"new_decision_count"`
		PreservedDecisionCount int    `json:"preserved_decision_count"`
	}{sourceID, built.Revision, built.NewDecisionCount, built.PreservedDecisionCount})
	if encodeErr != nil {
		return out, failed("response", "REVIEW_BUILD_COMMITTED_UNVERIFIED")
	}
	out.Result = result
	return out, nil
}

func intakeReviewSourceID(value string) bool {
	if len(value) < 5 || len(value) > 512 || !utf8.ValidString(value) || !strings.HasPrefix(value, "src_") {
		return false
	}
	for _, point := range value[4:] {
		if point != '_' && (point < 'a' || point > 'z') && (point < '0' || point > '9') {
			return false
		}
	}
	return true
}

func intakeReviewRevision(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, point := range value[7:] {
		if (point < 'a' || point > 'f') && (point < '0' || point > '9') {
			return false
		}
	}
	return true
}

func intakeReviewPaths(sourceID string) (string, string, string) {
	base := "career_vault/intake_runs/" + sourceID + "/"
	return base + "extracted.yaml", base + "review.yaml", base + "approval_result.yaml"
}

func optionalReview(raw []byte, present bool) []byte {
	if !present {
		return nil
	}
	return raw
}

func reviewInputsUnchanged(root *runtimepaths.Root, extractionPath string, extractionRaw []byte, reviewPath string, reviewRaw []byte, reviewPresent bool, approvalPath string) bool {
	extractionAgain, extractionPresent, extractionErr := root.ReadSingleLinkFile(extractionPath, intakeread.MaxExtractionBytes)
	reviewAgain, reviewStillPresent, reviewErr := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
	approvalPresent, approvalErr := root.Exists(approvalPath)
	return extractionErr == nil && extractionPresent && bytes.Equal(extractionAgain, extractionRaw) &&
		reviewErr == nil && reviewStillPresent == reviewPresent && bytes.Equal(reviewAgain, reviewRaw) &&
		approvalErr == nil && !approvalPresent
}
