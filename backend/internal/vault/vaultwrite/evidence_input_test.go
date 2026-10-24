package vaultwrite

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/vault/vaultread"
)

func evidencePayload(target vaultread.EditTarget) map[string]any {
	if target == vaultread.ExperienceTarget {
		return map[string]any{"company": "Synthetic", "role": "Engineer", "dates": "Jan 2024 - Present",
			"category": "technical", "technologies": []any{"Python"}, "proof_points": []any{"Synthetic proof"}}
	}
	return map[string]any{"name": "Synthetic", "summary": "Synthetic project", "category": "technical",
		"technologies": []any{"Python"}, "related_experience": []any{}, "proof_points": []any{}}
}

func evidenceValue(t *testing.T, raw string) cataloginput.Value {
	t.Helper()
	value, e := cataloginput.ParseYAML([]byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	return value
}

func TestEvidenceRecordPlansPreserveExtensionsAndClearStaleDates(t *testing.T) {
	for _, target := range []vaultread.EditTarget{vaultread.ExperienceTarget, vaultread.ProjectTarget} {
		payload := evidencePayload(target)
		payload["dates"] = "uncommon new wording"
		normalized, e := normalizeEvidence(target, payload)
		if e != nil {
			t.Fatal(e)
		}
		raw := `- id: old
  source: import
  archived: true
  dates: Jan 2020 - Dec 2021
  date_range: {start: 2020-01, end: 2021-12, ongoing: false}
  extension: {enabled: false, label: on}
  competency_ids: [old_skill]
  technologies: [Old Tool]
`
		original := evidenceValue(t, raw)
		plan, e := planEvidenceRecord(target, original, normalized, "update", "old", false)
		if e != nil || plan.Index != 0 || len(plan.Candidate.Items) != 1 {
			t.Fatal("update record selection")
		}
		record := plan.Candidate.Items[0]
		if _, exists := get(record, "date_range"); exists {
			t.Fatal("source stale-derived-range bug retained")
		}
		for _, key := range []string{"source", "extension", "archived", "competency_ids"} {
			before, _ := get(evidenceValue(t, raw).Items[0], key)
			after, _ := get(record, key)
			if !equalValue(before, after) {
				t.Fatalf("%s extension lost", key)
			}
		}
		if raw, e := encode(context.Background(), plan.Candidate); e != nil || len(raw) == 0 {
			t.Fatal("candidate not serializable")
		}
		if target == vaultread.ProjectTarget {
			delete(payload, "dates")
			normalized, e = normalizeEvidence(target, payload)
			if e != nil {
				t.Fatal(e)
			}
			cleared, e := planEvidenceRecord(target, evidenceValue(t, raw), normalized, "update", "old", false)
			if e != nil {
				t.Fatal(e)
			}
			dates, _ := get(cleared.Candidate.Items[0], "dates")
			if dates.Text != "" {
				t.Fatal("optional dates not cleared")
			}
			if _, exists := get(cleared.Candidate.Items[0], "date_range"); exists {
				t.Fatal("optional clear kept range")
			}
		}
		created, e := planEvidenceRecord(target, evidenceValue(t, "[]"), normalized, "create", "new", false)
		if e != nil || created.Index != 0 {
			t.Fatal("create")
		}
		record = created.Candidate.Items[0]
		source, _ := get(record, "source")
		if source.Text != "manual" || record.Entries[0].Key.Text != "id" {
			t.Fatal("manual origin/order")
		}
		for _, archived := range []bool{false, true} {
			plan, e := planEvidenceRecord(target, evidenceValue(t, raw), cataloginput.Value{}, "archive", "old", archived)
			if e != nil {
				t.Fatal(e)
			}
			record := plan.Candidate.Items[0]
			value, present := get(record, "archived")
			if archived != present || (present && !value.Boolean) {
				t.Fatal("archive/restore")
			}
			for _, key := range []string{"dates", "date_range", "technologies", "competency_ids", "extension"} {
				before, _ := get(evidenceValue(t, raw).Items[0], key)
				after, _ := get(record, key)
				if !equalValue(before, after) {
					t.Fatalf("archive changed %s", key)
				}
			}
		}
	}
}

func TestEvidenceInputBoundsAndInvalidPlanUse(t *testing.T) {
	for _, target := range []vaultread.EditTarget{vaultread.ExperienceTarget, vaultread.ProjectTarget} {
		for _, tc := range []struct {
			key   string
			value any
			code  string
		}{
			{"dates", strings.Repeat(" ", maxPayloadBytes+1), "VAULT_EDITOR_INPUT_LIMIT"},
			{"dates", "\xff", "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
			{"dates", struct{}{}, "VAULT_EDITOR_INPUT_UNSUPPORTED"},
			{"date_range", map[string]any{"\xff": 1}, "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
			{"date_range", map[string]any{strings.Repeat("k", maxPayloadBytes+1): nil}, "VAULT_EDITOR_INPUT_LIMIT"},
			{"date_range", map[string]any{"start": struct{}{}}, "VAULT_EDITOR_INPUT_UNSUPPORTED"},
			{"\xff", nil, "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
			{strings.Repeat("k", maxPayloadBytes+1), nil, "VAULT_EDITOR_INPUT_LIMIT"},
		} {
			payload := evidencePayload(target)
			payload[tc.key] = tc.value
			if _, e := normalizeEvidence(target, payload); e == nil || e.Code != tc.code {
				t.Fatal("bounded input failed", tc.code)
			}
		}
		huge := map[string]any{}
		for i := 0; i < 1001; i++ {
			huge[fmt.Sprint(i)] = nil
		}
		if _, e := normalizeEvidence(target, huge); e == nil || e.Code != "VAULT_EDITOR_INPUT_LIMIT" {
			t.Fatal("payload key count")
		}
		payload := evidencePayload(target)
		payload["date_range"] = huge
		if _, e := normalizeEvidence(target, payload); e == nil || e.Code != "VAULT_EDITOR_INPUT_LIMIT" {
			t.Fatal("range key count")
		}
		original := evidenceValue(t, "[false, {id: other}]")
		if _, e := planEvidenceRecord(target, original, cataloginput.Value{}, "update", "missing", false); e == nil || e.SourceClass != "VaultRecordNotFound" {
			t.Fatal("missing record")
		}
		if _, e := planEvidenceRecord(target, original, cataloginput.Value{}, "unknown", "missing", false); e == nil || e.Code != "VAULT_EDITOR_OPERATION_INVALID" {
			t.Fatal("operation guard")
		}
		if e := evidenceMissing(target, nil); e.SourceClass != "VaultRecordNotFound" {
			t.Fatal("identity failure")
		}
	}
	if _, e := normalizeEvidence(99, nil); e == nil {
		t.Fatal("unbounded normalization target")
	}
	if _, e := allocateEvidenceID(99, cataloginput.Value{}, nil); e == nil {
		t.Fatal("unbounded allocation target")
	}
	if _, e := planEvidenceRecord(99, cataloginput.Value{}, cataloginput.Value{}, "create", "x", false); e == nil {
		t.Fatal("unbounded record target")
	}
	if e := evidenceMissing(99, nil); e.Code != "VAULT_EDITOR_TARGET_INVALID" {
		t.Fatal("unbounded missing target")
	}
	if _, e := allocateEvidenceID(vaultread.ProjectTarget, evidenceValue(t, "name: synthetic"), nil); e != nil {
		t.Fatal("empty identity set")
	}
	unicode := cataloginput.Value{Kind: "mapping"}
	set(&unicode, "name", textValue("\xff"))
	if _, e := allocateEvidenceID(vaultread.ProjectTarget, unicode, nil); e == nil {
		t.Fatal("unsupported Unicode slug")
	}
}
