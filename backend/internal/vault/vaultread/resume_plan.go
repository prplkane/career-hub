package vaultread

import (
	"context"
	"crypto/sha256"
	"fmt"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

var planPaths = [5]string{"career_vault/experience_blocks.yaml", "career_vault/project_blocks.yaml", "career_vault/skill_blocks.yaml", "career_vault/proof_blocks.yaml", "career_vault/education.yaml"}

type planReader struct {
	source reader
	raw    map[string][]byte
}

func (r *planReader) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	raw, present, e := r.source.ReadFile(path, limit)
	if e == nil && present {
		r.raw[path] = raw
	}
	return raw, present, e
}

// ResumePlanSources validates all eight files but exposes only the five plan
// sources. The combined revision hashes exact observed bytes, not re-opened
// paths or JSON projections; the owner must recheck the complete read set.
func ResumePlanSources(ctx context.Context, source reader) (map[string]cataloginput.Value, string, *Error) {
	if source == nil {
		return nil, "", &Error{Stage: "read", Code: "VAULT_READ_FAILED"}
	}
	r := &planReader{source: source, raw: map[string][]byte{}}
	state, e := loadVault(ctx, r)
	if e != nil {
		return nil, "", e
	}
	if e = validateVault(ctx, state); e != nil {
		return nil, "", e
	}
	h := sha256.New()
	for _, path := range planPaths {
		_, _ = h.Write([]byte(path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(r.raw[path])
		_, _ = h.Write([]byte{0})
	}
	docs := map[string]cataloginput.Value{}
	for _, index := range []int{experienceIndex, projectIndex, skillIndex, proofIndex, educationIndex} {
		docs[schemas[index].path] = state.documents[index]
	}
	return docs, fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}
