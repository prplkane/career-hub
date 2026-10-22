package vaultwrite

import (
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/vaultread"
)

func educationPayload(target vaultread.EditTarget) map[string]any {
	if target == vaultread.SchoolTarget {
		return map[string]any{"institution": "New College", "degree": "BS", "dates": "", "graduation_date": "", "honors": []any{}}
	}
	return map[string]any{"name": "New Credential", "credential": "Provider", "completed": "May 2026", "proof_points": []any{}}
}
func TestEducationPlansPreserveBothCollectionsAndClearOnlyOwnedDates(t *testing.T) {
	for _, target := range []vaultread.EditTarget{vaultread.SchoolTarget, vaultread.CertificationTarget} {
		raw := `schools: [{id: school, institution: Before, degree: BS, dates: '2024', date_range: {start: 2024-01, end: 2024-12, ongoing: false}, graduation_date: '2025', graduation_month: 2025-01, completed_month: foreign, source: imported, archived: true, extra: [false, null]}]
certifications: [{id: cert, name: Before, credential: Provider, completed: '2025', completed_month: 2025-01, graduation_month: foreign, date_range: {foreign: keep}, source: imported, extra: [false, null]}]
extra: [1, 'on']
`
		normalized, err := normalizeEducation(target, educationPayload(target))
		if err != nil {
			t.Fatal(err)
		}
		collection, _ := target.EducationCollection()
		id := "school"
		if target == vaultread.CertificationTarget {
			id = "cert"
		}
		before := evidenceValue(t, raw)
		after, err := planEducationRecord(target, before, normalized, "update", id, false)
		if err != nil {
			t.Fatal(err)
		}
		if !equalValue(before, evidenceValue(t, raw)) {
			t.Fatal("plan mutated input")
		}
		selected, _ := get(after, collection)
		record := selected.Items[0]
		original, _ := get(before, collection)
		for _, field := range []string{"id", "source", "extra", "archived"} {
			old, present := get(original.Items[0], field)
			value, exists := get(record, field)
			if present != exists || !equalValue(old, value) {
				t.Fatal("lost original", field)
			}
		}
		if target == vaultread.SchoolTarget {
			for _, field := range []string{"date_range", "graduation_month"} {
				if _, ok := get(record, field); ok {
					t.Fatal("stale metadata", field)
				}
			}
			if value, _ := get(record, "completed_month"); value.Text != "foreign" {
				t.Fatal("unowned extension cleared")
			}
		} else {
			if value, _ := get(record, "completed_month"); value.Text != "2026-05" {
				t.Fatal("completed month")
			}
			if value, _ := get(record, "graduation_month"); value.Text != "foreign" {
				t.Fatal("unowned extension cleared")
			}
		}
		for _, archived := range []bool{true, false} {
			plan, err := planEducationRecord(target, before, cataloginput.Value{}, "archive", id, archived)
			if err != nil {
				t.Fatal(err)
			}
			rows, _ := get(plan, collection)
			value, ok := get(rows.Items[0], "archived")
			if ok != archived || ok && !value.Boolean {
				t.Fatal("archive presence")
			}
			for _, field := range []string{"dates", "date_range", "graduation_month", "completed_month", "extra"} {
				old, op := get(original.Items[0], field)
				value, p := get(rows.Items[0], field)
				if op != p || !equalValue(old, value) {
					t.Fatal("archive normalized", field)
				}
			}
		}
	}
}
func TestEducationInputAndServiceInvalidUse(t *testing.T) {
	for _, target := range []vaultread.EditTarget{vaultread.SchoolTarget, vaultread.CertificationTarget} {
		for _, test := range []struct {
			key   string
			value any
			code  string
		}{
			{"\xff", nil, "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
			{strings.Repeat("k", maxPayloadBytes+1), nil, "VAULT_EDITOR_INPUT_LIMIT"},
			{"competency_ids", []any{}, "VAULT_EDITOR_INVALID"},
		} {
			payload := educationPayload(target)
			payload[test.key] = test.value
			if _, err := normalizeEducation(target, payload); err == nil || err.Code != test.code {
				t.Fatalf("input guard %#v", err)
			}
		}
		if _, err := planEducationRecord(target, evidenceValue(t, "[]"), cataloginput.Value{}, "create", "new", false); err == nil {
			t.Fatal("wrong root")
		}
		if _, err := planEducationRecord(target, evidenceValue(t, "{}"), cataloginput.Value{}, "bogus", "new", false); err == nil {
			t.Fatal("wrong operation")
		}
		if _, err := planEducationRecord(target, evidenceValue(t, "{}"), cataloginput.Value{}, "update", "missing", false); err == nil || err.Code != "VAULT_EDITOR_RECORD_NOT_FOUND" {
			t.Fatal("missing record")
		}
		if out, err := New("not-opened").mutateEducation(context.Background(), target, "update", "../invalid", nil, false, ""); err == nil || err.Code != "VAULT_EDITOR_RECORD_NOT_FOUND" || out.Committed {
			t.Fatal("identity before opening")
		}
		if _, err := New("").mutateEducation(nil, target, "update", "\xff", nil, false, ""); err == nil || err.Code != "VAULT_EDITOR_INPUT_UNSUPPORTED" {
			t.Fatal("invalid Unicode identity")
		}
	}
	if _, err := normalizeEducation(99, nil); err == nil {
		t.Fatal("unknown normalization target")
	}
	if _, err := (*Service)(nil).CreateSchool(context.Background(), nil, ""); err == nil || err.Code != "VAULT_EDITOR_UNAVAILABLE" {
		t.Fatal("nil service")
	}
	if _, err := New("").CreateCertification(nil, nil, ""); err == nil || err.Code != "VAULT_EDITOR_UNAVAILABLE" {
		t.Fatal("nil context")
	}
}
