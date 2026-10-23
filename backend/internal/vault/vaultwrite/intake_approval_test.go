package vaultwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
)

const intakeStamp = "2026-09-21T12:34:56+00:00"

func intakeApprovalFixture(t *testing.T, typ, action string, resolutions map[string]string) ([]byte, []byte, map[string][]byte, evidenceCatalogMemory, *catalogstore.State) {
	t.Helper()
	files := evidenceCatalogMemory{
		catalogstore.CoreGroupsPath:           []byte("- {id: tools, name: Tools}\n"),
		catalogstore.CoreSkillsPath:           []byte("- {id: core, name: Core, category: tool, primary_group_id: tools}\n"),
		"career_vault/profile.yaml":           []byte("name: Existing Person\ntarget_location: Anywhere\nwork_authorization: Fictional\nmajor: Computing\ncontact: {email: original@example.invalid}\n"),
		"career_vault/education.yaml":         []byte("schools:\n- {id: school_one, institution: Example University, degree: BS, major: Computing, graduation_date: '2023', honors: [Existing honors]}\ncertifications:\n- {id: cert_one, name: Shared Name, credential: Certification, completed: '2023', proof_points: [Existing certification]}\n"),
		"career_vault/experience_blocks.yaml": []byte("- id: experience_one\n  company: Example Systems\n  role: Another Role\n  dates: 2020-2021\n  category: Work Experience\n  technologies: [Existing]\n  proof_points: [Existing evidence]\n  competency_ids: [core]\n  custom_extension: {keep: true, count: 2}\n"),
		"career_vault/project_blocks.yaml":    []byte("- {id: project_one, name: Local Search Engineer, category: Project, summary: Existing summary, technologies: [Existing], proof_points: [Existing project], competency_ids: [core]}\n"),
		"career_vault/skill_blocks.yaml":      []byte("- {id: skill_one, skill_family: Shared Name, related_experience: [experience_one], keywords: [Existing]}\n"),
	}
	state, err := catalogstore.ReadState(files, files)
	if err != nil {
		t.Fatal(err)
	}
	vault := map[string][]byte{}
	for _, path := range intakereview.ApprovalPreviewPaths() {
		vault[path] = bytes.Clone(files[path])
	}
	source := map[string]any{"id": "src_approval_1234567890", "path": "career_vault/raw/fictional.txt", "file_type": "txt", "sha256": strings.Repeat("a", 64)}
	extraction, _ := json.Marshal(map[string]any{"schema_version": 2, "status": "extracted", "extractor_version": "vault-intake-0.3", "registry_revision": state.Snapshot.Revision(), "source": source, "blocks": []any{map[string]any{"id": "b1", "order": 0, "kind": "paragraph", "text": "Additional source evidence", "is_bullet": true}}, "section_hints": []any{}, "technology_ids": []any{}, "technology_mentions": []any{}, "date_candidates": []any{}, "bullet_candidates": []any{}, "unresolved_fields": []any{}})
	entity := map[string]any{"id": "entity_one", "entity_type": typ, "name": "Shared Name", "organization": "Example Systems", "title": "Local Search Engineer", "institution": "Example University", "degree": "BS", "credential": "Certification", "major": "Computing", "gpa": "3.9", "honors": []string{"Source honors"}, "date_text": "2024", "location": "New Town", "emails": []string{"new@example.invalid"}, "phones": []string{"+1 555 0100"}, "links": []string{"https://github.com/fictional"}, "technologies": []string{"core"}, "pending_skills": []any{map[string]any{"name": "Fictional Pending Tool"}}, "items": []string{"Source evidence"}}
	entity["links"] = []string{"https://github.com/fictional", "https://linkedin.com/in/fictional", "https://example.invalid"}
	decision := map[string]any{"subject_kind": "entity", "subject_id": "entity_one", "action": action, "field_resolutions": resolutions}
	if action == "merge" {
		targets := map[string]string{"profile": "profile", "experience": "experience_one", "education": "school_one", "certification": "cert_one", "project": "project_one", "skill": "skill_one"}
		decision["target_id"] = targets[typ]
	}
	review, _ := json.Marshal(map[string]any{"extraction_schema_version": 2, "source": source, "entities": []any{entity}, "block_reviews": []any{map[string]any{"source_ref": map[string]any{"source_id": source["id"], "block_id": "b1"}, "category": typ, "disposition": "assigned", "owner_id": "entity_one"}}, "approval_decisions": []any{decision}})
	return extraction, review, vault, files, state
}

