package vaultwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/catalogwrite"
	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/intakereview"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vaultread"
)

type IntakeAppliedChange struct {
	SubjectKind      string            `json:"subject_kind"`
	SubjectID        string            `json:"subject_id"`
	Action           string            `json:"action"`
	EntityType       *string           `json:"entity_type"`
	TargetID         *string           `json:"target_id"`
	VaultPath        *string           `json:"vault_path"`
	FieldResolutions map[string]string `json:"field_resolutions"`
}
type IntakeApplyResult struct {
	SchemaVersion            int                   `json:"schema_version"`
	Status                   string                `json:"status"`
	SourceID                 string                `json:"source_id"`
	AppliedAt                string                `json:"applied_at"`
	ReviewRevision           string                `json:"review_revision"`
	PreviousVaultRevision    string                `json:"previous_vault_revision"`
	VaultRevision            string                `json:"vault_revision"`
	PreviousRegistryRevision string                `json:"previous_registry_revision"`
	RegistryRevision         string                `json:"registry_revision"`
	CreatedSkillIDs          []string              `json:"created_skill_ids"`
	Changes                  []IntakeAppliedChange `json:"changes"`
	ChangedFiles             []string              `json:"changed_files"`
}

// IntakeApprovalPlan is a pure proposal, never evidence that files were saved.
// The native owner must replan under admission/writer exclusion and publish the
// closed write set plus approval result as one recoverable operation.
type IntakeApprovalPlan struct {
	replacements map[string][]byte
	result       []byte
}

func (p *IntakeApprovalPlan) Replacements() map[string][]byte {
	out := map[string][]byte{}
	for path, raw := range p.replacements {
		out[path] = bytes.Clone(raw)
	}
	return out
}
func (p *IntakeApprovalPlan) ResultBytes() []byte { return bytes.Clone(p.result) }

