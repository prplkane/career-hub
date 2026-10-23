package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/intake/intakeview"
	"careerhub.local/backend/internal/intake/sourceupload"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

type intakeReviewBuildReceipt struct {
	SourceID               string `json:"source_id"`
	ReviewRevision         string `json:"review_revision"`
	NewDecisionCount       int    `json:"new_decision_count"`
	PreservedDecisionCount int    `json:"preserved_decision_count"`
}

type intakeReviewBuildOperation struct {
	Status                 string `json:"status"`
	NewDecisionCount       int    `json:"new_decision_count"`
	PreservedDecisionCount int    `json:"preserved_decision_count"`
}

type intakeReviewBuildResponse struct {
	*intakeview.ReviewDetail
	Operation intakeReviewBuildOperation `json:"operation"`
}

type intakeReviewSuggestReceipt struct {
	SourceID       string                       `json:"source_id"`
	ReviewRevision string                       `json:"review_revision"`
	Suggestion     intakereview.SuggestionStats `json:"suggestion"`
}

type intakeReviewSuggestResponse struct {
	*intakeview.ReviewDetail
	Suggestion intakereview.SuggestionStats `json:"suggestion"`
}

func intakeActions(w *readonly.Workspace, start protocol.StartRecord) func(context.Context, httpapi.IntakeRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	owner := applicationwrite.New(start.DataRoot)
	return func(ctx context.Context, request httpapi.IntakeRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		switch request.Operation {
		case "review.keep", "review.remove_source":
			home, err := w.StartupHome()
			if err != nil {
				return &httpapi.ApplicationOutcome{}, intakeError("APPROVED_IMPORT_UNAVAILABLE")
			}
			var value *applicationwrite.Outcome
			var problem *applicationwrite.Error
			if request.Operation == "review.keep" {
				value, problem = owner.PinnedToHome(home).KeepApprovedImport(ctx, request.SourceID)
			} else {
				value, problem = owner.PinnedToHome(home).RemoveApprovedImport(ctx, request.SourceID)
			}
			out := intakeWriteOutcome(value)
			if problem != nil {
				code := "APPROVED_IMPORT_RESUME_SAVE_FAILED"
				if request.Operation == "review.remove_source" {
					code = "APPROVED_IMPORT_SOURCE_REMOVE_FAILED"
				}
				if strings.HasPrefix(problem.Code, "APPROVED_IMPORT_") && sourceupload.ValidError(problem.Code) {
					code = problem.Code
				}
				switch problem.SourceClass {
				case "ResumeVersionArchived":
					code = "APPROVED_IMPORT_RESUME_ARCHIVED"
				case "ResumeVersionFileMissing", "ResumeVersionFileInvalid":
					code = "APPROVED_IMPORT_RESUME_UNAVAILABLE"
				case "ApplicationHistoryError":
					code = "APPROVED_IMPORT_RESUME_INVALID"
				}
				if out != nil {
					out.Result = nil
					out.Partial = out.Committed
				}
				return out, intakeError(code)
			}
			if request.Operation == "review.keep" {
				var result struct {
					Resume json.RawMessage `json:"resume"`
					Reused *bool           `json:"reused"`
				}
				if out == nil || json.Unmarshal(out.Result, &result) != nil || result.Reused == nil || len(result.Resume) == 0 || string(result.Resume) == "null" {
					return out, intakeError("APPROVED_IMPORT_RESUME_SAVE_FAILED")
				}
				out.Result, err = json.Marshal(struct {
					Resume       json.RawMessage `json:"resume"`
					AlreadySaved bool            `json:"already_saved"`
				}{result.Resume, *result.Reused})
				if err != nil {
					return out, intakeError("APPROVED_IMPORT_RESUME_SAVE_FAILED")
				}
			}
			return out, nil
		case "review.apply":
			if request.ExpectedReviewRevision == nil {
				return &httpapi.ApplicationOutcome{}, intakeError("APPROVAL_INVALID")
			}
			home, err := w.StartupHome()
			if err != nil {
				return &httpapi.ApplicationOutcome{}, intakeError("APPROVAL_UNAVAILABLE")
			}
			value, problem := owner.PinnedToHome(home).PinnedToResources(start.ResourceRoot).ApplyIntakeApproval(ctx,
				request.SourceID, *request.ExpectedReviewRevision, request.ExpectedVaultRevision, request.ExpectedRegistryRevision)
			out := intakeWriteOutcome(value)
			if problem != nil {
				code := problem.Code
				if !sourceupload.ValidError(code) {
					code = "APPROVAL_UNAVAILABLE"
				}
				if out != nil {
					out.Result = nil
					out.Partial = out.Committed
				}
				return out, intakeError(code)
			}
			if value == nil || out == nil || !out.Committed || !value.ApprovalWrite.Completed || out.RecoveryRequired {
				return out, intakeError("APPROVAL_COMMITTED_UNVERIFIED")
			}
			result, parseErr := intakeread.ParseApproval(value.Result)
			if parseErr != nil || result.SourceID != request.SourceID {
				out.Result, out.Partial = nil, true
				return out, intakeError("APPROVAL_COMMITTED_UNVERIFIED")
			}
			return out, nil
		case "review.preview":
			value, err := w.IntakeApprovalPreview(ctx, request.SourceID)
			return intakeReadResult(value, err)
		case "review.save":
			if request.ExpectedReviewRevision == nil {
				return &httpapi.ApplicationOutcome{}, intakeError("REVIEW_UI_SAVE_FAILED")
			}
			home, err := w.StartupHome()
			if err != nil {
				return &httpapi.ApplicationOutcome{}, intakeError("REVIEW_UI_SAVE_UNAVAILABLE")
			}
			value, problem := owner.PinnedToHome(home).SaveIntakeReview(ctx, request.SourceID, *request.ExpectedReviewRevision, request.Review)
			out := intakeWriteOutcome(value)
			if problem != nil {
				code := problem.Code
				if !sourceupload.ValidError(code) && code != "REVIEW_UI_SAVE_CLEANUP_FAILED" {
					code = "REVIEW_UI_SAVE_UNAVAILABLE"
				}
				if out != nil && out.Committed {
					out.Partial = true
				}
				return out, intakeError(code)
			}
			var receipt intakeReviewBuildReceipt
			if out == nil || !out.Committed || value == nil || json.Unmarshal(value.Result, &receipt) != nil || !validIntakeBuildReceipt(receipt, request.SourceID) {
				return out, intakeError("REVIEW_UI_SAVE_COMMITTED_UNVERIFIED")
			}
			detail, readErr := w.IntakeReview(ctx, request.SourceID)
			if readErr != nil || detail == nil || detail.ReviewRevision != receipt.ReviewRevision {
				out.Partial = true
				return out, intakeError("REVIEW_UI_SAVE_COMMITTED_UNVERIFIED")
			}
			out.Result, err = json.Marshal(detail)
			if err != nil {
				out.Partial = true
				return out, intakeError("REVIEW_UI_SAVE_COMMITTED_UNVERIFIED")
			}
			return out, nil
		case "source.extract":
			home, err := w.StartupHome()
			if err != nil {
				return &httpapi.ApplicationOutcome{}, intakeError("SOURCE_EXTRACTION_UNAVAILABLE")
			}
			value, problem := owner.PinnedToHome(home).PinnedToResources(start.ResourceRoot).ExtractIntakeSource(ctx, request.SourceName, request.Overwrite)
			out := intakeWriteOutcome(value)
			if problem != nil {
				code := problem.Code
				if !sourceupload.ValidError(code) && code != "SOURCE_EXTRACTION_CLEANUP_FAILED" {
					code = "SOURCE_EXTRACTION_UNAVAILABLE"
				}
				if out != nil && out.Committed {
					out.Partial = true
				}
				return out, intakeError(code)
			}
			return out, nil
		case "source.upload":
			home, err := w.StartupHome()
			if err != nil {
				return &httpapi.ApplicationOutcome{}, intakeError("SOURCE_UPLOAD_UNAVAILABLE")
			}
			value, problem := owner.PinnedToHome(home).UploadIntakeSource(ctx, request.EncodedName, request.Content, request.Reimport)
			out := intakeWriteOutcome(value)
			if problem != nil {
				code := problem.Code
				if !sourceupload.ValidError(code) && code != "SOURCE_UPLOAD_CLEANUP_FAILED" {
					code = "SOURCE_UPLOAD_UNAVAILABLE"
				}
				if out != nil && out.Committed {
					out.Partial = true
				}
				return out, intakeError(code)
			}
			return out, nil
		case "source.check":
			value, err := w.CheckIntakeUpload(ctx, request.EncodedName, request.Content)
			return intakeReadResult(value, err)
		case "extraction.list":
			value, err := w.IntakeExtractions(ctx, request.Offset, request.Limit)
			return intakeReadResult(value, err)
		case "source.list":
			value, err := w.IntakeSources(ctx, request.Offset, request.Limit)
			return intakeReadResult(value, err)
		case "extraction.read":
			value, err := w.IntakeExtraction(ctx, request.SourceID)
			return intakeReadResult(value, err)
		case "review.read":
			value, err := w.IntakeReview(ctx, request.SourceID)
			return intakeReadResult(value, err)
		case "review.build":
			home, err := w.StartupHome()
			if err != nil {
				return &httpapi.ApplicationOutcome{}, intakeError("INTAKE_REVIEW_BUILD_UNAVAILABLE")
			}
			value, problem := owner.PinnedToHome(home).BuildIntakeReview(ctx, request.SourceID, request.ExpectedReviewRevision)
			out := intakeWriteOutcome(value)
			if problem != nil {
				if out != nil && out.Committed {
					out.Partial = true
				}
				return out, intakeError(intakeBuildCode(problem.Code))
			}
			if out == nil || !out.Committed || value == nil {
				return out, intakeError("INTAKE_REVIEW_BUILD_COMMITTED_UNVERIFIED")
			}
			var receipt intakeReviewBuildReceipt
			if json.Unmarshal(value.Result, &receipt) != nil || !validIntakeBuildReceipt(receipt, request.SourceID) {
				out.Partial = true
				return out, intakeError("INTAKE_REVIEW_BUILD_COMMITTED_UNVERIFIED")
			}
			detail, readErr := w.IntakeReview(ctx, request.SourceID)
			if readErr != nil || detail == nil || detail.ReviewRevision != receipt.ReviewRevision {
				out.Partial = true
				return out, intakeError("INTAKE_REVIEW_BUILD_COMMITTED_UNVERIFIED")
			}
			payload, encodeErr := json.Marshal(intakeReviewBuildResponse{detail, intakeReviewBuildOperation{
				Status: detail.Validation.Status, NewDecisionCount: receipt.NewDecisionCount,
				PreservedDecisionCount: receipt.PreservedDecisionCount,
			}})
			if encodeErr != nil {
				out.Partial = true
				return out, intakeError("INTAKE_REVIEW_BUILD_COMMITTED_UNVERIFIED")
			}
			out.Result = payload
			return out, nil
		case "review.suggest":
			if request.ExpectedReviewRevision == nil {
				return &httpapi.ApplicationOutcome{}, intakeError("INTAKE_REVIEW_SUGGEST_REQUEST_INVALID")
			}
			home, err := w.StartupHome()
			if err != nil {
				return &httpapi.ApplicationOutcome{}, intakeError("INTAKE_REVIEW_SUGGEST_UNAVAILABLE")
			}
			value, problem := owner.PinnedToHome(home).SuggestIntakeReview(ctx, request.SourceID, *request.ExpectedReviewRevision)
			out := intakeWriteOutcome(value)
			if problem != nil {
				if out != nil && out.Committed {
					out.Partial = true
				}
				return out, intakeError(intakeSuggestCode(problem.Code))
			}
			if out == nil || !out.Committed || value == nil {
				return out, intakeError("INTAKE_REVIEW_SUGGEST_COMMITTED_UNVERIFIED")
			}
			var receipt intakeReviewSuggestReceipt
			if json.Unmarshal(value.Result, &receipt) != nil || !validIntakeSuggestReceipt(receipt, request.SourceID) {
				out.Partial = true
				return out, intakeError("INTAKE_REVIEW_SUGGEST_COMMITTED_UNVERIFIED")
			}
			detail, readErr := w.IntakeReview(ctx, request.SourceID)
			if readErr != nil || detail == nil || detail.ReviewRevision != receipt.ReviewRevision {
				out.Partial = true
				return out, intakeError("INTAKE_REVIEW_SUGGEST_COMMITTED_UNVERIFIED")
			}
			payload, encodeErr := json.Marshal(intakeReviewSuggestResponse{detail, receipt.Suggestion})
			if encodeErr != nil {
				out.Partial = true
				return out, intakeError("INTAKE_REVIEW_SUGGEST_COMMITTED_UNVERIFIED")
			}
			out.Result = payload
			return out, nil
		default:
			return &httpapi.ApplicationOutcome{}, intakeError("INTAKE_REVIEW_BUILD_REQUEST_INVALID")
		}
	}
}

