package vaultread

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func validFixture() *fixtureReader {
	f := shapeFixture()
	raw := [vaultFileCount]string{
		"name: '  Synthetic Ω  '\ntarget_location: US\nwork_authorization: synthetic\nmajor: Computer Science\n",
		"schools: [{id: school, institution: Synthetic College, degree: BS}]\ncertifications: [{id: cert, name: Synthetic, credential: Test, completed: '2026'}]\n",
		"current_status: synthetic\nwork_authorization: synthetic\nrequires_future_sponsorship: false\nstem_opt_eligible: false\n",
		"- {id: exp, company: Synthetic, role: Engineer, dates: '2025', category: technical, technologies: [], proof_points: []}\n",
		"- {id: project, name: Synthetic, category: technical, summary: Test, technologies: [], proof_points: [], related_experience: [exp]}\n",
		"- {id: skill, skill_family: Technical, related_experience: [exp], related_projects: [project], related_certifications: [cert], keywords: []}\n",
		"- {id: proof, claim: Synthetic, evidence_type: experience, source_block: exp}\n",
		"standards_metadata: {reviewed_on: '2026-09-01', review_due: '2027-09-01', market: US, authority_order: [false]}\nresume_voice: {}\nbullet_rules: {}\ndocument_rules: {layout: single_column, standard_sections: [false]}\nats_rules: {}\nselection_rules: {allowed_decisions: [full, compact, excluded, full]}\nquality_gates: {}\nforbidden: []\n",
	}
	for index, text := range raw {
		f.files[schemas[index].path] = []byte(text)
	}
	return f
}

type validationCase struct {
	name string
	edit func(*fixtureReader)
	code string
}

func appendYAML(index int, text string) func(*fixtureReader) {
	return func(f *fixtureReader) {
		f.files[schemas[index].path] = append(f.files[schemas[index].path], []byte(text)...)
	}
}

func replaceYAML(index int, old, text string) func(*fixtureReader) {
	return func(f *fixtureReader) {
		path := schemas[index].path
		if !strings.Contains(string(f.files[path]), old) {
			panic("synthetic replacement not found")
		}
		f.files[path] = []byte(strings.ReplaceAll(string(f.files[path]), old, text))
	}
}

