package agentcontext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTaskIsolationAndPrivacy(t *testing.T) {
	input := map[string]any{"purpose": "application_text", "kind": "short_answer", "exact_prompt": "PRIVATE_TASK", "limits": map[string]any{"words": json.Number("100")}}
	task, p := NormalizeTask(input, "application_text")
	if p != nil {
		t.Fatal(p)
	}
	before, p := TaskPayload("application_text", task)
	if p != nil {
		t.Fatal(p)
	}
	input["exact_prompt"] = "changed"
	input["limits"].(map[string]any)["words"] = json.Number("1")
	after, p := TaskPayload("application_text", task)
	if p != nil || !bytes.Equal(before, after) {
		t.Fatal("caller mutated normalized task")
	}
	if _, p := TaskPayload("resume_delta", task); p == nil || strings.Contains(fmt.Sprintf("%+v", p), "PRIVATE_TASK") {
		t.Fatal("purpose mismatch/privacy")
	}
	if _, p := NormalizeTask(map[string]any{"purpose": "application_text", "kind": "short_answer", "exact_prompt": string([]byte{0xff})}, "application_text"); p == nil {
		t.Fatal("invalid scalar accepted")
	}
	if _, p := NormalizeTask(nil, "assessment"); p == nil {
		t.Fatal("supplied null request accepted")
	}
	if _, p := TaskPayload("application_text", &TaskRequest{}); p == nil {
		t.Fatal("unvalidated task")
	}
}

func TestWorkbenchExplicitUnsupportedAndDetachedProjection(t *testing.T) {
	for _, response := range []any{json.Number("2"), true, []any{"PRIVATE"}, map[string]any{"private": "text"}, strings.Repeat("x", (2<<20)+1)} {
		if _, p := WorkbenchContext(map[string]any{"items": []any{map[string]any{"id": "a", "kind": "other", "response": response}}}, nil); p == nil || p.Code != "AGENT_CONTEXT_WORKBENCH_UNSUPPORTED" {
			t.Fatal("unsupported representation hidden")
		}
	}
	if _, p := WorkbenchContext(map[string]any{"items": make([]any, 1001)}, nil); p == nil {
		t.Fatal("item bound")
	}
	if _, p := WorkbenchContext(nil, &TaskRequest{}); p == nil {
		t.Fatal("unvalidated task")
	}
	refs := []any{"exp_one"}
	store := map[string]any{"items": []any{map[string]any{"id": "a", "kind": "other", "exact_prompt": "", "response": "PRIVATE_RESPONSE", "evidence_refs": refs, "state": "draft"}}}
	raw, p := WorkbenchContext(store, nil)
	if p != nil {
		t.Fatal(p)
	}
	refs[0] = "changed"
	if bytes.Contains(raw, []byte("changed")) || bytes.Contains(raw, []byte("PRIVATE_RESPONSE")) {
		t.Fatal("raw response or mutable aliases exposed")
	}
}