func intakeSuggestCode(code string) string {
	switch code {
	case "REVIEW_SUGGEST_REQUEST_INVALID":
		return "INTAKE_REVIEW_SUGGEST_REQUEST_INVALID"
	case "REVIEW_SUGGEST_CANCELED":
		return "INTAKE_REVIEW_SUGGEST_CANCELED"
	case "REVIEW_SUGGEST_EXTRACTION_NOT_FOUND":
		return "INTAKE_EXTRACTION_NOT_FOUND"
	case "REVIEW_SUGGEST_EXTRACTION_INVALID":
		return "INTAKE_EXTRACTION_INVALID"
	case "REVIEW_SUGGEST_REVIEW_NOT_FOUND":
		return "INTAKE_REVIEW_NOT_FOUND"
	case "REVIEW_SUGGEST_REVIEW_INVALID":
		return "INTAKE_REVIEW_INVALID"
	case "REVIEW_SUGGEST_REVISION_CONFLICT":
		return "INTAKE_REVIEW_SUGGEST_REVISION_CONFLICT"
	case "REVIEW_SUGGEST_INPUT_CHANGED":
		return "INTAKE_REVIEW_SUGGEST_INPUT_CHANGED"
	case "REVIEW_SUGGEST_APPLIED_READ_ONLY":
		return "INTAKE_REVIEW_SUGGEST_APPLIED_READ_ONLY"
	case "REVIEW_SUGGEST_WRITE_UNCERTAIN":
		return "INTAKE_REVIEW_SUGGEST_WRITE_UNCERTAIN"
	case "REVIEW_SUGGEST_COMMITTED_UNVERIFIED":
		return "INTAKE_REVIEW_SUGGEST_COMMITTED_UNVERIFIED"
	case "REVIEW_SUGGEST_ROOT_CLOSE_FAILED":
		return "INTAKE_REVIEW_SUGGEST_CLEANUP_FAILED"
	case "REVIEW_SUGGEST_EXTRACTION_UNSAFE", "REVIEW_SUGGEST_APPROVAL_UNSAFE", "REVIEW_SUGGEST_REVIEW_UNSAFE",
		"REVIEW_SUGGEST_RESULT_LIMIT", "REVIEW_SUGGEST_RESULT_INVALID", "REVIEW_BUILD_SERIALIZATION_FAILED":
		return "INTAKE_REVIEW_SUGGEST_UNAVAILABLE"
	default:
		return "INTAKE_REVIEW_SUGGEST_UNAVAILABLE"
	}
}

