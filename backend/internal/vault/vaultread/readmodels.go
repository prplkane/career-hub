package vaultread

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

// ReadEligibility returns the source eligibility editor projection without
// applying the standalone Profile editor's additional contact mapping check.
func ReadEligibility(ctx context.Context, root string) (json.RawMessage, *Error) {
	state, failure := openVault(ctx, root)
	if failure != nil {
		return nil, failure
	}
	return eligibilityPayload(ctx, state)
}

func eligibilityPayload(ctx context.Context, state *vaultState) (json.RawMessage, *Error) {
	if failure := validateVault(ctx, state); failure != nil {
		return nil, failure
	}
	encoder := profileEncoder{ctx: ctx, source: "eligibility"}
	encoder.recordProjection(state, eligibilityIndex)
	return encoder.result()
}

// ReadEducation returns only revision, schools and certifications. Other root
// keys are not part of this source projection, but record fields are retained.
func ReadEducation(ctx context.Context, root string) (json.RawMessage, *Error) {
	state, failure := openVault(ctx, root)
	if failure != nil {
		return nil, failure
	}
	return educationPayload(ctx, state)
}

func educationRecords(value cataloginput.Value, key string) cataloginput.Value {
	records, present := lookup(value, key)
	if !present {
		return cataloginput.Value{Kind: "sequence"}
	}
	return records // Shared validation rejects a present non-list, including null.
}

func educationPayload(ctx context.Context, state *vaultState) (json.RawMessage, *Error) {
	if failure := validateVault(ctx, state); failure != nil {
		return nil, failure
	}
	encoder := profileEncoder{ctx: ctx, source: "education"}
	encoder.educationProjection(state)
	return encoder.result()
}

// ReadExperiences retains every source record, including archived evidence.
func ReadExperiences(ctx context.Context, root string) (json.RawMessage, *Error) {
	state, failure := openVault(ctx, root)
	if failure != nil {
		return nil, failure
	}
	return experiencesPayload(ctx, state)
}

func experiencesPayload(ctx context.Context, state *vaultState) (json.RawMessage, *Error) {
	if failure := validateVault(ctx, state); failure != nil {
		return nil, failure
	}
	encoder := profileEncoder{ctx: ctx, source: "experiences"}
	encoder.evidenceProjection(state, experienceIndex)
	return encoder.result()
}

// ReadProjects includes raw-valued options for all truthy experience IDs.
func ReadProjects(ctx context.Context, root string) (json.RawMessage, *Error) {
	state, failure := openVault(ctx, root)
	if failure != nil {
		return nil, failure
	}
	return projectsPayload(ctx, state)
}

func projectsPayload(ctx context.Context, state *vaultState) (json.RawMessage, *Error) {
	if failure := validateVault(ctx, state); failure != nil {
		return nil, failure
	}
	encoder := profileEncoder{ctx: ctx, source: "projects"}
	encoder.evidenceProjection(state, projectIndex)
	return encoder.result()
}

// ReadSkills preserves the legacy editor contract, not a separate new skills UI.
func ReadSkills(ctx context.Context, root string) (json.RawMessage, *Error) {
	state, failure := openVault(ctx, root)
	if failure != nil {
		return nil, failure
	}
	return skillsPayload(ctx, state)
}

func skillsPayload(ctx context.Context, state *vaultState) (json.RawMessage, *Error) {
	if failure := validateVault(ctx, state); failure != nil {
		return nil, failure
	}
	encoder := profileEncoder{ctx: ctx, source: "legacy_skills"}
	encoder.skillsProjection(state)
	return encoder.result()
}

// ReadSnapshot loads and validates once. Cross-file writer exclusion belongs to
// the caller; one load is not a transactional filesystem snapshot.
func ReadSnapshot(ctx context.Context, root string) (json.RawMessage, *Error) {
	state, failure := openVault(ctx, root)
	if failure != nil {
		return nil, failure
	}
	return snapshotPayload(ctx, state)
}

func snapshotPayload(ctx context.Context, state *vaultState) (json.RawMessage, *Error) {
	// Deliberately do not use validateProfileState: the source snapshot permits
	// contact:null, although the standalone Profile endpoint rejects it.
	if failure := validateVault(ctx, state); failure != nil {
		return nil, failure
	}
	encoder := profileEncoder{ctx: ctx, source: "snapshot"}
	encoder.add([]byte(`{"profile":`))
	encoder.recordProjection(state, profileIndex)
	encoder.add([]byte(`,"experiences":`))
	encoder.evidenceProjection(state, experienceIndex)
	encoder.add([]byte(`,"projects":`))
	encoder.evidenceProjection(state, projectIndex)
	encoder.add([]byte(`,"skills":`))
	encoder.skillsProjection(state)
	encoder.add([]byte(`,"education":`))
	encoder.educationProjection(state)
	encoder.add([]byte("}"))
	return encoder.result()
}

func (e *profileEncoder) result() (json.RawMessage, *Error) {
	if e.failure != nil {
		return nil, e.failure
	}
	return json.RawMessage(e.output), nil
}

func (e *profileEncoder) revision(state *vaultState, index int) {
	e.add([]byte(`{"revision":`))
	e.scalar(state.revisions[index])
}

func (e *profileEncoder) recordProjection(state *vaultState, index int) {
	e.revision(state, index)
	if version, err := SchemaVersion(state.documents[profileIndex]); err == nil && version == 2 {
		e.add([]byte(`,"vault_schema_version":2`))
	}
	e.add([]byte(`,"record":`))
	e.value(state.documents[index])
	e.add([]byte("}"))
}

