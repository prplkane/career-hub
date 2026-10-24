package applicationwrite

import (
	"bytes"
	"context"
	"database/sql"
	"strings"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/jobs/savedassessment"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type assessmentObservation struct {
	path    string
	limit   int64
	raw     []byte
	present bool
}
type assessmentSource struct {
	ctx      context.Context
	root     *runtimepaths.Root
	resource bool
	budget   *int
	observed []assessmentObservation
}
type assessmentReads struct{ data, resources *assessmentSource }

func (r *assessmentSource) Check(path string) *runtimepaths.Error {
	if r.ctx == nil || r.ctx.Err() != nil {
		return &runtimepaths.Error{Code: "ASSESSMENT_CANCELED"}
	}
	allowed := false
	if r.resource {
		allowed = path == "src/data/job_technologies.yaml" || path == "src/data/skill_groups.yaml" || path == "src/data/requirement_concepts.yaml"
	} else {
		switch path {
		case "career_vault/profile.yaml", "career_vault/education.yaml", "career_vault/visa_profile.yaml", "career_vault/experience_blocks.yaml",
			"career_vault/project_blocks.yaml", "career_vault/skill_blocks.yaml", "career_vault/proof_blocks.yaml", "career_vault/style_rules.yaml",
			"career_vault/hard_blocker_policy.yaml", "career_vault/taxonomy/skill_groups.yaml", "career_vault/taxonomy/technologies.yaml":
			allowed = true
		}
		// Only the full Job Card validator selects one incoming source. There is
		// no public filename endpoint or discovery/document/mail subtree fallback.
		allowed = allowed || strings.HasPrefix(path, "jobs/normalized/") || strings.HasPrefix(path, "jobs/incoming/")
	}
	if !allowed {
		return &runtimepaths.Error{Code: "ASSESSMENT_PATH_INVALID"}
	}
	return r.root.Check(path)
}
func (r *assessmentSource) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if e := r.Check(path); e != nil {
		return nil, false, e
	}
	if limit < 1 || limit > 4<<20 {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	for _, seen := range r.observed {
		if seen.path == path {
			if int64(len(seen.raw)) > limit {
				return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
			}
			return bytes.Clone(seen.raw), seen.present, nil
		}
	}
	raw, present, e := r.root.ReadSingleLinkFile(path, limit)
	if e != nil {
		return nil, false, e
	}
	if r.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "ASSESSMENT_CANCELED"}
	}
	if int64(len(raw)) > limit || len(raw) > 32<<20-*r.budget || len(r.observed) >= 32 {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	*r.budget += len(raw)
	r.observed = append(r.observed, assessmentObservation{path, limit, bytes.Clone(raw), present})
	return raw, present, nil
}
func (r *assessmentReads) unchanged() *Error {
	for _, source := range []*assessmentSource{r.data, r.resources} {
		for _, seen := range source.observed {
			if source.ctx.Err() != nil {
				return canceled()
			}
			if source.Check(seen.path) != nil {
				return failed("source", "SAVED_ASSESSMENT_SOURCE_CHANGED")
			}
			raw, present, e := source.root.ReadSingleLinkFile(seen.path, seen.limit)
			if e != nil || present != seen.present || !bytes.Equal(raw, seen.raw) {
				return failed("source", "SAVED_ASSESSMENT_SOURCE_CHANGED")
			}
		}
	}
	return nil
}

// AssessSaved borrows the existing transaction/process owner. Resource root and
// evaluation month are trusted host configuration, never a renderer path or rule
// version. load defaults to registration; it records only Captured, never Applied.
func (s *Service) AssessSaved(ctx context.Context, resourceRoot string, request savedassessment.Request) (*Outcome, *Error) {
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		return s.prepareSavedAssessment(reads, resourceRoot, request)
	})
}

func (s *Service) prepareSavedAssessment(reads *sourceReads, resourceRoot string, request savedassessment.Request) (mutation, *Error) {
	resources, e := runtimepaths.Open(resourceRoot)
	if e != nil {
		return nil, failed("resources", e.Code)
	}
	budget := 0
	ar := &assessmentReads{data: &assessmentSource{ctx: reads.ctx, root: reads.root, budget: &budget},
		resources: &assessmentSource{ctx: reads.ctx, root: resources, resource: true, budget: &budget}}
	reads.assessmentReads = ar // cleanup is retained even if preparation fails.
	prepared, p := savedassessment.Prepare(reads.ctx, ar.data, ar.resources, request)
	if p != nil {
		return nil, &Error{p.Stage, p.Code, p.SourceClass, p.SourceMessage}
	}
	now := s.now()
	return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
		result, p := prepared.ApplyInTransaction(ctx, tx, now)
		if p != nil {
			return nil, &Error{p.Stage, p.Code, p.SourceClass, p.SourceMessage}
		}
		return result, nil
	}, nil
}