func PlanIntakeApproval(ctx context.Context, extractionRaw, reviewRaw []byte, vaultRaw map[string][]byte, state *catalogstore.State, expectedReview, expectedVault, expectedRegistry, stamp string) (*IntakeApprovalPlan, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, intakeFailure("APPROVAL_CANCELED")
	}
	if state == nil || state.Snapshot == nil {
		return nil, intakeFailure("APPROVAL_CATALOG_INVALID")
	}
	parsedStamp, stampErr := time.Parse(time.RFC3339Nano, stamp)
	if stampErr != nil || parsedStamp.Year() < 1 || parsedStamp.Year() > 9999 {
		return nil, intakeFailure("APPROVAL_TIMESTAMP_INVALID")
	}
	extraction, extractionErr := intakeread.ParseExtraction(extractionRaw)
	if extractionErr != nil {
		return nil, intakeFailure("APPROVAL_EXTRACTION_INVALID")
	}
	normalized, normalErr := intakereview.Normalize(reviewRaw)
	if normalErr != nil {
		return nil, intakeFailure("APPROVAL_REVIEW_INVALID")
	}
	review, reviewErr := intakeread.ParseReview(normalized)
	if reviewErr != nil || review.Source != extraction.Source {
		return nil, intakeFailure("APPROVAL_REVIEW_INVALID")
	}
	review, _ = intakeread.BackfillReviewCategories(extraction, review)
	if !intakeread.BuildReviewCompletion(review, intakeread.ValidateReview(extraction, review)).ReadyToApprove {
		return nil, intakeFailure("APPROVAL_NOT_READY")
	}
	names := map[string]string{}
	for _, skill := range state.Snapshot.Skills() {
		names[skill.ID] = skill.Name
	}
	preview, previewErr := intakereview.BuildApprovalPreview(ctx, extractionRaw, normalized, vaultRaw, state.Snapshot.Revision(), names)
	if previewErr != nil {
		return nil, intakeFailure(previewErr.Code)
	}
	if expectedReview != preview.ReviewRevision {
		return nil, intakeFailure("REVIEW_REVISION_CONFLICT")
	}
	if expectedVault != preview.VaultRevision {
		return nil, intakeFailure("VAULT_REVISION_CONFLICT")
	}
	if expectedRegistry != preview.RegistryRevision {
		return nil, intakeFailure("SKILL_REGISTRY_REVISION_CONFLICT")
	}
	var full struct {
		Entities []intakeEntity `json:"entities"`
	}
	if json.Unmarshal(normalized, &full) != nil {
		return nil, intakeFailure("APPROVAL_REVIEW_INVALID")
	}
	entities := map[string]intakeEntity{}
	for _, e := range full.Entities {
		if e.State == "active" {
			entities[e.ID] = e
		}
	}
	previews := map[string]intakereview.ApprovalEntity{}
	for _, item := range preview.Entities {
		previews[item.SourceEntityID] = item
	}
	decisions := map[string]intakeread.ReviewDecision{}
	for _, d := range review.ApprovalDecisions {
		if d.SubjectKind != "entity" {
			continue
		}
		if _, exists := entities[d.SubjectID]; !exists {
			return nil, intakeFailure("INVALID_APPROVAL_SUBJECT")
		}
		if d.Action == "merge" {
			allowed := false
			for _, c := range previews[d.SubjectID].Candidates {
				if d.TargetID != nil && c.TargetID == *d.TargetID {
					allowed = true
				}
			}
			if !allowed {
				return nil, intakeFailure("INVALID_MERGE_TARGET")
			}
		}
		decisions[d.SubjectID] = d
	}
	documents := map[string]cataloginput.Value{}
	for _, path := range intakereview.ApprovalPreviewPaths() {
		value, err := cataloginput.ParseYAML(vaultRaw[path])
		if err != nil {
			return nil, intakeFailure("APPROVAL_VAULT_INVALID")
		}
		documents[path] = value
	}
	if err := vaultread.ValidateIntakeVault(ctx, documents); err != nil {
		return nil, &Error{Stage: "intake_approval", Code: "VAULT_VALIDATION_FAILED", Vault: err}
	}
	// Validate before adding pending skills: only explicitly selected technology
	// fields on supported evidence cards participate in the shared registry plan.
	references := []catalogwrite.SkillReferenceDraft{}
	via := "resume_digest"
	skillStamp := parsedStamp.UTC().Format("2006-01-02T15:04:05Z")
	for _, e := range full.Entities {
		if e.State != "active" {
			continue
		}
		d, ok := decisions[e.ID]
		if !ok || (d.Action != "create" && d.Action != "merge") || (e.Type != "experience" && e.Type != "project" && e.Type != "certification") {
			continue
		}
		if d.FieldResolutions != nil && d.FieldResolutions["technologies"] != "source" && d.FieldResolutions["technologies"] != "add_source" {
			continue
		}
		draft := catalogwrite.SkillReferenceDraft{EntityID: e.ID, CompetencyIDs: e.Technologies, CreatedVia: &via, CreatedAt: &skillStamp}
		for _, pending := range e.Pending {
			draft.PendingSkills = append(draft.PendingSkills, catalogwrite.PendingSkill{Name: pending.Name, GroupID: pending.GroupID})
		}
		references = append(references, draft)
	}
	skillPlan, skillErr := catalogwrite.PlanSkillReferences(ctx, state, references, expectedRegistry)
	if skillErr != nil {
		return nil, &Error{Stage: "intake_approval", Code: skillErr.Code, Catalog: skillErr}
	}
	skills := skillPlan.Result()
	known := map[string]bool{}
	for id := range names {
		known[id] = true
	}
	for _, skill := range skills.CreatedSkills {
		known[skill.ID] = true
	}
	if err := intakeValidateSkills(documents, known); err != nil {
		return nil, err
	}
	before := map[string][]byte{}
	for path, value := range documents {
		raw, err := encode(ctx, value)
		if err != nil {
			return nil, err
		}
		before[path] = raw
	}
	result := IntakeApplyResult{SchemaVersion: 1, Status: "applied", SourceID: review.Source.ID, AppliedAt: stamp, ReviewRevision: preview.ReviewRevision, PreviousVaultRevision: preview.VaultRevision, PreviousRegistryRevision: skills.PreviousRevision, RegistryRevision: skills.Revision, CreatedSkillIDs: []string{}, Changes: []IntakeAppliedChange{}, ChangedFiles: []string{}}
	for _, skill := range skills.CreatedSkills {
		result.CreatedSkillIDs = append(result.CreatedSkillIDs, skill.ID)
	}
	for _, d := range review.ApprovalDecisions {
		if ctx.Err() != nil {
			return nil, intakeFailure("APPROVAL_CANCELED")
		}
		change := IntakeAppliedChange{SubjectKind: d.SubjectKind, SubjectID: d.SubjectID, Action: d.Action}
		if d.SubjectKind == "block" {
			result.Changes = append(result.Changes, change)
			continue
		}
		e := entities[d.SubjectID]
		change.EntityType = &e.Type
		change.TargetID = d.TargetID
		change.FieldResolutions = d.FieldResolutions
		if d.Action == "create" || d.Action == "merge" {
			ids := skills.SkillIDsByEntity[e.ID]
			skillNames := skills.SkillNamesByEntity[e.ID]
			if len(skillNames) == 0 {
				for _, id := range e.Technologies {
					name, ok := names[id]
					if !ok {
						name = textcompat.Title(strings.ReplaceAll(id, "_", " "))
					}
					skillNames = append(skillNames, name)
				}
			}
			skillNames = intakeUnique(skillNames)
			blocks := []string{}
			owned := map[string]bool{}
			for _, block := range review.BlockReviews {
				if block.OwnerID != nil && *block.OwnerID == e.ID {
					blocks = append(blocks, block.BlockID)
					owned[block.BlockID] = true
				}
			}
			proof := append([]string{}, e.Items...)
			for _, block := range extraction.Blocks {
				if owned[block.ID] && block.IsBullet && textcompat.Strip(block.Text) != "" {
					proof = append(proof, textcompat.Strip(block.Text))
				}
			}
			proof = intakeUnique(proof)
			values := intakeSourceValues(e, proof, skillNames)
			note := intakeNote(review.Source.ID, blocks)
			path, collection, err := intakeDestination(e.Type)
			if err != nil {
				return nil, err
			}
			document := documents[path]
			if e.Type == "profile" {
				if d.Action == "create" {
					return nil, intakeFailure("PROFILE_CREATE_NOT_ALLOWED")
				}
				if d.TargetID == nil || *d.TargetID != "profile" {
					return nil, intakeFailure("INVALID_MERGE_TARGET")
				}
				if err := intakeMergeProfile(e, d, values, &document); err != nil {
					return nil, err
				}
			} else {
				records := document
				if collection != "" {
					var present bool
					records, present = get(document, collection)
					if !present {
						records = cataloginput.Value{Kind: "sequence"}
					}
				}
				if records.Kind != "sequence" {
					return nil, intakeFailure("INVALID_VAULT_SHAPE")
				}
				if d.Action == "create" {
					id, err := intakeID(e, records, review.Source.ID)
					if err != nil {
						return nil, err
					}
					record, err := intakeCreate(e, d, values, id, note, ids)
					if err != nil {
						return nil, err
					}
					records.Items = append(records.Items, record)
					change.TargetID = &id
				} else {
					index := -1
					for i, record := range records.Items {
						id := intakeField(record, "id")
						if id.Kind == "string" && d.TargetID != nil && id.Text == *d.TargetID {
							index = i
							break
						}
					}
					if index < 0 {
						return nil, intakeFailure("INVALID_MERGE_TARGET")
					}
					if err := intakeMerge(e, d, values, &records.Items[index], note, ids); err != nil {
						return nil, err
					}
				}
				if collection != "" {
					set(&document, collection, records)
				} else {
					document = records
				}
			}
			documents[path] = document
			change.VaultPath = &path
		}
		result.Changes = append(result.Changes, change)
	}
	if err := vaultread.ValidateIntakeVault(ctx, documents); err != nil {
		return nil, &Error{Stage: "intake_approval", Code: "VAULT_VALIDATION_FAILED", Vault: err}
	}
	if err := intakeValidateSkills(documents, known); err != nil {
		return nil, err
	}
	plan := &IntakeApprovalPlan{replacements: map[string][]byte{}}
	for path, value := range documents {
		raw, err := encode(ctx, value)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(raw, before[path]) {
			plan.replacements[path] = raw
			result.ChangedFiles = append(result.ChangedFiles, path)
		}
	}
	sort.Strings(result.ChangedFiles)
	catalogReplacements := skillPlan.CatalogReplacements()
	for _, path := range []string{catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath} {
		if raw, changed := catalogReplacements[path]; changed {
			plan.replacements[path] = raw
			result.ChangedFiles = append(result.ChangedFiles, path)
		}
	}
	hash := sha256.New()
	for _, path := range intakereview.ApprovalPreviewPaths() {
		raw := vaultRaw[path]
		if replacement, ok := plan.replacements[path]; ok {
			raw = replacement
		}
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write(raw)
		hash.Write([]byte{0})
	}
	result.VaultRevision = fmt.Sprintf("sha256:%x", hash.Sum(nil))
	resultRaw, err := json.Marshal(result)
	if err != nil || len(resultRaw) > intakeread.MaxApprovalBytes {
		return nil, intakeFailure("APPROVAL_RESULT_LIMIT")
	}
	if _, err := intakeread.ParseApproval(resultRaw); err != nil {
		return nil, intakeFailure("APPROVAL_RESULT_INVALID")
	}
	plan.result = resultRaw
	if ctx.Err() != nil {
		return nil, intakeFailure("APPROVAL_CANCELED")
	}
	return plan, nil
}

