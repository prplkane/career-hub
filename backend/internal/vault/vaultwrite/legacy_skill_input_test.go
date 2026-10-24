package vaultwrite

import (
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
)

func TestLegacySkillPlansPreserveExtensionsAndInput(t *testing.T) {
	raw := `- {id: skill, skill_family: Before, keywords: [old], related_experience: [exp], archived: true, source: imported, dates: '2024', date_range: {start: 2024-01, end: 2024-12, ongoing: false}, extra: [false, null]}
- {id: other, extra: keep}
`
	before := evidenceValue(t, raw)
	payload := map[string]any{"skill_family": "After"}
	for _, key := range legacySkillFields[1:] {
		payload[key] = []any{}
	}
	normalized, err := normalizeLegacySkill(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"create", "update", "archive"} {
		for _, archived := range []bool{true, false} {
			id := "skill"
			if op == "create" {
				id = "new"
			}
			after, err := planLegacySkill(before, normalized, op, id, archived)
			if err != nil || !equalValue(before, evidenceValue(t, raw)) {
				t.Fatal("plan failed or mutated input", err)
			}
			if !equalValue(before.Items[1], after.Items[1]) {
				t.Fatal("sibling changed")
			}
			if op == "create" {
				if len(after.Items) != 3 || !equalValue(before.Items[0], after.Items[0]) {
					t.Fatal("creation overwrote record")
				}
				if source, _ := get(after.Items[2], "source"); source.Text != "manual" {
					t.Fatal("creation provenance")
				}
				continue
			}
			for _, field := range []string{"id", "source", "dates", "date_range", "extra"} {
				old, _ := get(before.Items[0], field)
				value, _ := get(after.Items[0], field)
				if !equalValue(old, value) {
					t.Fatal("extension lost", field)
				}
			}
			if op == "archive" {
				flag, present := get(after.Items[0], "archived")
				if present != archived || present && !flag.Boolean {
					t.Fatal("archive presence")
				}
				for _, field := range legacySkillFields {
					old, p := get(before.Items[0], field)
					value, q := get(after.Items[0], field)
					if p != q || !equalValue(old, value) {
						t.Fatal("archive normalized field", field)
					}
				}
			} else {
				if flag, _ := get(after.Items[0], "archived"); !flag.Boolean {
					t.Fatal("update restored archive")
				}
				for _, field := range legacySkillFields[1:] {
					value, _ := get(after.Items[0], field)
					if value.Kind != "sequence" || len(value.Items) != 0 {
						t.Fatal("explicit empty list not cleared")
					}
				}
			}
		}
	}
}
func TestLegacySkillInvalidUse(t *testing.T) {
	for _, test := range []struct{ key, code string }{
		{"\xff", "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
		{strings.Repeat("k", maxPayloadBytes+1), "VAULT_EDITOR_INPUT_LIMIT"},
		{"competency_ids", "VAULT_EDITOR_INVALID"},
	} {
		if _, err := normalizeLegacySkill(map[string]any{"skill_family": "New", test.key: nil}); err == nil || err.Code != test.code {
			t.Fatal("input guard", err)
		}
	}
	for _, test := range []struct{ raw, op, code string }{
		{"{}", "create", "VAULT_EDITOR_TARGET_INVALID"},
		{"[]", "bogus", "VAULT_EDITOR_OPERATION_INVALID"},
		{"[]", "update", "VAULT_EDITOR_RECORD_NOT_FOUND"},
	} {
		if _, err := planLegacySkill(evidenceValue(t, test.raw), cataloginput.Value{}, test.op, "absent", false); err == nil || err.Code != test.code {
			t.Fatal("plan guard", err)
		}
	}
	for _, id := range []string{"../invalid", "", ".", ".."} {
		if out, err := New("not-opened").UpdateLegacySkillGroup(context.Background(), id, nil, ""); err == nil || err.Code != "VAULT_EDITOR_RECORD_NOT_FOUND" || out.Committed {
			t.Fatal("identity must fail before root open")
		}
	}
	for _, id := range []string{"\xff", strings.Repeat("x", maxPayloadBytes+1)} {
		if _, err := New("").SetLegacySkillGroupArchived(nil, id, true, ""); err == nil || err.Code != "VAULT_EDITOR_INPUT_UNSUPPORTED" {
			t.Fatal("unsupported identity")
		}
	}
	if _, err := (*Service)(nil).CreateLegacySkillGroup(context.Background(), nil, ""); err == nil || err.Code != "VAULT_EDITOR_UNAVAILABLE" {
		t.Fatal("nil service")
	}
}
