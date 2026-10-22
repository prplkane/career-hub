package vaultread

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"careerhub.local/backend/internal/cataloginput"
)

// EditTarget is closed: this editor cannot select an arbitrary file. Evidence
// mutations require their own combined canonical/catalog publication boundary.
type EditTarget uint8

const (
	ProfileTarget EditTarget = iota + 1
	EligibilityTarget
	ExperienceTarget
	ProjectTarget
	SchoolTarget
	CertificationTarget
	LegacySkillTarget
)

func (target EditTarget) index() (int, bool) {
	switch target {
	case ProfileTarget:
		return profileIndex, true
	case EligibilityTarget:
		return eligibilityIndex, true
	case ExperienceTarget:
		return experienceIndex, true
	case ProjectTarget:
		return projectIndex, true
	case SchoolTarget, CertificationTarget:
		return educationIndex, true
	case LegacySkillTarget:
		return skillIndex, true
	}
	return 0, false
}

func (target EditTarget) Path() (string, bool) {
	index, ok := target.index()
	if !ok {
		return "", false
	}
	return schemas[index].path, true
}

// EducationCollection is a closed selector, not a caller-supplied YAML key.
func (target EditTarget) EducationCollection() (string, bool) {
	switch target {
	case SchoolTarget:
		return "schools", true
	case CertificationTarget:
		return "certifications", true
	}
	return "", false
}

// EditorState is a shape-checked, not yet domain-validated, immutable read set.
// The source writer may repair invalid fields in its target; validating the old
// target first would incorrectly prevent that repair. No method here writes.
type EditorState struct{ state *vaultState }

// ReadEditorState reads only the eight fixed Vault paths. The caller owns root
// admission, expected-byte observations and exclusion of concurrent writers.
func ReadEditorState(ctx context.Context, source reader) (*EditorState, *Error) {
	if source == nil {
		return nil, &Error{Stage: "configuration", Code: "VAULT_EDITOR_SOURCE_REQUIRED"}
	}
	state, failure := loadVault(ctx, source)
	if failure != nil {
		return nil, failure
	}
	return &EditorState{state}, nil
}

func (s *EditorState) Revision(target EditTarget) (string, bool) {
	index, ok := target.index()
	if !ok || s == nil || s.state == nil {
		return "", false
	}
	return s.state.revisions[index], true
}

// Record returns an owned value tree. Mutating it cannot mutate captured input.
func (s *EditorState) Record(target EditTarget) (cataloginput.Value, bool) {
	index, ok := target.index()
	if !ok || s == nil || s.state == nil {
		return cataloginput.Value{}, false
	}
	return cloneEditorValue(s.state.documents[index]), true
}

func cloneEditorValue(v cataloginput.Value) cataloginput.Value {
	out := v
	out.Bytes = append([]byte(nil), v.Bytes...)
	if v.Items != nil {
		out.Items = make([]cataloginput.Value, len(v.Items))
		for i, item := range v.Items {
			out.Items[i] = cloneEditorValue(item)
		}
	}
	if v.Entries != nil {
		out.Entries = make([]cataloginput.Entry, len(v.Entries))
		for i, entry := range v.Entries {
			out.Entries[i] = cataloginput.Entry{
				Key: cloneEditorValue(entry.Key), Value: cloneEditorValue(entry.Value),
			}
		}
	}
	return out
}

// ValidateReplacement reparses the exact proposed bytes, validates the complete
// candidate Vault, then preflights its browser response. Unsupported stored
// extensions (dates/binary/nonfinite numbers) are refused before publication,
// never dropped or silently converted. The original state is unchanged.
func (s *EditorState) ValidateReplacement(ctx context.Context, target EditTarget, raw []byte) (json.RawMessage, *Error) {
	if target != ProfileTarget && target != EligibilityTarget {
		return nil, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	next, index, e := s.candidate(ctx, target, raw)
	if e != nil {
		return nil, e
	}
	encoder := profileEncoder{ctx: ctx, source: schemas[index].source}
	encoder.recordProjection(next, index)
	return encoder.result()
}

func (s *EditorState) candidate(ctx context.Context, target EditTarget, raw []byte) (*vaultState, int, *Error) {
	index, ok := target.index()
	if !ok || s == nil || s.state == nil {
		return nil, 0, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, 0, canceled()
	}
	value, inputErr := cataloginput.ParseYAML(raw)
	if inputErr != nil {
		return nil, 0, &Error{Stage: "candidate", Source: schemas[index].source,
			Code: "VAULT_EDITOR_CANDIDATE_INVALID", Input: inputErr}
	}
	if value.Kind != schemas[index].kind {
		return nil, 0, &Error{Stage: "candidate", Source: schemas[index].source, Code: "VAULT_ROOT_TYPE"}
	}
	next := *s.state
	next.documents[index] = value
	next.revisions[index] = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	if failure := validateVault(ctx, &next); failure != nil {
		return nil, 0, failure
	}
	return &next, index, nil
}

// CreationIDs preserves the source writer's truthy-ID then str(ID) selection.
// Invalid old records are not silently repaired; full candidate validation
// follows allocation. This bounded projection never changes the captured tree.
func (s *EditorState) CreationIDs(ctx context.Context, target EditTarget) (map[string]bool, *Error) {
	index, ok := target.index()
	_, education := target.EducationCollection()
	if !ok || (target != ExperienceTarget && target != ProjectTarget && target != LegacySkillTarget && !education) || s == nil || s.state == nil {
		return nil, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, canceled()
	}
	records := s.state.documents[index].Items
	if education {
		records = nil
		for _, collection := range []string{"schools", "certifications"} {
			items := educationRecords(s.state.documents[index], collection)
			if items.Kind != "sequence" {
				return nil, &Error{Stage: "identity", Source: "education", Code: "VAULT_EDITOR_EDUCATION_LIST_REQUIRED"}
			}
			records = append(records, items.Items...)
		}
	}
	ids := map[string]bool{}
	for _, record := range records {
		value, present := lookup(record, "id")
		if !present || !truthy(value) {
			continue
		}
		text, e := displayText(ctx, schemas[index].source, value)
		if e != nil {
			return nil, e
		}
		ids[text] = true
	}
	return ids, nil
}

// ValidateRecordReplacement validates all eight prospective files but projects
// only the exact edited record. An unrelated record's unsupported extension
// cannot cause a false response failure. Registry response fields are composed
// separately by the owning evidence writer, before recording its transaction.
func (s *EditorState) ValidateRecordReplacement(ctx context.Context, target EditTarget, raw []byte, recordID string) (json.RawMessage, *Error) {
	collection, education := target.EducationCollection()
	if target != ExperienceTarget && target != ProjectTarget && target != LegacySkillTarget && !education {
		return nil, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	next, index, e := s.candidate(ctx, target, raw)
	if e != nil {
		return nil, e
	}
	records := next.documents[index]
	if education {
		records = educationRecords(records, collection)
	}
	for _, record := range records.Items {
		id, present := lookup(record, "id")
		if present && id.Kind == "string" && id.Text == recordID {
			encoder := profileEncoder{ctx: ctx, source: schemas[index].source}
			encoder.revision(next, index)
			encoder.add([]byte(`,"record":`))
			encoder.value(record)
			encoder.add([]byte("}"))
			return encoder.result()
		}
	}
	return nil, &Error{Stage: "candidate", Source: schemas[index].source, Code: "VAULT_EDITOR_RECORD_NOT_FOUND"}
}
