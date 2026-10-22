package assessmentcontext

import (
	"context"
	"encoding/binary"
	"fmt"
)

// SavedContextPrefix is intentionally different from the discovery context.
// The full normalized card hash (including user edits) stays a separate input.
const SavedContextPrefix = "ch-saved-context-v2:sha256:"

type SavedVersions struct{ Evaluator, Validation, Workflow uint32 }

func candidateIdentity(set *readSet) (string, *Error) {
	ctx := set.ctx
	projection, err := set.projection()
	if err != nil {
		return "", err
	}
	serialized, err := canonicalJSON(ctx, projection)
	if err != nil {
		return "", err
	}
	candidate := newFramedHash("career-hub/candidate/v2")
	for _, source := range set.observations {
		candidate.field(source.spec.path, source.present, nil)
	}
	for i, field := range eligibilityFields {
		candidate.field("eligibility_present:"+field, set.eligibilityPresent[i], nil)
	}
	candidate.field("candidate_projection", true, serialized)
	ordered, err := orderedJSON(ctx, projection)
	if err != nil {
		return "", err
	}
	candidate.field("candidate_projection_order", true, ordered)
	return candidate.finish(candidateKeyPrefix), nil
}

func SavedRevisionsValid(value Revisions) bool {
	return validKey(value.Candidate, candidateKeyPrefix) && validKey(value.Context, SavedContextPrefix)
}

// ReadSavedRevisions shares candidate-v2 framing but has its own consumed-input
// manifest. UI location conclusions also depend on two profile fields; contact
// details and private documents do not enter this identity.
func ReadSavedRevisions(ctx context.Context, data, resources Reader, month Month, versions SavedVersions, sourceSHA256 string) (Revisions, *Error) {
	if err := canceled(ctx); err != nil {
		return Revisions{}, err
	}
	if !month.Valid() {
		return Revisions{}, cacheError("ASSESSMENT_MONTH_INVALID")
	}
	if !lowerSHA256(sourceSHA256) {
		return Revisions{}, cacheError("ASSESSMENT_SOURCE_HASH_INVALID")
	}
	if versions.Evaluator == 0 || versions.Validation == 0 || versions.Workflow == 0 {
		return Revisions{}, cacheError("ASSESSMENT_CACHE_VERSION_INVALID")
	}
	set := readSet{ctx: ctx, data: data, resources: resources}
	candidate, err := candidateIdentity(&set)
	if err != nil {
		return Revisions{}, err
	}
	frame := newFramedHash("career-hub/saved-assessment-context/v2")
	frame.field("retained_source_sha256", true, []byte(sourceSHA256))
	for _, spec := range cacheContextFiles {
		raw, present, err := set.observe(spec)
		if err != nil {
			return Revisions{}, err
		}
		role := "data:"
		if spec.resource {
			role = "resource:"
		}
		frame.field(role+spec.path, present, raw)
	}
	profile, err := set.yaml(sourceSpec{"career_vault/profile.yaml", "profile_location", false})
	if err != nil {
		return Revisions{}, err
	}
	for _, key := range []string{"target_location", "location_flexibility"} {
		value, present := get(profile, key)
		raw, err := canonicalJSON(ctx, value)
		if err != nil {
			return Revisions{}, err
		}
		frame.field("profile:"+key, present, raw)
	}
	frame.field("evaluation_month", true, []byte(fmt.Sprintf("%04d-%02d", month.Year, month.Number)))
	for _, v := range []struct {
		name  string
		value uint32
	}{{"evaluator", versions.Evaluator}, {"validation", versions.Validation}, {"workflow", versions.Workflow}} {
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], v.value)
		frame.field("version:"+v.name, true, raw[:])
	}
	if err := set.verify(); err != nil {
		return Revisions{}, err
	}
	return Revisions{Candidate: candidate, Context: frame.finish(SavedContextPrefix)}, nil
}