func intakeReadResult(value any, err error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if err != nil {
		code := "INTAKE_REVIEW_UNAVAILABLE"
		var problem *readonly.Error
		if errors.As(err, &problem) {
			switch problem.Code {
			case "APPROVAL_PREVIEW_INCOMPLETE", "APPROVAL_PREVIEW_INPUT_CHANGED", "APPROVAL_PREVIEW_UNAVAILABLE", "INTAKE_READ_CANCELED":
				code = problem.Code
			case "SOURCE_UPLOAD_CHECK_FAILED", "SOURCE_UPLOAD_CHECK_UNAVAILABLE", "INTAKE_EXTRACTION_NOT_FOUND", "INTAKE_REVIEW_NOT_FOUND", "INTAKE_EXTRACTION_INVALID", "INTAKE_REVIEW_INVALID",
				"INTAKE_EXTRACTION_UNAVAILABLE", "INTAKE_REVIEW_UNAVAILABLE", "INTAKE_RUNS_UNAVAILABLE", "INTAKE_RUNS_CLEANUP_FAILED",
				"INTAKE_RUNS_CHANGED", "INTAKE_VIEW_RESULT_LIMIT", "INTAKE_SOURCES_UNAVAILABLE", "INTAKE_SOURCES_CHANGED", "INTAKE_QUERY_INVALID":
				code = problem.Code
			case "APPLICATION_READ_CANCELED":
				code = "INTAKE_READ_CANCELED"
			}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = "INTAKE_READ_CANCELED"
		}
		return &httpapi.ApplicationOutcome{}, intakeError(code)
	}
	raw, encodeErr := json.Marshal(value)
	if encodeErr != nil {
		return &httpapi.ApplicationOutcome{}, intakeError("INTAKE_REVIEW_UNAVAILABLE")
	}
	return &httpapi.ApplicationOutcome{Result: raw}, nil
}

