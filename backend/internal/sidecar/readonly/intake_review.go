package readonly

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"

	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/intakeview"
	"careerhub.local/backend/internal/runtimepaths"
)

const maxIntakeViewResponseBytes = 4 << 20

var intakeSourceID = regexp.MustCompile(`^src_[a-z0-9_]+$`)

func (w *Workspace) IntakeExtraction(ctx context.Context, sourceID string) (*intakeview.ExtractionDetail, error) {
	if !validIntakeSourceID(sourceID) {
		return nil, fail("INTAKE_SOURCE_ID_INVALID")
	}
	return readIntakeRun(w, ctx, sourceID, "INTAKE_EXTRACTION_NOT_FOUND", func(observed *runtimepaths.ObservedRuns, _, _ *runtimepaths.Root) (*intakeview.ExtractionDetail, error) {
		extraction, e := observed.ReadChildFile(sourceID, "extracted.yaml", intakeread.MaxExtractionBytes)
		if e != nil {
			return nil, fail("INTAKE_EXTRACTION_UNAVAILABLE")
		}
		if !extraction.Present {
			return nil, fail("INTAKE_EXTRACTION_NOT_FOUND")
		}
		review, e := observed.ReadChildFile(sourceID, "review.yaml", intakeread.MaxReviewBytes)
		if e != nil {
			return nil, fail("INTAKE_EXTRACTION_UNAVAILABLE")
		}
		view, projectionFailure := intakeview.Extraction(extraction.Content, extraction.Modified, review.Present)
		if projectionFailure != nil || view == nil || view.ID != sourceID {
			return nil, fail("INTAKE_EXTRACTION_INVALID")
		}
		return view, nil
	})
}

func (w *Workspace) IntakeReview(ctx context.Context, sourceID string) (*intakeview.ReviewDetail, error) {
	if !validIntakeSourceID(sourceID) {
		return nil, fail("INTAKE_SOURCE_ID_INVALID")
	}
	return readIntakeRun(w, ctx, sourceID, "INTAKE_EXTRACTION_NOT_FOUND", func(observed *runtimepaths.ObservedRuns, _, _ *runtimepaths.Root) (*intakeview.ReviewDetail, error) {
		extraction, e := observed.ReadChildFile(sourceID, "extracted.yaml", intakeread.MaxExtractionBytes)
		if e != nil {
			return nil, fail("INTAKE_EXTRACTION_UNAVAILABLE")
		}
		if !extraction.Present {
			return nil, fail("INTAKE_EXTRACTION_NOT_FOUND")
		}
		review, e := observed.ReadChildFile(sourceID, "review.yaml", intakeread.MaxReviewBytes)
		if e != nil {
			return nil, fail("INTAKE_REVIEW_UNAVAILABLE")
		}
		if !review.Present {
			return nil, fail("INTAKE_REVIEW_NOT_FOUND")
		}
		approval, e := observed.ReadChildFile(sourceID, "approval_result.yaml", intakeread.MaxApprovalBytes)
		if e != nil {
			return nil, fail("INTAKE_REVIEW_UNAVAILABLE")
		}
		view, projectionFailure := intakeview.Review(extraction.Content, review.Content, approval.Content, approval.Present, review.Modified)
		if projectionFailure != nil || view == nil || view.SourceID != sourceID {
			return nil, fail("INTAKE_REVIEW_INVALID")
		}
		if approval.Present {
			approvalSource, _ := view.ApprovalApplication["source_id"].(string)
			if approvalSource != sourceID {
				return nil, fail("INTAKE_REVIEW_INVALID")
			}
		}
		return view, nil
	})
}

func validIntakeSourceID(value string) bool {
	return len(value) >= 5 && len(value) <= 512 && intakeSourceID.MatchString(value)
}

func readIntakeRun[T any](w *Workspace, ctx context.Context, sourceID, absentCode string, load func(*runtimepaths.ObservedRuns, *runtimepaths.Root, *runtimepaths.Root) (T, error)) (payload T, failure error) {
	var empty T
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (result T, resultFailure error) {
		present, e := data.Exists(resumeRunsPath)
		if e != nil {
			return empty, fail("INTAKE_RUNS_UNAVAILABLE")
		}
		if !present {
			return empty, fail(absentCode)
		}
		runs, e := runtimepaths.Open(filepath.Join(w.start.DataRoot, filepath.FromSlash(resumeRunsPath)))
		if e != nil {
			return empty, fail("INTAKE_RUNS_UNAVAILABLE")
		}
		defer func() {
			if runs.Close() != nil {
				result, resultFailure = empty, fail("INTAKE_RUNS_CLEANUP_FAILED")
			}
		}()
		observed, e := runs.ObserveRunDirectories()
		if e != nil {
			return empty, fail("INTAKE_RUNS_UNAVAILABLE")
		}
		if _, found := slices.BinarySearch(observed.Names(), sourceID); !found {
			return empty, fail(absentCode)
		}
		if ctx == nil || ctx.Err() != nil {
			return empty, fail("APPLICATION_READ_CANCELED")
		}
		result, resultFailure = load(observed, data, resources)
		if resultFailure != nil {
			return empty, resultFailure
		}
		if e := observed.Recheck(); e != nil {
			return empty, fail("INTAKE_RUNS_CHANGED")
		}
		if ctx == nil || ctx.Err() != nil {
			return empty, fail("APPLICATION_READ_CANCELED")
		}
		encoded, encodeErr := json.Marshal(result)
		if encodeErr != nil || len(encoded) > maxIntakeViewResponseBytes {
			return empty, fail("INTAKE_VIEW_RESULT_LIMIT")
		}
		return result, nil
	})
}