func intakeFixturePreview(t *testing.T, extraction, review []byte, vault map[string][]byte, state *catalogstore.State) *intakereview.ApprovalPlan {
	t.Helper()
	names := map[string]string{}
	for _, s := range state.Snapshot.Skills() {
		names[s.ID] = s.Name
	}
	preview, err := intakereview.BuildApprovalPreview(context.Background(), extraction, review, vault, state.Snapshot.Revision(), names)
	if err != nil {
		t.Fatal(err.Code)
	}
	return preview
}

func TestIntakeApprovalPlanCreatesCoherentOwnedProposal(t *testing.T) {
	extraction, review, vault, _, state := intakeApprovalFixture(t, "experience", "create", nil)
	preview := intakeFixturePreview(t, extraction, review, vault, state)
	before, _ := json.Marshal(vault)
	plan, err := PlanIntakeApproval(context.Background(), extraction, review, vault, state, preview.ReviewRevision, preview.VaultRevision, preview.RegistryRevision, intakeStamp)
	if err != nil {
		t.Fatalf("plan failed: %s %+v", err.Code, err.Vault)
	}
	var result IntakeApplyResult
	if json.Unmarshal(plan.ResultBytes(), &result) != nil {
		t.Fatal("invalid result")
	}
	if len(result.ChangedFiles) != 3 || len(result.CreatedSkillIDs) != 1 || len(plan.Replacements()) != 3 || result.Changes[0].TargetID == nil || *result.Changes[0].TargetID != "example_systems_local_search_engineer" {
		t.Fatal("incomplete proposal", result)
	}
	if _, err := intakeread.ParseApproval(plan.ResultBytes()); err != nil {
		t.Fatal(err.Code)
	}
	replacements := plan.Replacements()
	replacements["career_vault/experience_blocks.yaml"][0] = '!'
	raw := plan.ResultBytes()
	raw[0] = '!'
	if bytes.Equal(raw, plan.ResultBytes()) || bytes.Equal(replacements["career_vault/experience_blocks.yaml"], plan.Replacements()["career_vault/experience_blocks.yaml"]) {
		t.Fatal("proposal aliases accessor")
	}
	after, _ := json.Marshal(vault)
	if !bytes.Equal(before, after) || len(state.CustomSkills) != 0 || len(state.CustomGroups) != 0 {
		t.Fatal("planner mutated captured state")
	}
}

func TestIntakeApprovalPlanRefusesConflictsAndUnselectedSkills(t *testing.T) {
	for _, field := range []string{"review", "vault", "registry"} {
		t.Run(field, func(t *testing.T) {
			extraction, review, vault, _, state := intakeApprovalFixture(t, "project", "merge", map[string]string{"summary": "source"})
			p := intakeFixturePreview(t, extraction, review, vault, state)
			switch field {
			case "review":
				p.ReviewRevision = "stale"
			case "vault":
				p.VaultRevision = "stale"
			case "registry":
				p.RegistryRevision = "stale"
			}
			plan, err := PlanIntakeApproval(context.Background(), extraction, review, vault, state, p.ReviewRevision, p.VaultRevision, p.RegistryRevision, intakeStamp)
			if plan != nil || err == nil || !strings.HasSuffix(err.Code, "REVISION_CONFLICT") {
				t.Fatal("stale approval accepted", err)
			}
		})
	}
	extraction, review, vault, _, state := intakeApprovalFixture(t, "project", "merge", map[string]string{"summary": "source"})
	p := intakeFixturePreview(t, extraction, review, vault, state)
	plan, err := PlanIntakeApproval(context.Background(), extraction, review, vault, state, p.ReviewRevision, p.VaultRevision, p.RegistryRevision, intakeStamp)
	if err != nil {
		t.Fatal(err.Code)
	}
	var result IntakeApplyResult
	json.Unmarshal(plan.ResultBytes(), &result)
	if len(result.CreatedSkillIDs) != 0 || len(result.ChangedFiles) != 1 || result.RegistryRevision != result.PreviousRegistryRevision {
		t.Fatal("unselected skills promoted", result)
	}
	if !bytes.Contains(plan.Replacements()["career_vault/project_blocks.yaml"], []byte("Existing")) {
		t.Fatal("unselected fields lost")
	}
	for _, action := range []string{"source_only", "exclude"} {
		extraction, review, vault, _, state := intakeApprovalFixture(t, "experience", action, nil)
		p := intakeFixturePreview(t, extraction, review, vault, state)
		plan, err := PlanIntakeApproval(context.Background(), extraction, review, vault, state, p.ReviewRevision, p.VaultRevision, p.RegistryRevision, intakeStamp)
		if err != nil || len(plan.Replacements()) != 0 {
			t.Fatal("source-only/excluded card writes data", err)
		}
	}
}

