package readonly

import (
	"context"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/runtimepaths"
)

// These internal cache-input services do not publish a fit verdict or add an
// HTTP route. The candidate projection is deliberately not a full Vault editor
// validation: invalid/missing relevant facts must still invalidate cached input.
func (w *Workspace) AssessmentCandidateProjection(ctx context.Context) (cataloginput.Value, error) {
	return readAssessment(w, ctx, func(data, resources *runtimepaths.Root) (cataloginput.Value, *assessmentcontext.Error) {
		return assessmentcontext.ReadCandidateProjection(ctx, data)
	})
}
func (w *Workspace) AssessmentCandidateRevision(ctx context.Context) (string, error) {
	return readAssessment(w, ctx, func(data, resources *runtimepaths.Root) (string, *assessmentcontext.Error) {
		return assessmentcontext.ReadCandidateRevision(ctx, data)
	})
}
func (w *Workspace) AssessmentContextRevision(ctx context.Context, month assessmentcontext.Month) (string, error) {
	if !month.Valid() {
		return "", fail("ASSESSMENT_MONTH_INVALID")
	}
	return readAssessment(w, ctx, func(data, resources *runtimepaths.Root) (string, *assessmentcontext.Error) {
		return assessmentcontext.ReadContextRevision(ctx, data, resources, month)
	})
}
func (w *Workspace) AssessmentRevisions(ctx context.Context, month assessmentcontext.Month) (assessmentcontext.Revisions, error) {
	if !month.Valid() {
		return assessmentcontext.Revisions{}, fail("ASSESSMENT_MONTH_INVALID")
	}
	return readAssessment(w, ctx, func(data, resources *runtimepaths.Root) (assessmentcontext.Revisions, *assessmentcontext.Error) {
		return assessmentcontext.ReadRevisions(ctx, data, resources, month)
	})
}

// AssessmentCacheIdentity supplies v2 input identity, not a cache-hit verdict.
// The complete assessment route remains disabled until fresh validation and
// digest/evaluation/artifact persistence can be composed without partial output.
func (w *Workspace) AssessmentCacheIdentity(ctx context.Context, input assessmentcontext.CacheInputs) (assessmentcontext.CacheIdentity, error) {
	if err := input.Validate(); err != nil {
		return assessmentcontext.CacheIdentity{}, assessmentReadError(err)
	}
	return readAssessment(w, ctx, func(data, resources *runtimepaths.Root) (assessmentcontext.CacheIdentity, *assessmentcontext.Error) {
		return assessmentcontext.ReadCacheIdentity(ctx, data, resources, input)
	})
}
func assessmentCanceled(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return fail("ASSESSMENT_INPUT_CANCELED")
	}
	return nil
}
func admittedAssessment[T any](ctx context.Context, check func() error, load func() (T, error)) (T, error) {
	var empty T
	if err := assessmentCanceled(ctx); err != nil {
		return empty, err
	}
	if err := check(); err != nil {
		return empty, err
	}
	if err := assessmentCanceled(ctx); err != nil {
		return empty, err
	}
	payload, loadErr := load() // Includes native handle cleanup, even on domain failure.
	if err := assessmentCanceled(ctx); err != nil {
		return empty, err
	}
	if err := check(); err != nil {
		return empty, err
	}
	if err := assessmentCanceled(ctx); err != nil {
		return empty, err
	}
	if loadErr != nil {
		return empty, loadErr
	}
	return payload, nil
}
func readAssessment[T any](w *Workspace, ctx context.Context, load func(*runtimepaths.Root, *runtimepaths.Root) (T, *assessmentcontext.Error)) (T, error) {
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (T, error) {
		payload, failure := load(data, resources)
		if failure != nil {
			var empty T
			return empty, assessmentReadError(failure)
		}
		return payload, nil
	})
}

// Shared only by internal assessment reads. Native ownership, admission and
// cleanup are the same for identities and full evidence preparation.
func readOwnedAssessment[T any](w *Workspace, ctx context.Context, load func(*runtimepaths.Root, *runtimepaths.Root) (T, error)) (payload T, failure error) {
	var empty T
	if err := assessmentCanceled(ctx); err != nil {
		return empty, err
	}
	if w == nil || w.dataHomeID == "" {
		return empty, fail("DATA_HOME_UNAVAILABLE")
	}
	data, pathErr := runtimepaths.Open(w.start.DataRoot)
	if pathErr != nil {
		return empty, fail("ASSESSMENT_ROOT_UNAVAILABLE")
	}
	defer func() {
		if data.Close() != nil {
			payload, failure = empty, fail("ASSESSMENT_CLEANUP_FAILED")
		}
	}()
	// Check the existing native claim before rereading private admission metadata.
	if err := data.RequireProcessOwnership(); err != nil {
		if err.Code == "OWNERSHIP_UNSUPPORTED" {
			return empty, fail("ASSESSMENT_OWNERSHIP_UNSUPPORTED")
		}
		return empty, fail("ASSESSMENT_OWNERSHIP_REQUIRED")
	}
	resources, pathErr := runtimepaths.Open(w.start.ResourceRoot)
	if pathErr != nil {
		return empty, fail("ASSESSMENT_ROOT_UNAVAILABLE")
	}
	defer func() {
		if resources.Close() != nil {
			payload, failure = empty, fail("ASSESSMENT_CLEANUP_FAILED")
		}
	}()
	return admittedAssessment(ctx, w.CheckAdmission, func() (T, error) {
		payload, loadErr := load(data, resources)
		resourceClose, dataClose := resources.Close(), data.Close()
		if resourceClose != nil || dataClose != nil {
			return empty, fail("ASSESSMENT_CLEANUP_FAILED")
		}
		if loadErr != nil {
			return empty, loadErr
		}
		return payload, nil
	})
}
func assessmentReadError(err *assessmentcontext.Error) error {
	switch err.Code {
	case "ASSESSMENT_INPUT_CANCELED", "ASSESSMENT_ROOT_UNAVAILABLE", "ASSESSMENT_INPUT_READ_FAILED",
		"ASSESSMENT_INPUT_LIMIT", "ASSESSMENT_INPUT_CHANGED", "ASSESSMENT_YAML_INVALID", "ASSESSMENT_INPUT_UNSUPPORTED",
		"ASSESSMENT_MONTH_INVALID", "ASSESSMENT_PROJECTION_LIMIT", "ASSESSMENT_PROJECTION_UNSUPPORTED",
		"ASSESSMENT_CACHE_VERSION_INVALID", "ASSESSMENT_CACHE_SCOPE_INELIGIBLE", "ASSESSMENT_CACHE_CAPTURE_INVALID", "ASSESSMENT_CACHE_INPUT_LIMIT":
		return fail(err.Code)
	default:
		return fail("ASSESSMENT_READ_FAILED")
	}
}
