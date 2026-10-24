package readonly

import (
	"context"

	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func (w *Workspace) IntakeApprovalPreview(ctx context.Context, sourceID string) (*intakereview.ApprovalPlan, error) {
	if !validIntakeSourceID(sourceID) {
		return nil, fail("INTAKE_SOURCE_ID_INVALID")
	}
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return nil, fail("INTAKE_READ_CANCELED")
	}
	defer release()
	return readIntakeRun(w, ctx, sourceID, "INTAKE_EXTRACTION_NOT_FOUND", func(observed *runtimepaths.ObservedRuns, data, resources *runtimepaths.Root) (*intakereview.ApprovalPlan, error) {
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
		plan, err := prepareIntakeApprovalPreview(ctx, extraction.Content, review.Content, data, resources)
		if err != nil {
			return nil, err
		}
		if plan.SourceID != sourceID {
			return nil, fail("INTAKE_REVIEW_INVALID")
		}
		return plan, nil
	})
}

func prepareIntakeApprovalPreview(ctx context.Context, extraction, review []byte, dataSource, resourceSource evaluationSource) (*intakereview.ApprovalPlan, error) {
	data := &evidenceReader{ctx: ctx, source: dataSource}
	resources := &evidenceReader{ctx: ctx, source: resourceSource, resource: true}
	vault := map[string][]byte{}
	for _, path := range intakereview.ApprovalPreviewPaths() {
		raw, present, err := data.ReadFile(path, cataloginput.YAMLMaxInputBytes)
		if err != nil || !present {
			return nil, fail("APPROVAL_PREVIEW_UNAVAILABLE")
		}
		vault[path] = raw
	}
	state, catalogErr := catalogstore.ReadState(resources, data)
	if catalogErr != nil {
		return nil, fail("APPROVAL_PREVIEW_UNAVAILABLE")
	}
	skills := map[string]string{}
	for _, skill := range state.Snapshot.Skills() {
		skills[skill.ID] = skill.Name
	}
	plan, problem := intakereview.BuildApprovalPreview(ctx, extraction, review, vault, state.Snapshot.Revision(), skills)
	// Recheck the same exact bytes even for domain refusals; stale readiness is
	// not a useful instruction to the user. No partial candidate list escapes.
	if data.verify() != nil || resources.verify() != nil {
		return nil, fail("APPROVAL_PREVIEW_INPUT_CHANGED")
	}
	if problem != nil {
		switch problem.Code {
		case "APPROVAL_PREVIEW_INCOMPLETE":
			return nil, fail(problem.Code)
		case "APPROVAL_PREVIEW_CANCELED":
			return nil, fail("INTAKE_READ_CANCELED")
		case "APPROVAL_PREVIEW_EXTRACTION_INVALID":
			return nil, fail("INTAKE_EXTRACTION_INVALID")
		case "APPROVAL_PREVIEW_REVIEW_INVALID":
			return nil, fail("INTAKE_REVIEW_INVALID")
		case "APPROVAL_PREVIEW_RESULT_LIMIT", "APPROVAL_PREVIEW_INPUT_LIMIT":
			return nil, fail("INTAKE_VIEW_RESULT_LIMIT")
		default:
			return nil, fail("APPROVAL_PREVIEW_UNAVAILABLE")
		}
	}
	return plan, nil
}
