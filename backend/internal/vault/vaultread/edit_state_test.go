package vaultread

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestEvidenceEditorStateWholeVaultSingleRecordAndIsolation(t *testing.T) {
	ctx := context.Background()
	for _, target := range []EditTarget{ExperienceTarget, ProjectTarget} {
		index, _ := target.index()
		f := validFixture()
		id := "exp"
		if target == ProjectTarget {
			id = "project"
		}
		raw := f.files[schemas[index].path]
		// Source responses contain only the selected record, not every sibling.
		other := strings.Replace(string(raw), "id: "+id, "id: sibling", 1)
		other = strings.Replace(other, "proof_points: []", "proof_points: [], extension: 2026-01-01", 1)
		candidate := append(append([]byte(nil), raw...), []byte(other)...)
		state, e := ReadEditorState(ctx, f)
		if e != nil {
			t.Fatal(e)
		}
		out, e := state.ValidateRecordReplacement(ctx, target, candidate, id)
		if e != nil || !strings.Contains(string(out), `"id":"`+id+`"`) || strings.Contains(string(out), "sibling") {
			t.Fatal("single-record projection changed")
		}
		if out, e := state.ValidateRecordReplacement(ctx, target, candidate, "sibling"); out != nil || e == nil || e.Code != "VAULT_RESPONSE_UNSUPPORTED" {
			t.Fatal("selected unsupported value was lost")
		}
		before, _ := state.Record(target)
		before.Items[0].Entries[0].Value.Text = "mutated"
		after, _ := state.Record(target)
		if field(after.Items[0], "id").Text != id {
			t.Fatal("sequence clone aliases editor state")
		}
		if out, e := state.ValidateRecordReplacement(ctx, target, raw, "missing"); out != nil || e == nil || e.Code != "VAULT_EDITOR_RECORD_NOT_FOUND" {
			t.Fatal("wrong selected identity")
		}
		if out, e := state.ValidateRecordReplacement(ctx, target, []byte("{}"), id); out != nil || e == nil || e.Code != "VAULT_ROOT_TYPE" {
			t.Fatal("wrong candidate root")
		}
		f.files[schemas[proofIndex].path] = []byte("- {id: p, claim: x, evidence_type: experience, source_block: missing}\n")
		state, e = ReadEditorState(ctx, f)
		if e != nil {
			t.Fatal(e)
		}
		if out, e := state.ValidateRecordReplacement(ctx, target, raw, id); out != nil || e == nil || e.Code != "VAULT_RECORD_VALIDATION_FAILED" {
			t.Fatal("invalid companion skipped")
		}
	}
}

func TestEvidenceCreationIDsAndClosedEditorMethods(t *testing.T) {
	ctx := context.Background()
	f := validFixture()
	f.files[schemas[experienceIndex].path] = []byte("[{id: exp}, {id: 1}, {id: true}, {id: 0}, {id: false}, {id: ''}, {id: null}, {id: [one, two]}, {id: 2026-01-01}, {}]")
	state, e := ReadEditorState(ctx, f)
	if e != nil {
		t.Fatal(e)
	}
	ids, e := state.CreationIDs(ctx, ExperienceTarget)
	want := map[string]bool{"exp": true, "1": true, "True": true, "['one', 'two']": true, "2026-01-01": true}
	if e != nil || !reflect.DeepEqual(ids, want) {
		t.Fatal("truthy Python identity conversion changed")
	}
	if _, e := state.CreationIDs(nil, ExperienceTarget); e == nil {
		t.Fatal("missing context")
	}
	for _, target := range []EditTarget{ProfileTarget, EligibilityTarget, 99} {
		if _, e := state.CreationIDs(ctx, target); e == nil {
			t.Fatal("unbounded allocation target")
		}
		if _, e := state.ValidateRecordReplacement(ctx, target, []byte("{}"), "x"); e == nil {
			t.Fatal("unbounded record target")
		}
	}
	for _, target := range []EditTarget{ExperienceTarget, ProjectTarget} {
		if _, e := state.ValidateReplacement(ctx, target, []byte("[]")); e == nil {
			t.Fatal("mapping editor widened")
		}
		if _, e := (*EditorState)(nil).CreationIDs(ctx, target); e == nil {
			t.Fatal("nil identity state")
		}
		if _, e := (*EditorState)(nil).ValidateRecordReplacement(ctx, target, nil, "x"); e == nil {
			t.Fatal("nil candidate state")
		}
	}
}

