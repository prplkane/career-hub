package vaultwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/catalogtransaction"
	"careerhub.local/backend/internal/catalogwrite"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/vaultread"
)

type evidenceCatalogMemory map[string][]byte

func (m evidenceCatalogMemory) Check(string) *runtimepaths.Error { return nil }
func (m evidenceCatalogMemory) ReadFile(path string, _ int64) ([]byte, bool, *runtimepaths.Error) {
	raw, ok := m[path]
	return bytes.Clone(raw), ok, nil
}
func evidenceCatalogState(t *testing.T) *catalogstore.State {
	t.Helper()
	m := evidenceCatalogMemory{
		catalogstore.CoreGroupsPath:   []byte("- {id: tools, name: Tools}\n"),
		catalogstore.CoreSkillsPath:   []byte("- {id: core, name: Core, category: tool, primary_group_id: tools}\n"),
		catalogstore.CustomGroupsPath: []byte("- {id: user_tools, name: User tools}\n"),
		catalogstore.CustomSkillsPath: []byte("- {id: retired, name: Retired, category: tool, primary_group_id: user_tools, archived: true}\n"),
	}
	state, err := catalogstore.ReadState(m, m)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestEvidenceSelectionBounds(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{"competency_ids": []any{}, "pending_skills": []any{}, "registry_revision": "sha256:test"}
	}
	if got, err := parseEvidenceSelection(map[string]any{"technologies": []any{"Free text"}}); got != nil || err != nil {
		t.Fatal("implicit skill selection")
	}
	for _, test := range []struct {
		key   string
		value any
		code  string
	}{
		{"competency_ids", "core", "VAULT_EDITOR_INVALID"},
		{"competency_ids", []any{42}, "VAULT_EDITOR_INVALID"},
		{"pending_skills", nil, "VAULT_EDITOR_INVALID"},
		{"registry_revision", 42, "VAULT_EDITOR_INVALID"},
		{"pending_skills", []any{42}, "VAULT_EDITOR_INVALID"},
		{"pending_skills", []any{map[string]any{"name": true}}, "VAULT_EDITOR_INVALID"},
		{"pending_skills", []any{map[string]any{"name": "X", "primary_group_id": false}}, "VAULT_EDITOR_INVALID"},
		{"competency_ids", make([]any, cataloginput.YAMLMaxNodes+1), "VAULT_EDITOR_INPUT_LIMIT"},
		{"pending_skills", make([]any, cataloginput.YAMLMaxNodes+1), "VAULT_EDITOR_INPUT_LIMIT"},
		{"competency_ids", []any{strings.Repeat("x", maxPayloadBytes+1)}, "VAULT_EDITOR_INPUT_LIMIT"},
		{"registry_revision", strings.Repeat("x", maxPayloadBytes+1), "VAULT_EDITOR_INPUT_LIMIT"},
		{"registry_revision", "\xff", "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
		{"competency_ids", []any{"\xff"}, "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
		{"pending_skills", []any{map[string]any{"name": "\xff"}}, "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
		{"pending_skills", []any{map[string]any{"name": "X", "primary_group_id": "\xff"}}, "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
	} {
		payload := valid()
		payload[test.key] = test.value
		if got, err := parseEvidenceSelection(payload); got != nil || err == nil || err.Code != test.code {
			t.Fatalf("%s: %#v", test.key, err)
		}
	}
	payload := valid()
	payload["competency_ids"] = []any{"core"}
	payload["pending_skills"] = []any{map[string]any{"name": "Exact Tool", "primary_group_id": "user_tools"}}
	selection, err := parseEvidenceSelection(payload)
	if err != nil || len(selection.pending) != 1 || *selection.pending[0].GroupID != "user_tools" || !reflect.DeepEqual(selection.ids, []string{"core"}) {
		t.Fatal("explicit selection")
	}
}

func TestPriorEvidenceIDsQualifiedIterableShapes(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want map[string]bool
		fail bool
	}{
		{"{}", map[string]bool{}, false},
		{"{competency_ids: [core, 1, core, null]}", map[string]bool{"core": true}, false},
		{"{competency_ids: {core: true, 1: no}}", map[string]bool{"core": true}, false},
		{"{competency_ids: 'Ωx'}", map[string]bool{"Ω": true, "x": true}, false},
		{"{competency_ids: !!binary YQ==}", map[string]bool{}, false},
		{"{competency_ids: null}", nil, true},
		{"{competency_ids: 1}", nil, true},
	} {
		got, err := priorEvidenceIDs(evidenceValue(t, test.raw))
		if (err != nil) != test.fail || !reflect.DeepEqual(got, test.want) {
			t.Fatal(test.raw, got, err)
		}
	}
}

func TestEvidenceSkillLinkValidationIsAtomic(t *testing.T) {
	state := evidenceCatalogState(t)
	before := "- {id: selected, competency_ids: [retired]}\n- {id: other, private: [true, null]}\n"
	valid := "- {id: selected, competency_ids: [retired, core], technologies: [Retired, Core]}\n- {id: other, private: [true, null]}\n"
	if err := validateEvidenceSkillLinks(evidenceValue(t, before), evidenceValue(t, valid), "selected", state); err != nil {
		t.Fatal(err)
	}
	for _, after := range []string{
		strings.Replace(valid, "[Retired, Core]", "[Wrong, Core]", 1),
		strings.Replace(valid, "[retired, core]", "[core, core]", 1),
		strings.Replace(valid, "[retired, core]", "[retired, missing]", 1),
		strings.Replace(valid, "[Retired, Core]", "[]", 1),
		strings.Replace(valid, "[retired, core]", "null", 1),
		strings.Replace(valid, "[true, null]", "[false, null]", 1),
		strings.Replace(valid, "id: other", "id: selected", 1),
		"- {id: selected, competency_ids: [], technologies: []}",
		valid + "- {id: unexpected}",
	} {
		if err := validateEvidenceSkillLinks(evidenceValue(t, before), evidenceValue(t, after), "selected", state); err == nil {
			t.Fatal("accepted invalid target", after)
		}
	}
	newRecord := "- {id: added, competency_ids: [core], technologies: [Core]}"
	if err := validateEvidenceSkillLinks(evidenceValue(t, before), evidenceValue(t, before+newRecord), "added", state); err != nil {
		t.Fatal("append", err)
	}
	for _, mutation := range []struct{ old, next, id string }{
		{before, newRecord + "\n" + before, "added"},
		{strings.Replace(before, "[retired]", "[]", 1), valid, "selected"},
		{strings.Replace(before, "[retired]", "null", 1), valid, "selected"},
		{strings.Replace(before, "id: selected", "id: renamed", 1), valid, "selected"},
		{before, valid, "missing"},
		{"{}", valid, "selected"},
	} {
		if err := validateEvidenceSkillLinks(evidenceValue(t, mutation.old), evidenceValue(t, mutation.next), mutation.id, state); err == nil {
			t.Fatal("accepted invalid prior/link")
		}
	}
	if err := validateEvidenceSkillLinks(evidenceValue(t, before), evidenceValue(t, valid), "selected", nil); err == nil {
		t.Fatal("nil registry")
	}
}

func TestEvidenceSkillPlanDoesNotMutateInputs(t *testing.T) {
	state := evidenceCatalogState(t)
	raw := "{id: selected, competency_ids: [retired], technologies: [Legacy]}"
	selection := &evidenceSelection{ids: []string{"retired", "core", "retired"}, revision: state.Snapshot.Revision(),
		pending: []catalogwrite.PendingSkill{{Name: "New Exact Tool"}}}
	record := evidenceValue(t, raw)
	planned, plan, err := planEvidenceSkills(context.Background(), state, selection, "selected", "2026-09-01T12:00:00Z", record)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := get(planned, "competency_ids")
	if len(ids.Items) != 3 || ids.Items[0].Text != "retired" || ids.Items[1].Text != "core" || len(plan.Result().CreatedSkills) != 1 {
		t.Fatal("skill resolution")
	}
	// Value has slices: the planner must not alias its caller's original record.
	if !equalValue(record, evidenceValue(t, raw)) {
		t.Fatal("original record was mutated")
	}
	if len(state.CustomSkills) != 1 {
		t.Fatal("original catalog was mutated")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := planEvidenceSkills(ctx, state, selection, "selected", "stamp", evidenceValue(t, raw)); err == nil || err.Code != "VAULT_EDITOR_CANCELED" {
		t.Fatal("canceled plan", err)
	}
	if _, _, err := planEvidenceSkills(nil, nil, nil, "", "", record); err == nil || err.Code != "VAULT_EDITOR_SKILLS_UNAVAILABLE" {
		t.Fatal("invalid configuration")
	}
}

func TestEvidenceServiceConfigurationAndErrors(t *testing.T) {
	for _, broken := range []func(*EvidenceService){
		func(s *EvidenceService) { s.files = nil }, func(s *EvidenceService) { s.files.gate = nil },
		func(s *EvidenceService) { s.files.read = nil }, func(s *EvidenceService) { s.files.replace = nil },
		func(s *EvidenceService) { s.files.close = nil }, func(s *EvidenceService) { s.now = nil },
		func(s *EvidenceService) { s.begin = nil }, func(s *EvidenceService) { s.recover = nil },
	} {
		service := NewEvidence("not-opened", "not-opened")
		broken(service)
		out, err := service.CreateExperience(context.Background(), nil, "")
		if err == nil || err.Code != "VAULT_EDITOR_UNAVAILABLE" || out.Committed {
			t.Fatal("configuration", err)
		}
	}
	var service *EvidenceService
	if _, err := service.RecoverEvidence(context.Background()); err == nil || err.Code != "VAULT_EDITOR_UNAVAILABLE" {
		t.Fatal("nil service")
	}
	if _, err := NewEvidence("", "").RecoverEvidence(nil); err == nil || err.Code != "VAULT_EDITOR_UNAVAILABLE" {
		t.Fatal("nil context")
	}
	for _, cause := range []error{catalogtransaction.ErrPending, catalogtransaction.ErrConflict, catalogtransaction.ErrJournal, catalogtransaction.ErrTarget, catalogtransaction.ErrInput, context.DeadlineExceeded} {
		if err := evidenceTransactionError(cause); err == nil || err.Code == "VAULT_EDITOR_TRANSACTION_FAILED" {
			t.Fatal("unmapped transaction error")
		}
	}
	for _, raw := range [][]byte{nil, []byte("[]"), []byte("x")} {
		if _, err := evidenceMetadata(raw, "revision", nil); err == nil {
			t.Fatal("bad framing")
		}
	}
	if _, err := evidenceMetadata(append(bytes.Repeat([]byte(" "), vaultread.MaxProfileBytes), '}'), "revision", nil); err == nil || err.Code != "VAULT_RESPONSE_LIMIT" {
		t.Fatal("response bound")
	}
	created := []string{}
	out, err := evidenceMetadata([]byte(`{"record":{"id":"synthetic"}}`), "revision", &created)
	if err != nil || !json.Valid(out) || !bytes.Contains(out, []byte(`"created_skill_ids":[]`)) {
		t.Fatal("response metadata")
	}
	problem := &Error{Code: "synthetic", SourceMessage: "private evidence text", Catalog: &catalogwrite.Error{Code: "private registry text"}}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if strings.Contains(fmt.Sprintf(format, problem), "private") {
			t.Fatal("private details printed")
		}
	}
}
