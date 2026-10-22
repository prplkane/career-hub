package vaultwrite

import (
	"context"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/vaultread"
)

func (s *Service) CreateSchool(ctx context.Context, payload map[string]any, expected string) (*Outcome, *Error) {
	return s.mutateEducation(ctx, vaultread.SchoolTarget, "create", "", payload, false, expected)
}
func (s *Service) UpdateSchool(ctx context.Context, id string, payload map[string]any, expected string) (*Outcome, *Error) {
	return s.mutateEducation(ctx, vaultread.SchoolTarget, "update", id, payload, false, expected)
}
func (s *Service) SetSchoolArchived(ctx context.Context, id string, archived bool, expected string) (*Outcome, *Error) {
	return s.mutateEducation(ctx, vaultread.SchoolTarget, "archive", id, nil, archived, expected)
}
func (s *Service) CreateCertification(ctx context.Context, payload map[string]any, expected string) (*Outcome, *Error) {
	return s.mutateEducation(ctx, vaultread.CertificationTarget, "create", "", payload, false, expected)
}
func (s *Service) UpdateCertification(ctx context.Context, id string, payload map[string]any, expected string) (*Outcome, *Error) {
	return s.mutateEducation(ctx, vaultread.CertificationTarget, "update", id, payload, false, expected)
}
func (s *Service) SetCertificationArchived(ctx context.Context, id string, archived bool, expected string) (*Outcome, *Error) {
	return s.mutateEducation(ctx, vaultread.CertificationTarget, "archive", id, nil, archived, expected)
}

func (s *Service) mutateEducation(ctx context.Context, target vaultread.EditTarget, op, id string,
	payload map[string]any, archived bool, expected string) (*Outcome, *Error) {
	if len(id) > maxPayloadBytes || !utf8.ValidString(id) {
		return &Outcome{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_UNSUPPORTED"}
	}
	if op != "create" && !validEvidenceID(id) {
		return &Outcome{}, educationMissing(nil)
	}
	return s.runCanonical(ctx, func(root *runtimepaths.Root, home *datahome.Info, out *Outcome) (*Outcome, *Error) {
		kind, ok := educationKind(target)
		if !ok {
			return out, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
		}
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
			normalized, problem = normalizeEducation(target, payload)
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
		collection, _ := target.EducationCollection()
		if _, problem := educationItems(original, collection); problem != nil {
			return out, problem
		}
		if op == "create" {
			// Source checks the selected collection first, then both collections
			// in schema order before allocating in their shared ID namespace.
			for _, key := range []string{"schools", "certifications"} {
				if _, problem := educationItems(original, key); problem != nil {
					return out, problem
				}
			}
			existing, loadErr := state.CreationIDs(ctx, target)
			if loadErr != nil {
				return out, vaultFailure("identity", loadErr)
			}
			nameKey := "name"
			if target == vaultread.SchoolTarget {
				nameKey = "institution"
			}
			name, _ := get(normalized, nameKey)
			id, problem = allocateNamedID(name.Text, "manual_"+kind, existing)
			if problem != nil {
				return out, problem
			}
		}
		candidate, problem := planEducationRecord(target, original, normalized, op, id, archived)
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