func TestIntakeApprovalMultipleEntitiesShareSkillsWithoutLosingProvenance(t *testing.T) {
	var extractionDoc, reviewDoc map[string]any
	var extraction, review []byte
	var vault map[string][]byte
	var state *catalogstore.State
	entities, decisions, blocks, blockReviews := []any{}, []any{}, []any{}, []any{}
	for _, typ := range []string{"profile", "experience", "experience_copy", "education", "certification", "project", "skill"} {
		actualType := strings.TrimSuffix(typ, "_copy")
		action := "create"
		if typ == "profile" {
			action = "merge"
		}
		extraction, review, vault, _, state = intakeApprovalFixture(t, actualType, action, nil)
		extraction = bytes.ReplaceAll(extraction, []byte(`"b1"`), []byte(`"b_`+typ+`"`))
		review = bytes.ReplaceAll(review, []byte(`"b1"`), []byte(`"b_`+typ+`"`))
		review = bytes.ReplaceAll(review, []byte(`"entity_one"`), []byte(`"entity_`+typ+`"`))
		json.Unmarshal(extraction, &extractionDoc)
		json.Unmarshal(review, &reviewDoc)
		entities = append(entities, reviewDoc["entities"].([]any)...)
		decisions = append(decisions, reviewDoc["approval_decisions"].([]any)...)
		blocks = append(blocks, extractionDoc["blocks"].([]any)...)
		blocks[len(blocks)-1].(map[string]any)["order"] = len(blocks) - 1
		blockReviews = append(blockReviews, reviewDoc["block_reviews"].([]any)...)
	}
	extractionDoc["blocks"] = blocks
	reviewDoc["entities"], reviewDoc["approval_decisions"], reviewDoc["block_reviews"] = entities, decisions, blockReviews
	extraction, _ = json.Marshal(extractionDoc)
	review, _ = json.Marshal(reviewDoc)
	p := intakeFixturePreview(t, extraction, review, vault, state)
	plan, err := PlanIntakeApproval(context.Background(), extraction, review, vault, state, p.ReviewRevision, p.VaultRevision, p.RegistryRevision, intakeStamp)
	if err != nil {
		t.Fatal(err.Code)
	}
	var result IntakeApplyResult
	json.Unmarshal(plan.ResultBytes(), &result)
	if len(result.CreatedSkillIDs) != 1 || len(result.ChangedFiles) != 7 || len(result.Changes) != 7 {
		t.Fatal("incoherent combined proposal", result)
	}
	if *result.Changes[1].TargetID == *result.Changes[2].TargetID {
		t.Fatal("same-label creates collided")
	}
	for _, path := range intakereview.ApprovalPreviewPaths() {
		if len(plan.Replacements()[path]) == 0 {
			t.Fatal("missing prospective file", path)
		}
	}

}