func TestEditorStateRepairsOnlyCandidateWithoutMutatingCapturedState(t *testing.T) {
	fixture := validFixture()
	original := []byte("name: null\nextra: {keep: [one, two]}\n")
	fixture.files[schemas[profileIndex].path] = original
	state, e := ReadEditorState(context.Background(), fixture)
	if e != nil {
		t.Fatal(e)
	}
	first, _ := state.Record(ProfileTarget)
	first.Entries[1].Value.Entries[0].Value.Items[0].Text = "mutated"
	again, _ := state.Record(ProfileTarget)
	if again.Entries[1].Value.Entries[0].Value.Items[0].Text != "one" {
		t.Fatal("state aliases caller")
	}
	valid := validFixture().files[schemas[profileIndex].path]
	response, e := state.ValidateReplacement(context.Background(), ProfileTarget, valid)
	if e != nil || len(response) == 0 {
		t.Fatal("candidate repair rejected")
	}
	revision, ok := state.Revision(ProfileTarget)
	if !ok || revision != fmt.Sprintf("sha256:%x", sha256.Sum256(original)) {
		t.Fatal("captured revision mutated")
	}
	after, _ := state.Record(ProfileTarget)
	if !reflect.DeepEqual(again, after) {
		t.Fatal("candidate modified original")
	}
	// The same replacement may not excuse an unrelated reference error.
	fixture.files[schemas[proofIndex].path] = []byte("- {id: p, claim: x, evidence_type: experience, source_block: missing}\n")
	state, e = ReadEditorState(context.Background(), fixture)
	if e != nil {
		t.Fatal(e)
	}
	response, e = state.ValidateReplacement(context.Background(), ProfileTarget, valid)
	if response != nil || e == nil || e.Code != "VAULT_RECORD_VALIDATION_FAILED" {
		t.Fatal("invalid companion ignored")
	}
}

func TestEditorStateRejectsInvalidUseAndUnsupportedResponse(t *testing.T) {
	var absent *EditorState
	for _, state := range []*EditorState{nil, {}, {state: &vaultState{}}} {
		if state == nil || state.state == nil {
			if _, ok := state.Record(ProfileTarget); ok {
				t.Fatal("uninitialized record")
			}
			if _, ok := state.Revision(ProfileTarget); ok {
				t.Fatal("uninitialized revision")
			}
		}
		if _, ok := state.Record(99); ok {
			t.Fatal("unknown target")
		}
		if _, ok := state.Revision(99); ok {
			t.Fatal("unknown target")
		}
		if _, e := state.ValidateReplacement(context.Background(), 99, nil); e == nil {
			t.Fatal("unknown candidate")
		}
	}
	if _, e := absent.ValidateReplacement(context.Background(), ProfileTarget, nil); e == nil {
		t.Fatal("nil state")
	}
	if _, ok := EditTarget(99).Path(); ok {
		t.Fatal("unbounded path")
	}
	if path, ok := EligibilityTarget.Path(); !ok || path != "career_vault/visa_profile.yaml" {
		t.Fatal("eligibility path")
	}
	if _, e := ReadEditorState(context.Background(), nil); e == nil {
		t.Fatal("nil source")
	}
	state, e := ReadEditorState(context.Background(), validFixture())
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range []struct{ raw, code string }{
		{"[", "VAULT_EDITOR_CANDIDATE_INVALID"},
		{"[]", "VAULT_ROOT_TYPE"},
		{string(validFixture().files[schemas[profileIndex].path]) + "extra: .nan\n", "VAULT_RESPONSE_UNSUPPORTED"},
	} {
		if out, e := state.ValidateReplacement(context.Background(), ProfileTarget, []byte(item.raw)); out != nil || e == nil || e.Code != item.code {
			t.Fatal("candidate failure changed", item.code)
		}
	}
	if _, e := state.ValidateReplacement(nil, ProfileTarget, []byte("{}")); e == nil || e.Code != "VAULT_READ_CANCELED" {
		t.Fatal("nil context")
	}
	fixture := validFixture()
	delete(fixture.files, schemas[educationIndex].path)
	if _, e := ReadEditorState(context.Background(), fixture); e == nil {
		t.Fatal("missing source")
	}
}