func intakeDestination(typ string) (string, string, *Error) {
	switch typ {
	case "profile":
		return "career_vault/profile.yaml", "", nil
	case "experience":
		return "career_vault/experience_blocks.yaml", "", nil
	case "project":
		return "career_vault/project_blocks.yaml", "", nil
	case "skill":
		return "career_vault/skill_blocks.yaml", "", nil
	case "education":
		return "career_vault/education.yaml", "schools", nil
	case "certification":
		return "career_vault/education.yaml", "certifications", nil
	}
	return "", "", intakeFailure("UNSUPPORTED_APPROVAL_TYPE")
}
func intakeValidateSkills(documents map[string]cataloginput.Value, known map[string]bool) *Error {
	for _, typ := range []string{"experience", "project", "certification"} {
		path, collection, _ := intakeDestination(typ)
		records := documents[path]
		if collection != "" {
			records = intakeField(records, collection)
		}
		for _, record := range records.Items {
			ids, present := get(record, "competency_ids")
			if !present {
				continue
			}
			if ids.Kind != "sequence" {
				return intakeFailure("INVALID_COMPETENCY_REFERENCE")
			}
			for _, id := range ids.Items {
				if id.Kind != "string" || !known[id.Text] {
					return intakeFailure("INVALID_COMPETENCY_REFERENCE")
				}
			}
		}
	}
	return nil
}
