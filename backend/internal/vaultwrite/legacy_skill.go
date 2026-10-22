package vaultwrite

import (
	"context"
	"slices"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/vaultread"
)

// Legacy Skill Groups are retained evidence blocks, not modern registry groups
// or atomic skill definitions. Their keywords never imply a catalog mutation.
var legacySkillFields = []string{"skill_family", "keywords", "related_experience", "related_projects", "related_certifications"}

func normalizeLegacySkill(payload map[string]any) (cataloginput.Value, *Error) {
	if err := checkRecordKeys(payload, legacySkillFields, "skill"); err != nil {
		return cataloginput.Value{}, err
	}
	record := cataloginput.Value{Kind: "mapping"}
	for _, key := range legacySkillFields {
		var value cataloginput.Value
		var err *Error
		if key == "skill_family" {
			value, err = cleanText(payload[key], key, false, 500)
		} else {
			value, err = cleanList(payload[key], key)
		}
		if err != nil {
			return cataloginput.Value{}, err
		}
		set(&record, key, value)
	}
	return record, nil
}
func legacySkillMissing(id *string) *Error {
	message := "Skill Group record does not exist"
	if id != nil {
		message += ": " + *id
	}
	return &Error{Stage: "identity", Code: "VAULT_EDITOR_RECORD_NOT_FOUND", SourceClass: "VaultRecordNotFound", SourceMessage: message}
}

// Unlike Experience/Project updates, the legacy normalizer owns no date fields.
// Preserve those and every other extension; archive changes only its flag.
func planLegacySkill(original, normalized cataloginput.Value, op, id string, archived bool) (cataloginput.Value, *Error) {
	if original.Kind != "sequence" {
		return cataloginput.Value{}, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	record := cataloginput.Value{Kind: "mapping"}
	index := -1
	if op == "create" {
		index = len(original.Items)
		set(&record, "id", textValue(id))
	} else if op == "update" || op == "archive" {
		for i, item := range original.Items {
			key, _ := get(item, "id")
			if item.Kind == "mapping" && key.Kind == "string" && key.Text == id {
				index, record = i, item
				break
			}
		}
		if index < 0 {
			return cataloginput.Value{}, legacySkillMissing(&id)
		}
		record.Entries = slices.Clone(record.Entries)
	} else {
		return cataloginput.Value{}, &Error{Stage: "configuration", Code: "VAULT_EDITOR_OPERATION_INVALID"}
	}
	if op == "archive" {
		if archived {
			set(&record, "archived", cataloginput.Value{Kind: "bool", Boolean: true})
		} else {
			remove(&record, "archived")
		}
	} else {
		for _, entry := range normalized.Entries {
			set(&record, entry.Key.Text, entry.Value)
		}
		set(&record, "id", textValue(id))
		if op == "create" {
			set(&record, "source", textValue("manual"))
		}
	}
	original.Items = slices.Clone(original.Items)
	if index == len(original.Items) {
		original.Items = append(original.Items, record)
	} else {
		original.Items[index] = record
	}
	return original, nil
}

func (s *Service) CreateLegacySkillGroup(ctx context.Context, payload map[string]any, expected string) (*Outcome, *Error) {
	return s.mutateLegacySkill(ctx, "create", "", payload, false, expected)
}
func (s *Service) UpdateLegacySkillGroup(ctx context.Context, id string, payload map[string]any, expected string) (*Outcome, *Error) {
	return s.mutateLegacySkill(ctx, "update", id, payload, false, expected)
}
func (s *Service) SetLegacySkillGroupArchived(ctx context.Context, id string, archived bool, expected string) (*Outcome, *Error) {
	return s.mutateLegacySkill(ctx, "archive", id, nil, archived, expected)
}
func (s *Service) mutateLegacySkill(ctx context.Context, op, id string, payload map[string]any, archived bool, expected string) (*Outcome, *Error) {
	if len(id) > maxPayloadBytes || !utf8.ValidString(id) {
		return &Outcome{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_UNSUPPORTED"}
	}
	if op != "create" && !validEvidenceID(id) {
		return &Outcome{}, legacySkillMissing(nil)
	}
	return s.runCanonical(ctx, func(root *runtimepaths.Root, home *datahome.Info, out *Outcome) (*Outcome, *Error) {
		target := vaultread.LegacySkillTarget
		path, _ := target.Path()
		before, present, pathErr := s.read(root, path, cataloginput.YAMLMaxInputBytes)
		if pathErr != nil || !present {
			return out, &Error{Stage: "revision", Code: "VAULT_EDITOR_TARGET_UNAVAILABLE"}
		}
		if revision(before) != expected {
			return out, conflict(target, false)
		}
		var normalized cataloginput.Value
		var problem *Error
		if op != "archive" {
			normalized, problem = normalizeLegacySkill(payload)
			if problem != nil {
				return out, problem
			}
		}
		reads := &readSet{s, root, map[string]observation{}}
		state, loadErr := vaultread.ReadEditorState(ctx, reads)
		if loadErr != nil {
			return out, vaultFailure("load", loadErr)
		}
		if loaded, _ := state.Revision(target); loaded != expected {
			return out, conflict(target, true)
		}
		original, _ := state.Record(target)
		if op == "create" {
			existing, loadErr := state.CreationIDs(ctx, target)
			if loadErr != nil {
				return out, vaultFailure("identity", loadErr)
			}
			name, _ := get(normalized, "skill_family")
			id, problem = allocateNamedID(name.Text, "manual_skill_group", existing)
			if problem != nil {
				return out, problem
			}
		}
		candidate, problem := planLegacySkill(original, normalized, op, id, archived)
		if problem != nil {
			return out, problem
		}
		after, problem := encode(ctx, candidate)
		if problem != nil {
			return out, problem
		}
		response, loadErr := state.ValidateRecordReplacement(ctx, target, after, id)
		if loadErr != nil {
			return out, vaultFailure("validation", loadErr)
		}
		return s.replaceCanonical(ctx, reads, home, target, before, after, response, out)
	})
}