func validationCases() []validationCase {
	return []validationCase{
		{"valid", func(*fixtureReader) {}, ""},
		{"unknown_profile", appendYAML(profileIndex, "unknown: {flag: false, empty: [], null: null}\n"), ""},
		{"false_required", replaceYAML(profileIndex, "major: Computer Science", "major: false"), ""},
		{"empty_required_list", replaceYAML(profileIndex, "major: Computer Science", "major: []"), ""},
		{"zero_required", replaceYAML(profileIndex, "major: Computer Science", "major: 0"), ""},
		{"blank_required", replaceYAML(profileIndex, "major: Computer Science", "major: '  '"), ""},
		{"missing_required", replaceYAML(profileIndex, "major: Computer Science", ""), "VAULT_RECORD_VALIDATION_FAILED"},
		{"null_required", replaceYAML(profileIndex, "major: Computer Science", "major: null"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"empty_required", replaceYAML(profileIndex, "major: Computer Science", "major: ''"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"null_contact", appendYAML(profileIndex, "contact: null\n"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"empty_contact", appendYAML(profileIndex, "contact: {}\n"), ""},
		{"string_contact", appendYAML(profileIndex, "contact: private-contact\n"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"contact_entries", appendYAML(profileIndex, "contact: {secret: false, another: 1}\n"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"contact_key", appendYAML(profileIndex, "contact: {false: private}\n"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"role_targets", appendYAML(profileIndex, "role_targets: [false, '', null, [], developer]\n"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"roles_type", appendYAML(profileIndex, "role_targets: false\n"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"null_roles", appendYAML(profileIndex, "role_targets: null\n"), ""},
		{"mapping_short_circuit", func(f *fixtureReader) {
			replaceYAML(profileIndex, "major: Computer Science", "")(f)
			appendYAML(profileIndex, "contact: false\nrole_targets: false\n")(f)
		}, "VAULT_RECORD_VALIDATION_FAILED"},
		{"education_empty", replaceYAML(educationIndex, "schools: [{id: school, institution: Synthetic College, degree: BS}]", "schools: []"), ""},
		{"education_null", replaceYAML(educationIndex, "schools: [{id: school, institution: Synthetic College, degree: BS}]", "schools: null"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"certifications_null", replaceYAML(educationIndex, "certifications: [{id: cert, name: Synthetic, credential: Test, completed: '2026'}]", "certifications: null"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"education_multiple_errors", replaceYAML(educationIndex, "id: school, institution: Synthetic College, degree: BS", "id: cert, archived: 0, honors: [false, null], notes: false, date_range: null"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"education_nonmapping", replaceYAML(educationIndex, "{id: school, institution: Synthetic College, degree: BS}", "false"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"cert_duplicates", replaceYAML(educationIndex, "{id: cert, name: Synthetic, credential: Test, completed: '2026'}", "{id: cert, name: Synthetic, credential: Test, completed: '2026'}, {id: cert}"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"date_valid", replaceYAML(experienceIndex, "dates: '2025'", "dates: '2025', date_range: {start: '2026-01', end: '', ongoing: true}, graduation_month: 'Jan 2026', completed_month: '2026-01-01T12x+01:00'"), ""},
		{"date_errors", replaceYAML(experienceIndex, "dates: '2025'", "dates: '2025', date_range: {start: '2026-02', end: '2026-01'}, graduation_month: false, completed_month: ''"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"date_unknown_key", replaceYAML(experienceIndex, "dates: '2025'", "dates: '2025', date_range: {private-key: 1}"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"date_nonstr_key", replaceYAML(experienceIndex, "dates: '2025'", "dates: '2025', date_range: {true: 1}"), "VAULT_VALIDATION_UNSUPPORTED"},
		{"date_yaml_scalar", replaceYAML(experienceIndex, "dates: '2025'", "dates: '2025', completed_month: 2026-01-01"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"archived_type", replaceYAML(projectIndex, "category: technical", "category: technical, archived: 'true'"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"archived_still_referenced", replaceYAML(experienceIndex, "category: technical", "category: technical, archived: true"), ""},
		{"archived_still_validated", replaceYAML(experienceIndex, "category: technical", "category: null, archived: true"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"duplicate_experience", func(f *fixtureReader) {
			f.files[schemas[experienceIndex].path] = append(f.files[schemas[experienceIndex].path], f.files[schemas[experienceIndex].path]...)
		}, "VAULT_RECORD_VALIDATION_FAILED"},
		{"legacy_duplicate_ignored", func(f *fixtureReader) {
			f.files[schemas[skillIndex].path] = append(f.files[schemas[skillIndex].path], f.files[schemas[skillIndex].path]...)
		}, ""},
		{"nonstr_id", replaceYAML(projectIndex, "id: project", "id: false"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"empty_experiences", func(f *fixtureReader) { f.files[schemas[experienceIndex].path] = []byte("[]\n") }, "VAULT_RECORD_VALIDATION_FAILED"},
		{"nonmapping_project", func(f *fixtureReader) { f.files[schemas[projectIndex].path] = []byte("[null, false, 0]\n") }, "VAULT_RECORD_VALIDATION_FAILED"},
		{"reference_list_type", replaceYAML(projectIndex, "related_experience: [exp]", "related_experience: false"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"reference_null", replaceYAML(projectIndex, "related_experience: [exp]", "related_experience: null"), ""},
		{"reference_unknown", replaceYAML(skillIndex, "related_experience: [exp]", "related_experience: [secret, false, null]"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"reference_unhashable", replaceYAML(skillIndex, "related_projects: [project]", "related_projects: [[]]"), "VAULT_VALIDATION_UNSUPPORTED"},
		{"unknown_evidence", replaceYAML(proofIndex, "evidence_type: experience", "evidence_type: private"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"unhashable_evidence", replaceYAML(proofIndex, "evidence_type: experience", "evidence_type: []"), "VAULT_VALIDATION_UNSUPPORTED"},
		{"unhashable_proof_ref", replaceYAML(proofIndex, "source_block: exp", "source_block: {}"), "VAULT_VALIDATION_UNSUPPORTED"},
		{"proof_unknown", replaceYAML(proofIndex, "source_block: exp", "source_block: secret"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"style_mapping", replaceYAML(styleIndex, "resume_voice: {}", "resume_voice: []"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"style_metadata", replaceYAML(styleIndex, "{reviewed_on: '2026-09-01', review_due: '2027-09-01', market: US, authority_order: [false]}", "{}"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"style_dates", replaceYAML(styleIndex, "reviewed_on: '2026-09-01', review_due: '2027-09-01'", "reviewed_on: 2026-09-01, review_due: '2026-02-30'"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"style_iso_quirk", replaceYAML(styleIndex, "'2026-09-01'", "'20260101XX'"), ""},
		{"style_document", replaceYAML(styleIndex, "{layout: single_column, standard_sections: [false]}", "{layout: false, standard_sections: []}"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"style_decisions", replaceYAML(styleIndex, "[full, compact, excluded, full]", "[full, compact, false]"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"style_decisions_type", replaceYAML(styleIndex, "[full, compact, excluded, full]", "false"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"style_unhashable", replaceYAML(styleIndex, "[full, compact, excluded, full]", "[full, []]"), "VAULT_VALIDATION_UNSUPPORTED"},
		{"exception_overrides_domain", func(f *fixtureReader) {
			appendYAML(profileIndex, "contact: false\n")(f)
			replaceYAML(proofIndex, "source_block: exp", "source_block: {}")(f)
		}, "VAULT_VALIDATION_UNSUPPORTED"},
	}
}

func fixtureState(t *testing.T, f *fixtureReader) *vaultState {
	t.Helper()
	state, err := loadVault(context.Background(), f)
	if err != nil {
		t.Fatalf("synthetic load failed: %s %s", err.Code, err.Source)
	}
	return state
}

func TestManualValidationCases(t *testing.T) {
	for _, tc := range validationCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := validFixture()
			tc.edit(f)
			state := fixtureState(t, f)
			before, _ := json.Marshal(state.documents)
			err := validateProfileState(context.Background(), state)
			code := ""
			if err != nil {
				code = err.Code
			}
			if code != tc.code {
				t.Fatalf("code=%s want=%s", code, tc.code)
			}
			after, _ := json.Marshal(state.documents)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("read validation mutated decoded input")
			}
			if err != nil {
				for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
					if strings.Contains(fmt.Sprintf(format, err), "private") || strings.Contains(fmt.Sprintf(format, err), "secret") {
						t.Fatal("source content escaped error rendering")
					}
				}
			}
		})
	}
}

func TestValidationOrderingLimitsAndCancellation(t *testing.T) {
	f := validFixture()
	appendYAML(profileIndex, "role_targets: [false, null]\n")(f)
	replaceYAML(projectIndex, "related_experience: [exp]", "related_experience: [secret]")(f)
	err := validateProfileState(context.Background(), fixtureState(t, f))
	want := []Issue{
		{Source: "profile", Code: "STRING_REQUIRED", Loc: []any{"role_targets", 0}},
		{Source: "profile", Code: "STRING_REQUIRED", Loc: []any{"role_targets", 1}},
		{Source: "projects", Code: "UNKNOWN_ID", Loc: []any{0, "related_experience"}},
	}
	if err == nil || !reflect.DeepEqual(err.Issues, want) {
		t.Fatal("schema field/index order changed")
	}
	f = validFixture()
	appendYAML(profileIndex, "role_targets: ["+strings.Repeat("false,", maxValidationIssues+1)+"]\n")(f)
	state := fixtureState(t, f)
	if err := validateProfileState(context.Background(), state); err == nil || err.Code != "VAULT_VALIDATION_LIMIT" || err.Issues != nil {
		t.Fatal("issue bound did not refuse oversized error result")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if err := validateProfileState(ctx, state); err == nil || err.Code != "VAULT_READ_CANCELED" {
			t.Fatal("canceled validation continued")
		}
	}
}

func TestCalendarIssueFieldsAndRequiredFieldOrder(t *testing.T) {
	for _, tc := range []struct{ raw, field, code string }{
		{"{start: null}", "start", "CALENDAR_STRING_REQUIRED"},
		{"{start: '2026-01', end: 7}", "end", "CALENDAR_STRING_REQUIRED"},
		{"{start: '2026-01', ongoing: 0}", "ongoing", "CALENDAR_RANGE_ONGOING_BOOLEAN_REQUIRED"},
	} {
		f := validFixture()
		replaceYAML(experienceIndex, "dates: '2025'", "dates: '2025', date_range: "+tc.raw)(f)
		err := validateProfileState(context.Background(), fixtureState(t, f))
		if err == nil || len(err.Issues) != 1 || err.Issues[0].Code != tc.code ||
			!reflect.DeepEqual(err.Issues[0].Loc, []any{0, "date_range", tc.field}) {
			t.Fatal("calendar subfield location was discarded")
		}
	}
	f := validFixture()
	replaceYAML(profileIndex, "target_location: US", "target_location: ''")(f)
	replaceYAML(profileIndex, "major: Computer Science", "")(f)
	err := validateProfileState(context.Background(), fixtureState(t, f))
	if err == nil || len(err.Issues) != 1 || err.Issues[0].Code != "REQUIRED_FIELDS" ||
		!reflect.DeepEqual(err.Issues[0].Fields, []string{"target_location", "major"}) {
		t.Fatal("required field schema order changed")
	}
}