func TestIntakeApprovalRecordSelectionRefusesInvalidChoices(t *testing.T) {
	e := intakeEntity{Type: "project", Title: "Source title"}
	source := intakeSourceValues(e, []string{"Evidence"}, nil)
	for _, fields := range []map[string]string{{"unknown": "vault"}, {"name": "add_source"}, {"technologies": "source"}} {
		if _, err := intakeSelected(e, intakeread.ReviewDecision{FieldResolutions: fields}, source); err == nil {
			t.Fatal("invalid choice accepted", fields)
		}
	}
	selected, err := intakeSelected(e, intakeread.ReviewDecision{FieldResolutions: map[string]string{"name": "vault", "proof_points": "add_source"}}, source)
	if err != nil || len(selected) != 1 {
		t.Fatal("valid choices rejected")
	}
	if !reflect.DeepEqual(intakeUnique([]string{"ＡＢＣ", "abc", "Another"}), []string{"ＡＢＣ", "Another"}) {
		t.Fatal("normalization mismatch")
	}
	if got := fmt.Sprintf("%+v", intakeFailure("PRIVATE_TEST")); strings.Contains(got, "PRIVATE_TEST") {
		t.Fatal("error formatting leaked")
	}
}

func TestIntakeApprovalIDsAndLegacyProfileContacts(t *testing.T) {
	empty := cataloginput.Value{Kind: "sequence"}
	for _, tc := range []struct{ name, id, want string }{
		{"École Straße ＡＢＣ", "entity", "ecole_strae_abc"},
		{"名字", "entity", "entity"}, {"名字", "名字", "career_record"},
	} {
		got, err := intakeID(intakeEntity{Type: "skill", Name: tc.name, ID: tc.id}, empty, "source")
		if err != nil || got != tc.want {
			t.Fatalf("slug=%s want=%s", got, tc.want)
		}
	}
	if _, err := intakeID(intakeEntity{Type: "skill", Name: "\xff"}, empty, "source"); err == nil {
		t.Fatal("invalid Unicode silently allocated fallback ID")
	}
	records, _ := cataloginput.ParseYAML([]byte("- {id: example, archived: true}\n"))
	e := intakeEntity{Type: "skill", Name: "Example", ID: "entity"}
	id, err := intakeID(e, records, "source")
	if err != nil || id == "example" || !strings.HasPrefix(id, "example_") {
		t.Fatal("archived ID reused")
	}
	r := cataloginput.Value{Kind: "mapping"}
	set(&r, "id", textValue(id))
	records.Items = append(records.Items, r)
	if _, err := intakeID(e, records, "source"); err == nil || err.Code != "CREATE_ID_CONFLICT" {
		t.Fatal("second collision accepted")
	}

	e = intakeEntity{Type: "profile", Emails: []string{""}, Phones: []string{""}, Links: []string{"", "https://example.invalid", "https://other.invalid"}}
	profile := cataloginput.Value{Kind: "mapping"}
	if err := intakeMergeProfile(e, intakeread.ReviewDecision{}, intakeSourceValues(e, nil, nil), &profile); err != nil {
		t.Fatal(err.Code)
	}
	contact := intakeField(profile, "contact")
	for _, key := range []string{"email", "phone"} {
		if v, present := get(contact, key); !present || v.Kind != "string" || v.Text != "" {
			t.Fatal("legacy empty contact changed")
		}
	}
	if intakeField(contact, "website").Text != "https://example.invalid" {
		t.Fatal("legacy first nonempty link changed")
	}
}