func TestEducationEditorTargetsAndCrossCollectionIDs(t *testing.T) {
	ctx := context.Background()
	f := validFixture()
	raw := []byte("schools: [{id: school, institution: Test, degree: BS}]\ncertifications: [{id: cert, name: Test, credential: Test, completed: '2025', extra: 2026-01-01}]\n")
	f.files[schemas[educationIndex].path] = raw
	state, err := ReadEditorState(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []EditTarget{SchoolTarget, CertificationTarget} {
		path, ok := target.Path()
		if !ok || path != schemas[educationIndex].path {
			t.Fatal("wrong Education path")
		}
		ids, err := state.CreationIDs(ctx, target)
		if err != nil || !reflect.DeepEqual(ids, map[string]bool{"school": true, "cert": true}) {
			t.Fatal("shared namespace", ids, err)
		}
		if _, err := state.ValidateReplacement(ctx, target, raw); err == nil {
			t.Fatal("whole mapping projection widened")
		}
	}
	out, err := state.ValidateRecordReplacement(ctx, SchoolTarget, raw, "school")
	if err != nil || !strings.Contains(string(out), `"id":"school"`) || strings.Contains(string(out), "credential") {
		t.Fatal("unrelated record caused response failure", err)
	}
	if out, err := state.ValidateRecordReplacement(ctx, CertificationTarget, raw, "cert"); out != nil || err == nil || err.Code != "VAULT_RESPONSE_UNSUPPORTED" {
		t.Fatal("unsupported selected record")
	}
	if _, err := state.ValidateRecordReplacement(ctx, CertificationTarget, raw, "school"); err == nil || err.Code != "VAULT_EDITOR_RECORD_NOT_FOUND" {
		t.Fatal("cross-collection selection accepted")
	}
	if _, err := state.ValidateRecordReplacement(ctx, SchoolTarget, []byte(strings.ReplaceAll(string(raw), "id: cert", "id: school")), "school"); err == nil || err.Code != "VAULT_RECORD_VALIDATION_FAILED" {
		t.Fatal("duplicate across collections accepted")
	}
	f.files[schemas[educationIndex].path] = []byte("schools: [{id: 1}, {id: false}, {id: null}]\ncertifications: [{id: true}, {id: [one, two]}]\n")
	state, err = ReadEditorState(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := state.CreationIDs(ctx, SchoolTarget)
	if err != nil || !reflect.DeepEqual(ids, map[string]bool{"1": true, "True": true, "['one', 'two']": true}) {
		t.Fatal("source truthiness/conversion", ids, err)
	}
	f.files[schemas[educationIndex].path] = []byte("schools: null\n")
	state, err = ReadEditorState(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreationIDs(ctx, SchoolTarget); err == nil || err.Code != "VAULT_EDITOR_EDUCATION_LIST_REQUIRED" {
		t.Fatal("non-list identity source")
	}
	f.files[schemas[educationIndex].path] = []byte("{}")
	state, err = ReadEditorState(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := state.CreationIDs(ctx, CertificationTarget); err != nil || len(ids) != 0 {
		t.Fatal("absent collections")
	}
}

func TestLegacySkillEditorTargetReferencesAndProjection(t *testing.T) {
	ctx := context.Background()
	f := validFixture()
	raw := []byte("- {id: skill, skill_family: Test, keywords: [Go], related_experience: [exp], related_projects: [project]}\n- {id: other, skill_family: Other, keywords: [], related_experience: [], extra: 2026-01-01}\n")
	f.files[schemas[skillIndex].path] = raw
	state, err := ReadEditorState(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if path, ok := LegacySkillTarget.Path(); !ok || path != schemas[skillIndex].path {
		t.Fatal("wrong legacy target")
	}
	if _, ok := LegacySkillTarget.EducationCollection(); ok {
		t.Fatal("legacy target is not Education")
	}
	ids, err := state.CreationIDs(ctx, LegacySkillTarget)
	if err != nil || !reflect.DeepEqual(ids, map[string]bool{"skill": true, "other": true}) {
		t.Fatal("legacy identity namespace", ids, err)
	}
	if _, err := state.ValidateReplacement(ctx, LegacySkillTarget, raw); err == nil {
		t.Fatal("whole mapping projection widened")
	}
	out, err := state.ValidateRecordReplacement(ctx, LegacySkillTarget, raw, "skill")
	if err != nil || !strings.Contains(string(out), `"id":"skill"`) || strings.Contains(string(out), `"id":"other"`) {
		t.Fatal("unselected extension caused response failure", err)
	}
	if out, err := state.ValidateRecordReplacement(ctx, LegacySkillTarget, raw, "other"); out != nil || err == nil || err.Code != "VAULT_RESPONSE_UNSUPPORTED" {
		t.Fatal("unsupported selected response")
	}
	for _, test := range []struct{ raw, id, code string }{
		{string(raw), "absent", "VAULT_EDITOR_RECORD_NOT_FOUND"},
		{"{}", "skill", "VAULT_ROOT_TYPE"},
		{strings.ReplaceAll(string(raw), "related_projects: [project]", "related_projects: [missing]"), "skill", "VAULT_RECORD_VALIDATION_FAILED"},
	} {
		if out, err := state.ValidateRecordReplacement(ctx, LegacySkillTarget, []byte(test.raw), test.id); out != nil || err == nil || err.Code != test.code {
			t.Fatal("candidate guard", test.code, err)
		}
	}
	f.files[schemas[skillIndex].path] = []byte("[{id: 1}, {id: true}, {id: false}, {id: 0}, {id: null}, {id: ''}, {id: [one, two]}]")
	state, err = ReadEditorState(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	ids, err = state.CreationIDs(ctx, LegacySkillTarget)
	if err != nil || !reflect.DeepEqual(ids, map[string]bool{"1": true, "True": true, "['one', 'two']": true}) {
		t.Fatal("truthy source conversion", ids, err)
	}
}
