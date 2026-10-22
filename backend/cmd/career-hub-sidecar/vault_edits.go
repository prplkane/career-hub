package main

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/catalogtransaction"
	"careerhub.local/backend/internal/catalogwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/vaultwrite"
)

// Select only existing owning services. Evidence plus proposed vocabulary is
// one native transaction, never a registry save followed by a separate record.
func vaultEdits(start protocol.StartRecord) func(context.Context, httpapi.VaultEditRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	files := vaultwrite.New(start.DataRoot)
	evidence := vaultwrite.NewEvidence(start.ResourceRoot, start.DataRoot)
	registry := catalogwrite.New(start.ResourceRoot, start.DataRoot)
	return func(ctx context.Context, r httpapi.VaultEditRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		archived := r.Operation == "archive"
		if r.Kind == "experience" || r.Kind == "project" {
			var out *vaultwrite.EvidenceOutcome
			var e *vaultwrite.Error
			switch r.Kind + "." + r.Operation {
			case "experience.create":
				out, e = evidence.CreateExperience(ctx, r.Record, r.Revision)
			case "experience.update":
				out, e = evidence.UpdateExperience(ctx, r.ID, r.Record, r.Revision)
			case "experience.archive", "experience.restore":
				out, e = evidence.SetExperienceArchived(ctx, r.ID, archived, r.Revision)
			case "project.create":
				out, e = evidence.CreateProject(ctx, r.Record, r.Revision)
			case "project.update":
				out, e = evidence.UpdateProject(ctx, r.ID, r.Record, r.Revision)
			case "project.archive", "project.restore":
				out, e = evidence.SetProjectArchived(ctx, r.ID, archived, r.Revision)
			default:
				return invalidVaultEdit()
			}
			var result *httpapi.ApplicationOutcome
			if out != nil {
				result = &httpapi.ApplicationOutcome{Result: out.Result, Committed: out.Committed, CleanupFailed: out.CleanupFailed}
				applyWriteFacts(result, out.Write)
			}
			return result, vaultEditError(e)
		}
		if r.Kind == "registry_group" || r.Kind == "registry_skill" {
			switch r.Kind + "." + r.Operation {
			case "registry_group.create":
				return catalogEditResult(registry.CreateCustomGroup(ctx, r.Record, r.Revision))
			case "registry_group.update":
				return catalogRenameResult(registry.RenameCustomGroup(ctx, r.ID, r.Record, r.Revision))
			case "registry_group.archive", "registry_group.restore":
				out, e := registry.SetCustomGroupArchived(ctx, r.ID, archived, r.Revision)
				if out == nil {
					return nil, catalogEditError(e)
				}
				result := &httpapi.ApplicationOutcome{CleanupFailed: out.CleanupFailed}
				applyWriteFacts(result, out.Write)
				return catalogResponse(result, out.Result, e)
			case "registry_skill.create":
				return catalogEditResult(registry.CreateCustomSkill(ctx, r.Record, r.Revision))
			case "registry_skill.batch":
				return catalogEditResult(registry.CreateCustomSkills(ctx, r.Record, r.Revision))
			case "registry_skill.update":
				return catalogEditResult(registry.UpdateCustomSkill(ctx, r.ID, r.Record, r.Revision))
			case "registry_skill.archive", "registry_skill.restore":
				return catalogEditResult(registry.SetCustomSkillArchived(ctx, r.ID, archived, r.Revision))
			default:
				return invalidVaultEdit()
			}
		}
		var out *vaultwrite.Outcome
		var e *vaultwrite.Error
		switch r.Kind + "." + r.Operation {
		case "school.create":
			out, e = files.CreateSchool(ctx, r.Record, r.Revision)
		case "school.update":
			out, e = files.UpdateSchool(ctx, r.ID, r.Record, r.Revision)
		case "school.archive", "school.restore":
			out, e = files.SetSchoolArchived(ctx, r.ID, archived, r.Revision)
		case "certification.create":
			out, e = files.CreateCertification(ctx, r.Record, r.Revision)
		case "certification.update":
			out, e = files.UpdateCertification(ctx, r.ID, r.Record, r.Revision)
		case "certification.archive", "certification.restore":
			out, e = files.SetCertificationArchived(ctx, r.ID, archived, r.Revision)
		case "legacy_skill.create":
			out, e = files.CreateLegacySkillGroup(ctx, r.Record, r.Revision)
		case "legacy_skill.update":
			out, e = files.UpdateLegacySkillGroup(ctx, r.ID, r.Record, r.Revision)
		case "legacy_skill.archive", "legacy_skill.restore":
			out, e = files.SetLegacySkillGroupArchived(ctx, r.ID, archived, r.Revision)
		default:
			return invalidVaultEdit()
		}
		var result *httpapi.ApplicationOutcome
		if out != nil {
			result = &httpapi.ApplicationOutcome{Result: out.Result, Committed: out.Committed, CleanupFailed: out.CleanupFailed}
		}
		return result, vaultEditError(e)
	}
}

