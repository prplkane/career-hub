package applicationtexts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func fixture() map[string]any {
	return map[string]any{"id": "answer_1", "kind": "short_answer", "source": "Employer form", "exact_prompt": "Why this role?", "response": "I build reliable services."}
}

func TestDraftNormalizationAndIsolation(t *testing.T) {
	p := fixture()
	p["source"] = "\u001cEmployer form\u001f"
	p["evidence_refs"] = []any{" proof ", "proof", "Other"}
	p["limits"] = map[string]any{"characters": json.Number("9999999999999999999999999999999999")}
	before, _ := json.Marshal(p)
	d, err := NormalizeDraft(p)
	if err != nil || d.Source != "Employer form" || d.State != "draft" || !reflect.DeepEqual(d.EvidenceRefs, []string{"proof", "Other"}) || d.ManualChecks == nil {
		t.Fatal("normalization", err)
	}
	d.EvidenceRefs[0] = "changed"
	after, _ := json.Marshal(p)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated caller input")
	}
	for _, value := range []any{d, invalid()} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			if strings.Contains(fmt.Sprintf(format, value), "Employer form") {
				t.Fatal("private draft formatting")
			}
		}
	}
}

func TestDraftLimitsAndRefusal(t *testing.T) {
	for _, p := range []map[string]any{nil, {}, {"state": "submitted"}} {
		if _, err := NormalizeDraft(p); err == nil {
			t.Fatal("invalid payload accepted")
		}
	}
	for _, value := range []string{"\xff", strings.Repeat(" ", MaxInputBytes+1)} {
		p := fixture()
		p["response"] = value
		if _, err := NormalizeDraft(p); err == nil {
			t.Fatal("unsupported text accepted")
		}
	}
	for _, n := range []json.Number{"01", "1.0", "1e3", json.Number(strings.Repeat("9", 4097))} {
		p := fixture()
		p["limits"] = map[string]any{"words": n}
		if _, err := NormalizeDraft(p); err == nil {
			t.Fatal("unsupported numeric limit accepted")
		}
	}
	for _, count := range []int{1, 2, 3} {
		p := fixture()
		p["response"] = "😀\u001cx"
		p["limits"] = map[string]any{"words": count}
		_, err := NormalizeDraft(p)
		if (err == nil) != (count >= 2) {
			t.Fatal("Python whitespace word count")
		}
	}
}
