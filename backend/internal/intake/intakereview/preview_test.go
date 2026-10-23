package intakereview

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func previewFixture(t *testing.T) ([]byte, []byte, map[string][]byte) {
	t.Helper()
	extraction := suggestionExtraction(t, []suggestionBlock{{text: "Fictional profile"}, {text: "Built a local workflow", isBullet: true}, {text: "Reviewed education"}, {text: "Reviewed certification"}, {text: "Project evidence", isBullet: true}, {text: "Python and SQL"}, {text: "Professional summary"}, {text: "Miscellaneous retained source"}}, nil, nil, nil)
	built, failure := Build(extraction, nil)
	if failure != nil {
		t.Fatal(failure.Code)
	}
	document := suggestionDocument(t, built.Bytes)
	entities := []any{
		map[string]any{"id": "profile_one", "entity_type": "profile", "name": "Ｆｉｃｔｉｏｎａｌ Person", "emails": []string{"person@example.invalid"}, "links": []string{"https://github.com/fictional", "https://example.invalid"}},
		map[string]any{"id": "experience_one", "entity_type": "experience", "organization": "Straße Systems", "title": "Software Engineer", "date_text": "2022–2024", "technologies": []string{"python"}, "pending_skills": []any{map[string]any{"name": "Local tool"}}, "items": []string{"Built a local workflow", "Built_a local workflow"}},
		map[string]any{"id": "education_one", "entity_type": "education", "institution": "Example University", "degree": "BS", "major": "Computing", "date_text": "2024"},
		map[string]any{"id": "certification_one", "entity_type": "certification", "name": "Cloud Training", "credential": "Practitioner", "date_text": "2023"},
		map[string]any{"id": "project_one", "entity_type": "project", "title": "Fictional Local Search", "items": []string{"A useful project"}, "technologies": []string{"unknown"}},
		map[string]any{"id": "skill_one", "entity_type": "skill", "name": "Languages", "items": []string{"Python", "SQL"}},
		map[string]any{"id": "summary_one", "entity_type": "summary", "content": "Fictional summary"},
	}
	document["entities"] = entities
	for i, value := range document["block_reviews"].([]any) {
		item := value.(map[string]any)
		if i < len(entities) {
			entity := entities[i].(map[string]any)
			item["disposition"] = "assigned"
			item["owner_id"] = entity["id"]
		} else {
			item["disposition"] = "categorized"
			item["category"] = "other"
		}
	}
	review, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	paths := ApprovalPreviewPaths()
	vault := map[string][]byte{
		paths[0]: []byte(`name: Fictional Person
current_location: Example Town
contact:
  email: person@example.invalid
  github: https://github.com/fictional
`),
		paths[1]: []byte(`schools:
  - id: school_one
    institution: Example University
    degree: BS
    major: Computing
    graduation_date: 2024
    gpa: 3.9
    honors: [Honors]
certifications:
  - id: cert_one
    name: Cloud Training
    credential: Practitioner
    completed: 2023-06-01
    proof_points: [Completed training]
`),
		paths[2]: []byte(`- id: experience_archived
  company: STRASSE SYSTEMS
  archived: true
- id: experience_one
  company: STRASSE SYSTEMS
  role: Software Engineer
  dates: 2022-2024
  location: Example Town
  category: engineering
  technologies: [python]
  proof_points: [Delivered a local system]
`),
		paths[3]: []byte(`- id: project_one
  name: Fictional Local Search
  category: engineering
  summary: Local search prototype
  technologies: [unknown]
  proof_points: [Search evidence]
`),
		paths[4]: []byte(`- id: unrelated
  skill_family: Other
  keywords: [Python]
- id: tools
  skill_family: Tools
  keywords: [Python, SQL]
- id: languages
  skill_family: Languages
  keywords: [Python, SQL]
`),
	}
	return extraction, review, vault
}

func TestApprovalPreviewPreservesEvidenceAndDoesNotMutateInputs(t *testing.T) {
	extraction, review, vault := previewFixture(t)
	before, _ := json.Marshal(vault)
	reviewBefore := bytes.Clone(review)
	plan, failure := BuildApprovalPreview(context.Background(), extraction, review, vault, strings.Repeat("a", 64), map[string]string{"python": "Python"})
	if failure != nil {
		t.Fatal(failure.Code)
	}
	if len(plan.Entities) != 7 || len(plan.CategoryOnlyBlocks) != 1 || plan.Entities[6].Outcome != "unsupported" {
		t.Fatal("preview inventory changed")
	}
	experience := plan.Entities[1]
	if len(experience.Candidates) != 1 || experience.Candidates[0].TargetID != "experience_one" || !reflect.DeepEqual(experience.SourceFields["technologies"], []string{"Python", "Local tool"}) || !reflect.DeepEqual(experience.SourceFields["proof_points"], []string{"Built a local workflow"}) {
		t.Fatal("candidate or evidence projection changed", experience)
	}
	if len(experience.SourceBlocks) != 1 || experience.SourceBlocks[0].Category != "experience" {
		t.Fatal("legacy category backfill missing")
	}
	if got := plan.Entities[5].Candidates; len(got) != 2 || got[0].TargetID != "languages" || got[1].TargetID != "tools" {
		t.Fatal("skill ranking changed", got)
	}
	after, _ := json.Marshal(vault)
	if !bytes.Equal(before, after) || !bytes.Equal(review, reviewBefore) {
		t.Fatal("pure preview mutated inputs")
	}
	modified := bytes.ReplaceAll(vault[ApprovalPreviewPaths()[0]], []byte("Example Town"), []byte("Another Town"))
	vault[ApprovalPreviewPaths()[0]] = modified
	next, failure := BuildApprovalPreview(context.Background(), extraction, review, vault, strings.Repeat("a", 64), nil)
	if failure != nil || next.VaultRevision == plan.VaultRevision {
		t.Fatal("exact Vault revision not updated")
	}
}