func invalidVaultEdit() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "VAULT_RECORD_NOT_FOUND"}
}

func applyWriteFacts(result *httpapi.ApplicationOutcome, report catalogtransaction.Report) {
	result.Committed = result.Committed || len(report.AppliedFiles) > 0
	result.Partial = len(report.AppliedFiles) > 0 && !report.FilesComplete
	result.Write = &httpapi.ApplicationWriteState{IntentRecorded: report.IntentRecorded, AppliedFiles: len(report.AppliedFiles), FilesComplete: report.FilesComplete, JournalRemoved: report.JournalRemoved, Completed: report.Completed}
}

func vaultEditError(e *vaultwrite.Error) *httpapi.ApplicationError {
	if e == nil {
		return nil
	}
	code := "VAULT_OPERATION_FAILED"
	switch e.SourceClass {
	case "VaultEditorRevisionConflict":
		code = "VAULT_EDITOR_REVISION_CONFLICT"
	case "VaultRecordNotFound":
		code = "VAULT_RECORD_NOT_FOUND"
	case "VaultEditorError", "SkillRegistryError", "SkillRegistryRevisionConflict":
		code = "VAULT_RECORD_VALIDATION_FAILED"
	}
	if e.Stage == "validation" || e.Stage == "load" {
		code = "VAULT_RECORD_VALIDATION_FAILED"
	}
	if e.Stage == "context" {
		code = "LOCAL_OPERATION_CANCELED"
	}
	return &httpapi.ApplicationError{Code: code}
}

func catalogEditError(e *catalogwrite.Error) *httpapi.ApplicationError {
	if e == nil {
		return nil
	}
	code := "SKILL_REGISTRY_OPERATION_FAILED"
	switch e.Code {
	case "SKILL_REGISTRY_REVISION_CONFLICT", "CORE_GROUP_READ_ONLY", "CORE_SKILL_READ_ONLY", "SKILL_GROUP_NOT_EMPTY", "SKILL_GROUP_NOT_FOUND", "SKILL_NOT_FOUND", "SKILL_GROUP_ARCHIVED", "SKILL_NAME_CONFLICT", "SKILL_REGISTRY_INVALID", "CATALOG_MODEL_INVALID":
		code = e.Code
	case "CATALOG_OPERATION_CANCELLED":
		code = "LOCAL_OPERATION_CANCELED"
	default:
		if e.Stage == "input" || e.Stage == "validation" || e.Stage == "model" {
			code = "SKILL_REGISTRY_INVALID"
		}
	}
	return &httpapi.ApplicationError{Code: code}
}

func catalogResponse(out *httpapi.ApplicationOutcome, value any, e *catalogwrite.Error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if e != nil {
		out.Committed = out.Committed || e.Committed
		return out, catalogEditError(e)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return out, &httpapi.ApplicationError{Code: "LOCAL_RESPONSE_INVALID"}
	}
	out.Result = raw
	return out, nil
}

func catalogRenameResult(value *catalogwrite.Result, e *catalogwrite.Error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	// A successful rename has already replaced and read back the private catalog.
	// Preserve that fact if a later response/admission check fails.
	out := &httpapi.ApplicationOutcome{Committed: value != nil && e == nil}
	if e != nil {
		out.Committed = e.Committed
		out.CleanupFailed = e.Stage == "close"
	}
	return catalogResponse(out, value, e)
}

func catalogEditResult[T any](out *catalogwrite.EditOutcome[T], e *catalogwrite.Error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if out == nil {
		return nil, catalogEditError(e)
	}
	result := &httpapi.ApplicationOutcome{CleanupFailed: out.CleanupFailed}
	applyWriteFacts(result, out.Write)
	return catalogResponse(result, out.Result, e)
}