func TestIntakeApprovalRefusesUnauthorizedTargetsAndInvalidVault(t *testing.T) {
	for _, scenario := range []string{"off_preview", "archived_target", "dangling_skill", "missing_field", "bad_calendar", "missing_decision", "wrong_source"} {
		t.Run(scenario, func(t *testing.T) {
			extraction, review, vault, _, state := intakeApprovalFixture(t, "experience", "merge", nil)
			switch scenario {
			case "off_preview":
				review = bytes.ReplaceAll(review, []byte("experience_one"), []byte("unknown_target"))
			case "archived_target":
				vault["career_vault/experience_blocks.yaml"] = append(vault["career_vault/experience_blocks.yaml"], []byte("  archived: true\n")...)
			case "dangling_skill":
				vault["career_vault/experience_blocks.yaml"] = bytes.ReplaceAll(vault["career_vault/experience_blocks.yaml"], []byte("competency_ids: [core]"), []byte("competency_ids: [missing]"))
			case "missing_field":
				review = bytes.ReplaceAll(review, []byte(`"field_resolutions":null`), []byte(`"field_resolutions":{"technologies":"source","category":"source"}`))
			case "bad_calendar":
				vault["career_vault/experience_blocks.yaml"] = append(vault["career_vault/experience_blocks.yaml"], []byte("  date_range: {start: '2026-02', end: '2025-01'}\n")...)
			case "missing_decision":
				var doc map[string]any
				json.Unmarshal(review, &doc)
				doc["approval_decisions"] = []any{}
				review, _ = json.Marshal(doc)
			case "wrong_source":
				review = bytes.ReplaceAll(review, []byte("fictional.txt"), []byte("other.txt"))
			}
			want := map[string]string{"off_preview": "INVALID_MERGE_TARGET", "archived_target": "INVALID_MERGE_TARGET", "dangling_skill": "INVALID_COMPETENCY_REFERENCE", "missing_field": "INVALID_FIELD_RESOLUTION", "bad_calendar": "VAULT_VALIDATION_FAILED", "missing_decision": "APPROVAL_NOT_READY", "wrong_source": "APPROVAL_REVIEW_INVALID"}[scenario]
			reviewRevision, vaultRevision := "", ""
			if scenario != "missing_decision" && scenario != "wrong_source" {
				p := intakeFixturePreview(t, extraction, review, vault, state)
				reviewRevision, vaultRevision = p.ReviewRevision, p.VaultRevision
			}
			before, _ := json.Marshal(vault)
			plan, err := PlanIntakeApproval(context.Background(), extraction, review, vault, state, reviewRevision, vaultRevision, state.Snapshot.Revision(), intakeStamp)
			code := ""
			if err != nil {
				code = err.Code
			}
			if plan != nil || err == nil || err.Code != want {
				t.Fatalf("refusal=%s want=%s", code, want)
			}
			after, _ := json.Marshal(vault)
			if !bytes.Equal(before, after) {
				t.Fatal("refused operation edited inputs")
			}
		})
	}
}

func TestIntakeApprovalEmptySelectionAndInputGuards(t *testing.T) {
	extraction, review, vault, _, state := intakeApprovalFixture(t, "project", "merge", map[string]string{})
	p := intakeFixturePreview(t, extraction, review, vault, state)
	plan, err := PlanIntakeApproval(context.Background(), extraction, review, vault, state, p.ReviewRevision, p.VaultRevision, p.RegistryRevision, intakeStamp)
	if err != nil || len(plan.Replacements()) != 0 {
		t.Fatal("empty selection changed Vault or skills")
	}
	var result IntakeApplyResult
	json.Unmarshal(plan.ResultBytes(), &result)
	if result.VaultRevision != p.VaultRevision || len(result.Changes) != 1 || result.Changes[0].FieldResolutions == nil {
		t.Fatal("no-op approval lost audit or changed revision")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if plan, err := PlanIntakeApproval(ctx, extraction, review, vault, state, p.ReviewRevision, p.VaultRevision, p.RegistryRevision, intakeStamp); plan != nil || err == nil || err.Code != "APPROVAL_CANCELED" {
			t.Fatal("cancelled planning succeeded")
		}
	}
	for _, stamp := range []string{"", "invalid", "0000-01-01T00:00:00Z"} {
		if plan, err := PlanIntakeApproval(context.Background(), extraction, review, vault, state, p.ReviewRevision, p.VaultRevision, p.RegistryRevision, stamp); plan != nil || err == nil || err.Code != "APPROVAL_TIMESTAMP_INVALID" {
			t.Fatal("invalid approval clock accepted")
		}
	}
}