func TestApprovalPreviewRefusesIncompleteUnsafeOrCanceledInputs(t *testing.T) {
	for _, test := range []struct {
		name, code string
		modify     func(*[]byte, map[string][]byte)
	}{
		{"incomplete", "APPROVAL_PREVIEW_INCOMPLETE", func(review *[]byte, _ map[string][]byte) {
			*review = bytes.Replace(*review, []byte(`"assigned"`), []byte(`"unassigned"`), 1)
		}},
		{"source path", "APPROVAL_PREVIEW_REVIEW_INVALID", func(review *[]byte, _ map[string][]byte) {
			*review = bytes.ReplaceAll(*review, []byte("fictional.txt"), []byte("foreign.txt"))
		}},
		{"missing vault", "APPROVAL_PREVIEW_VAULT_MISSING", func(_ *[]byte, vault map[string][]byte) { delete(vault, ApprovalPreviewPaths()[0]) }},
		{"unsafe scalar", "APPROVAL_PREVIEW_VAULT_UNSUPPORTED", func(_ *[]byte, vault map[string][]byte) {
			vault[ApprovalPreviewPaths()[0]] = []byte("name: !!binary cHJpdmF0ZQ==\n")
		}},
		{"invalid list", "APPROVAL_PREVIEW_VAULT_UNSUPPORTED", func(_ *[]byte, vault map[string][]byte) {
			vault[ApprovalPreviewPaths()[2]] = []byte("- id: one\n  company: Straße Systems\n  technologies: wrong\n")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			extraction, review, vault := previewFixture(t)
			test.modify(&review, vault)
			result, failure := BuildApprovalPreview(context.Background(), extraction, review, vault, strings.Repeat("a", 64), nil)
			if result != nil || failure == nil || failure.Code != test.code {
				t.Fatal(result, failure)
			}
		})
	}
	extraction, review, vault := previewFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, failure := BuildApprovalPreview(ctx, extraction, review, vault, strings.Repeat("a", 64), nil); result != nil || failure == nil || failure.Code != "APPROVAL_PREVIEW_CANCELED" {
		t.Fatal(result, failure)
	}
	if result, failure := BuildApprovalPreview(context.Background(), extraction, review, vault, "not-a-revision", nil); result != nil || failure == nil || failure.Code != "APPROVAL_PREVIEW_REGISTRY_INVALID" {
		t.Fatal(result, failure)
	}
}

func TestApprovalPreviewNameRules(t *testing.T) {
	for _, test := range []struct {
		a, b  string
		match bool
	}{{"Straße", "STRASSE", true}, {"Ｆｕｌｌ Stack", "Full Stack", true}, {"ABC", "ABC Inc", false}, {"A B C D E", "A B C X Y", true}, {"A B", "A B C", false}, {"", "", false}, {"八文字以上の会社名称", "八文字以上の会社名称株式会社", true}} {
		if got := previewOverlap(test.a, test.b); got != test.match {
			t.Fatalf("name rule %q/%q: %v", test.a, test.b, got)
		}
	}
	if got := previewYears("年2024 年 2023 2024_ 2025", "2024 2023 2025"); !reflect.DeepEqual(got, []string{"2023", "2025"}) {
		t.Fatal("Unicode year boundaries", got)
	}
}

func TestApprovalPreviewBoundsExpandedResponseAndComparisons(t *testing.T) {
	extraction, review, vault := previewFixture(t)
	document := suggestionDocument(t, review)
	for _, id := range []string{"project_two", "project_three"} {
		document["entities"] = append(document["entities"].([]any), map[string]any{"id": id, "entity_type": "project", "title": "Fictional Local Search"})
	}
	review, _ = json.Marshal(document)
	vault[ApprovalPreviewPaths()[3]] = []byte("- id: project_one\n  name: Fictional Local Search\n  summary: " + strings.Repeat("x", 1500000) + "\n")
	result, failure := BuildApprovalPreview(context.Background(), extraction, review, vault, strings.Repeat("a", 64), nil)
	if result != nil || failure == nil || failure.Code != "APPROVAL_PREVIEW_RESULT_LIMIT" {
		t.Fatal("expanded preview was not bounded", failure)
	}
	state, _, failure := previewVault(vault)
	if failure != nil {
		t.Fatal(failure.Code)
	}
	count := 1000000
	if result, failure := previewCandidates(context.Background(), map[string]any{"entity_type": "project", "title": "Fictional Local Search"}, state, &count); result != nil || failure == nil || failure.Code != "APPROVAL_PREVIEW_INPUT_LIMIT" {
		t.Fatal("comparison budget not enforced", failure)
	}
}
