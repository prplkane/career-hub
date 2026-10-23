package applicationtexts

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
)

func TestWorkbenchTransitionsAndHistory(t *testing.T) {
	ctx := context.Background()
	draft, e := NormalizeDraft(fixture())
	if e != nil {
		t.Fatal(e)
	}
	fp := strings.Repeat("a", 64)
	raw, result, e := updateWorkbench(ctx, nil, false, "job_one", fp, draft)
	if e != nil || result != "created" {
		t.Fatal("create", e)
	}
	unchanged, result, e := updateWorkbench(ctx, raw, true, "job_one", fp, draft)
	if e != nil || result != "no_change_needed" || unchanged != nil {
		t.Fatal("no-op rewrites bytes", e)
	}
	draft.Response = "A revised response."
	updated, result, e := updateWorkbench(ctx, raw, true, "job_one", fp, draft)
	if e != nil || result != "updated" || bytes.Equal(raw, updated) {
		t.Fatal("update", e)
	}
	for _, state := range []string{"user_approved", "submitted"} {
		protected := bytes.Replace(updated, []byte(`"draft"`), []byte(`"`+state+`"`), 1)
		if bytes.Equal(protected, updated) {
			t.Fatal("protection fixture unchanged")
		}
		if _, _, e := updateWorkbench(ctx, protected, true, "job_one", fp, draft); e == nil || e.Code != "APPLICATION_TEXT_HISTORY_IMMUTABLE" {
			t.Fatal("overwrote history", e)
		}
		next := draft
		next.ID = "answer_2"
		after, result, e := updateWorkbench(ctx, protected, true, "job_one", fp, next)
		if e != nil || result != "created" {
			t.Fatal("new revision", e)
		}
		v, _ := cataloginput.ParseYAML(after)
		items := field(v, "items").Items
		if len(items) != 2 || !stringIs(field(items[0], "state"), state) {
			t.Fatal("history changed")
		}
	}
}

func TestWorkbenchPreservesTypedMetadataAndOrderIndependentNoop(t *testing.T) {
	d, _ := NormalizeDraft(fixture())
	raw, _, e := updateWorkbench(context.Background(), nil, false, "job", "fp", d)
	if e != nil {
		t.Fatal(e)
	}
	metadata := []byte("extra:\n  date: 2026-09-26\n  time: 2026-09-26T12:30:00Z\n  bytes: !!binary YWJj\n  flag: true\n  huge: 123456789012345678901234567890\n  real: 1.5\n  infinity: .inf\n  note: 'private fixture metadata'\n")
	raw = append(raw, metadata...)
	before, _ := cataloginput.ParseYAML(raw)
	// The reference permits arbitrary historical field order, not only the
	// current encoder's order. Reorder the existing draft, preserving semantics.
	for i := range before.Entries {
		if stringIs(before.Entries[i].Key, "items") {
			entries := before.Entries[i].Value.Items[0].Entries
			for a, b := 0, len(entries)-1; a < b; a, b = a+1, b-1 {
				entries[a], entries[b] = entries[b], entries[a]
			}
		}
	}
	raw, e = encodeStore(context.Background(), before)
	if e != nil {
		t.Fatal(e)
	}
	out, result, e := updateWorkbench(context.Background(), raw, true, "job", "fp", d)
	if e != nil || result != "no_change_needed" || out != nil {
		t.Fatal("field order caused a write", e)
	}
	d.Response = "Updated."
	out, _, e = updateWorkbench(context.Background(), raw, true, "job", "fp", d)
	if e != nil {
		t.Fatal(e)
	}
	after, pe := cataloginput.ParseYAML(out)
	if pe != nil || !valueEqual(field(before, "extra"), field(after, "extra")) || !valueEqual(field(before, "observations"), field(after, "observations")) {
		t.Fatal("unrelated metadata changed")
	}
}

func TestWorkbenchRefusalsAndNumericEquality(t *testing.T) {
	d, _ := NormalizeDraft(fixture())
	raw, _, _ := updateWorkbench(context.Background(), nil, false, "job", "fp", d)
	for _, stored := range [][]byte{[]byte("null"), []byte("bad: ["), bytes.Replace(raw, []byte(`"fp"`), []byte(`"stale"`), 1), bytes.Replace(raw, []byte(`"job"`), []byte(`"other"`), 1)} {
		if _, _, e := updateWorkbench(context.Background(), stored, true, "job", "fp", d); e == nil {
			t.Fatal("invalid store admitted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e := updateWorkbench(ctx, raw, true, "job", "fp", d); e == nil {
		t.Fatal("canceled update")
	}
	if !valueEqual(cataloginput.Value{Kind: "bool", Boolean: true}, cataloginput.Value{Kind: "int", Text: "1"}) {
		t.Fatal("Python bool/int equality")
	}
	if valueEqual(cataloginput.Value{Kind: "float", Text: "1e+40"}, cataloginput.Value{Kind: "int", Text: "10000000000000000000000000000000000000000"}) {
		t.Fatal("float decimal spelling treated as exact integer")
	}
	// Enforce store limits without dropping history or silently truncating.
	v, _ := cataloginput.ParseYAML(raw)
	for i := range v.Entries {
		if stringIs(v.Entries[i].Key, "items") {
			for n := 1; n < MaxItems; n++ {
				encoded, _ := json.Marshal(map[string]any{"id": "other_" + strconv.Itoa(n), "kind": "short_answer", "state": "draft"})
				parsed, _ := cataloginput.ParseYAML(encoded)
				v.Entries[i].Value.Items = append(v.Entries[i].Value.Items, parsed)
			}
		}
	}
	oversize, _ := encodeStore(context.Background(), v)
	d.ID = "additional"
	if out, _, e := updateWorkbench(context.Background(), oversize, true, "job", "fp", d); e == nil || e.Code != "APPLICATION_TEXT_STORE_LIMIT" || out != nil {
		t.Fatal("full history truncated or overwritten")
	}
}