func intakeWriteOutcome(value *applicationwrite.Outcome) *httpapi.ApplicationOutcome {
	if value == nil {
		return nil
	}
	out := &httpapi.ApplicationOutcome{
		RunID:  value.RunID,
		Result: value.Result, Committed: value.Committed, CleanupFailed: value.CleanupFailed,
		CommitUnknown: value.CommitAttempted && !value.Committed, RecoveryRequired: value.RecoveryRequired,
		Partial: value.Committed && value.CleanupFailed,
	}
	if value.ReviewValidation != nil {
		validation := intakeview.ReviewValidationView(*value.ReviewValidation)
		out.ReviewValidation = &validation
	}
	return out
}

func intakeBuildCode(code string) string {
	switch code {
	case "REVIEW_BUILD_REQUEST_INVALID":
		return "INTAKE_REVIEW_BUILD_REQUEST_INVALID"
	case "REVIEW_BUILD_CANCELED":
		return "INTAKE_REVIEW_BUILD_CANCELED"
	case "REVIEW_BUILD_EXTRACTION_NOT_FOUND":
		return "INTAKE_EXTRACTION_NOT_FOUND"
	case "REVIEW_BUILD_EXTRACTION_INVALID":
		return "INTAKE_EXTRACTION_INVALID"
	case "REVIEW_BUILD_REVISION_REQUIRED":
		return "INTAKE_REVIEW_BUILD_REVISION_REQUIRED"
	case "REVIEW_BUILD_REVISION_CONFLICT":
		return "INTAKE_REVIEW_BUILD_REVISION_CONFLICT"
	case "REVIEW_BUILD_INPUT_CHANGED":
		return "INTAKE_REVIEW_BUILD_INPUT_CHANGED"
	case "REVIEW_BUILD_APPLIED_READ_ONLY":
		return "INTAKE_REVIEW_BUILD_APPLIED_READ_ONLY"
	case "REVIEW_BUILD_WRITE_UNCERTAIN":
		return "INTAKE_REVIEW_BUILD_WRITE_UNCERTAIN"
	case "REVIEW_BUILD_COMMITTED_UNVERIFIED":
		return "INTAKE_REVIEW_BUILD_COMMITTED_UNVERIFIED"
	case "REVIEW_BUILD_ROOT_CLOSE_FAILED":
		return "INTAKE_REVIEW_BUILD_CLEANUP_FAILED"
	case "REVIEW_BUILD_EXTRACTION_UNSAFE", "REVIEW_BUILD_APPROVAL_UNSAFE", "REVIEW_BUILD_EXISTING_UNSAFE",
		"REVIEW_BUILD_EXISTING_INVALID", "REVIEW_BUILD_SOURCE_MISMATCH", "REVIEW_BUILD_DUPLICATE_BLOCK",
		"REVIEW_BUILD_STALE_BLOCK", "REVIEW_BUILD_RESULT_LIMIT", "REVIEW_BUILD_RESULT_INVALID", "REVIEW_BUILD_SERIALIZATION_FAILED":
		return "INTAKE_REVIEW_BUILD_UNAVAILABLE"
	default:
		return "INTAKE_REVIEW_BUILD_UNAVAILABLE"
	}
}

