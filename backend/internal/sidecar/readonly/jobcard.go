package readonly

import (
	"context"

	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/runtimepaths"
)

// ReadJobCard requires existing native ownership/admission and fresh validation.
// It does not activate a route, evaluate a job, trust cached digest provenance,
// write data, or fetch its source URL.
func (w *Workspace) ReadJobCard(ctx context.Context, relative string) (*jobcard.Loaded, error) {
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (*jobcard.Loaded, error) {
		result, err := jobcard.Read(ctx, data, relative)
		if err != nil {
			return nil, fail(err.Code)
		}
		return result, nil
	})
}