func (e *profileEncoder) educationProjection(state *vaultState) {
	e.revision(state, educationIndex)
	e.add([]byte(`,"schools":`))
	e.value(educationRecords(state.documents[educationIndex], "schools"))
	e.add([]byte(`,"certifications":`))
	e.value(educationRecords(state.documents[educationIndex], "certifications"))
	e.add([]byte("}"))
}

func (e *profileEncoder) evidenceProjection(state *vaultState, index int) {
	e.revision(state, index)
	e.add([]byte(`,"records":`))
	e.value(state.documents[index])
	e.add([]byte(`,"categories":`))
	e.categories(state.documents[index])
	if index == projectIndex {
		e.add([]byte(`,"experience_options":`))
		e.experienceOptions(state.documents[experienceIndex])
	}
	e.add([]byte("}"))
}

func (e *profileEncoder) skillsProjection(state *vaultState) {
	e.revision(state, skillIndex)
	e.add([]byte(`,"records":`))
	e.value(state.documents[skillIndex])
	e.add([]byte(`,"experience_options":`))
	e.referenceOptions(state.documents[experienceIndex], "company", "role")
	e.add([]byte(`,"project_options":`))
	e.referenceOptions(state.documents[projectIndex], "name")
	e.add([]byte(`,"certification_options":`))
	e.referenceOptions(educationRecords(state.documents[educationIndex], "certifications"), "name", "credential")
	e.add([]byte("}"))
}

func (e *profileEncoder) display(value cataloginput.Value) string {
	if e.failure != nil {
		return ""
	}
	text, failure := displayText(e.ctx, e.source, value)
	if failure != nil {
		e.failure = failure
	}
	return text
}

func (e *profileEncoder) categories(records cataloginput.Value) {
	unique := map[string]string{}
	budget := MaxProfileBytes
	for _, item := range records.Items {
		if e.failure != nil {
			return
		}
		category := field(item, "category")
		if item.Kind != "mapping" || !truthy(category) {
			continue
		}
		text := textcompat.Strip(e.display(category))
		if text == "" {
			continue
		}
		if _, present := unique[text]; !present {
			folded := textcompat.Fold(text)
			if len(text)+len(folded) > budget {
				e.failure = &Error{Stage: "projection", Source: e.source, Code: "VAULT_PROJECTION_LIMIT"}
				return
			}
			budget -= len(text) + len(folded)
			unique[text] = folded
		}
	}
	values := make([]string, 0, len(unique))
	for text := range unique {
		values = append(values, text)
	}
	// Python's set makes equal-casefold ties depend on its randomized hash seed.
	// Resolve only those ties by original scalar text for reproducible UI output.
	sort.Slice(values, func(i, j int) bool {
		left, right := unique[values[i]], unique[values[j]]
		if left == right {
			return values[i] < values[j]
		}
		return left < right
	})
	e.add([]byte("["))
	for index, text := range values {
		if index != 0 {
			e.add([]byte(","))
		}
		e.scalar(text)
	}
	e.add([]byte("]"))
}

func (e *profileEncoder) archived(item cataloginput.Value) {
	archived := field(item, "archived")
	e.value(cataloginput.Value{Kind: "bool", Boolean: archived.Kind == "bool" && archived.Boolean})
}

func (e *profileEncoder) experienceOptions(records cataloginput.Value) {
	e.add([]byte("["))
	first := true
	for _, item := range records.Items {
		if e.failure != nil {
			return
		}
		id := field(item, "id")
		if item.Kind != "mapping" || !truthy(id) {
			continue
		}
		if !first {
			e.add([]byte(","))
		}
		first = false
		e.add([]byte(`{"id":`))
		e.value(id)
		e.add([]byte(`,"company":`))
		e.value(field(item, "company"))
		e.add([]byte(`,"role":`))
		e.value(field(item, "role"))
		e.add([]byte(`,"archived":`))
		e.archived(item)
		e.add([]byte("}"))
	}
	e.add([]byte("]"))
}

func (e *profileEncoder) referenceOptions(records cataloginput.Value, fields ...string) {
	e.add([]byte("["))
	first := true
	for _, item := range records.Items {
		if e.failure != nil {
			return
		}
		id := field(item, "id")
		if item.Kind != "mapping" || !truthy(id) {
			continue
		}
		var parts []string
		size := 0
		for _, key := range fields {
			value := field(item, key)
			if !truthy(value) {
				continue
			}
			text := textcompat.Strip(e.display(value))
			if e.failure != nil {
				return
			}
			if text != "" {
				size += len(text)
				if len(parts) != 0 {
					size += len(" / ")
				}
				if size > MaxProfileBytes {
					e.failure = &Error{Stage: "projection", Source: e.source, Code: "VAULT_PROJECTION_LIMIT"}
					return
				}
				parts = append(parts, text)
			}
		}
		label := strings.Join(parts, " / ")
		if label == "" {
			label = e.display(id) // Fallback is not stripped in the source.
		}
		if !first {
			e.add([]byte(","))
		}
		first = false
		e.add([]byte(`{"id":`))
		e.value(id)
		e.add([]byte(`,"label":`))
		e.scalar(label)
		e.add([]byte(`,"archived":`))
		e.archived(item)
		e.add([]byte("}"))
	}
	e.add([]byte("]"))
}
