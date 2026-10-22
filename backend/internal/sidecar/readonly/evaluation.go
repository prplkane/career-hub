package readonly

import (
	"context"
	"crypto/sha256"
	"time"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/evaluationinput"
	"careerhub.local/backend/internal/runtimepaths"
)

const conceptsPath = "src/data/requirement_concepts.yaml"

// EvaluationInputs is internal preparation, not a partial assessment. Concepts
// remain immutable. The other fields are newly owned and must not be shared with
// a concurrent writer. No HTTP route or persistent cache is activated here.
type EvaluationInputs = evaluationinput.Inputs

func (w *Workspace) PrepareJobEvidence(ctx context.Context, month assessmentcontext.Month) (*EvaluationInputs, error) {
	if !month.Valid() {
		return nil, fail("EVIDENCE_MONTH_INVALID")
	}
	today := time.Date(month.Year, time.Month(month.Number), 1, 0, 0, 0, 0, time.UTC)
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (*EvaluationInputs, error) {
		return prepareJobEvidence(ctx, data, resources, today)
	})
}

type evaluationSource interface {
	Check(string) *runtimepaths.Error
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}
type evidenceObservation struct {
	path    string
	present bool
	hash    [32]byte
}
type evidenceReader struct {
	ctx      context.Context
	source   evaluationSource
	resource bool
	policy   bool
	observed []evidenceObservation
	bytes    int
}

func (r *evidenceReader) allowed(path string) bool {
	if r.resource {
		return path == catalogstore.CoreGroupsPath || path == catalogstore.CoreSkillsPath || path == conceptsPath
	}
	if r.policy && path == "career_vault/hard_blocker_policy.yaml" {
		return true
	}
	switch path {
	case "career_vault/profile.yaml", "career_vault/education.yaml", "career_vault/visa_profile.yaml", "career_vault/experience_blocks.yaml",
		"career_vault/project_blocks.yaml", "career_vault/skill_blocks.yaml", "career_vault/proof_blocks.yaml", "career_vault/style_rules.yaml",
		catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath:
		return true
	}
	return false
}
func (r *evidenceReader) Check(path string) *runtimepaths.Error {
	if r.ctx == nil || r.ctx.Err() != nil {
		return &runtimepaths.Error{Code: "EVIDENCE_CANCELED"}
	}
	if !r.allowed(path) {
		return &runtimepaths.Error{Code: "EVIDENCE_PATH_INVALID"}
	}
	return r.source.Check(path)
}
func (r *evidenceReader) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if r.ctx == nil || r.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "EVIDENCE_CANCELED"}
	}
	if !r.allowed(path) || limit != cataloginput.YAMLMaxInputBytes {
		return nil, false, &runtimepaths.Error{Code: "EVIDENCE_PATH_INVALID"}
	}
	raw, present, err := r.source.ReadFile(path, limit)
	if err != nil {
		return nil, false, err
	}
	if r.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "EVIDENCE_CANCELED"}
	}
	r.bytes += len(raw)
	if int64(len(raw)) > limit || r.bytes > 32<<20 {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	observation := evidenceObservation{path: path, present: present, hash: sha256.Sum256(raw)}
	for _, old := range r.observed {
		if old.path == path {
			if old != observation {
				return nil, false, &runtimepaths.Error{Code: "EVIDENCE_INPUT_CHANGED"}
			}
			return raw, present, nil
		}
	}
	r.observed = append(r.observed, observation)
	return raw, present, nil
}
func (r *evidenceReader) verify() error {
	for _, before := range r.observed {
		if err := assessmentCanceled(r.ctx); err != nil {
			return err
		}
		raw, present, err := r.source.ReadFile(before.path, cataloginput.YAMLMaxInputBytes)
		if err != nil {
			return fail("EVIDENCE_READ_FAILED")
		}
		if int64(len(raw)) > cataloginput.YAMLMaxInputBytes {
			return fail("EVIDENCE_INPUT_LIMIT")
		}
		if present != before.present || sha256.Sum256(raw) != before.hash {
			return fail("EVIDENCE_INPUT_CHANGED")
		}
	}
	return assessmentCanceled(r.ctx)
}

func prepareJobEvidence(ctx context.Context, dataSource, resourceSource evaluationSource, today time.Time) (*EvaluationInputs, error) {
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	if dataSource == nil || resourceSource == nil {
		return nil, fail("EVIDENCE_READ_FAILED")
	}
	data := &evidenceReader{ctx: ctx, source: dataSource}
	resources := &evidenceReader{ctx: ctx, source: resourceSource, resource: true}
	result, problem := evaluationinput.Prepare(ctx, data, resources, today)
	if problem != nil {
		return nil, fail(problem.Code)
	}
	if err := data.verify(); err != nil {
		return nil, err
	}
	if err := resources.verify(); err != nil {
		return nil, err
	}
	return result, nil
}