func intakeError(code string) *httpapi.ApplicationError {
	return &httpapi.ApplicationError{Code: code, Category: "intake_review"}
}

func validIntakeBuildReceipt(value intakeReviewBuildReceipt, sourceID string) bool {
	if value.SourceID != sourceID || value.NewDecisionCount < 0 || value.NewDecisionCount > 1_000_000 ||
		value.PreservedDecisionCount < 0 || value.PreservedDecisionCount > 1_000_000 ||
		len(value.ReviewRevision) != 71 || !strings.HasPrefix(value.ReviewRevision, "sha256:") {
		return false
	}
	for _, point := range value.ReviewRevision[7:] {
		if (point < 'a' || point > 'f') && (point < '0' || point > '9') {
			return false
		}
	}
	return true
}

func validIntakeSuggestReceipt(value intakeReviewSuggestReceipt, sourceID string) bool {
	if value.SourceID != sourceID || !validIntakeBuildReceipt(intakeReviewBuildReceipt{
		SourceID: sourceID, ReviewRevision: value.ReviewRevision,
	}, sourceID) {
		return false
	}
	stats := value.Suggestion
	counts := []int{
		stats.CardCount, stats.NewCardCount, stats.SuggestedLinkCount, stats.AutoAssignedLinkCount,
		stats.ProfileCardCount, stats.ExperienceCardCount, stats.EducationCardCount, stats.ProjectCardCount,
		stats.SummaryCardCount, stats.SkillCardCount, stats.CertificationCardCount, stats.StructuralBlockCount,
	}
	for _, count := range counts {
		if count < 0 || count > 1_000_000 {
			return false
		}
	}
	categoryTotal := stats.ProfileCardCount + stats.ExperienceCardCount + stats.EducationCardCount +
		stats.ProjectCardCount + stats.SummaryCardCount + stats.SkillCardCount + stats.CertificationCardCount
	return stats.CardCount == categoryTotal && stats.NewCardCount <= stats.CardCount
}
