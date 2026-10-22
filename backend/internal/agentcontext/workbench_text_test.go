package agentcontext

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkbenchStoredTextBounds(t *testing.T) {
	ctx := context.Background()
	for _, v := range []packetValue{
		{Kind: "date", Text: "2026-01-01"}, {Kind: "bytes", Bytes: []byte("private")},
		{Kind: "int", Text: "01"}, {Kind: "float", Text: "1e999"}, {Kind: "string", Text: string([]byte{0xff})},
		packetString(strings.Repeat("x", (2<<20)+1)),
		packetList(packetString(strings.Repeat("\x00", 600000))),
		packetObject(packetEntry{Key: packetInt(1), Value: packetNull()}),
	} {
		text, err := workbenchStoredText(ctx, v)
		if err == nil || text != "" {
			t.Fatal("unsupported repr produced partial text")
		}
	}
	cyclic := packetList(packetNull())
	cyclic.Items[0] = cyclic
	if text, err := workbenchStoredText(ctx, cyclic); err == nil || text != "" {
		t.Fatal("cycle accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if text, err := workbenchStoredText(canceled, packetBool(true)); err == nil || text != "" {
		t.Fatal("cancellation ignored")
	}
	if raw, err := WorkbenchContextJSON(canceled, nil, nil); err == nil || raw != nil {
		t.Fatal("absent workbench ignored cancellation")
	}
	if raw, err := WorkbenchContextJSON(ctx, json.RawMessage(`{"items":[{"response":"\ud800"}]}`), nil); err == nil || raw != nil {
		t.Fatal("invalid scalar replaced")
	}
}

func TestWorkbenchLegacyConversionRemainsLazy(t *testing.T) {
	// Repr expansion is refused if actually projected. An older unrelated draft
	// must not be stringified just because its bytes were in the admitted store.
	// Python repr uses four-character \x00, not JSON's six-character \u0000.
	oversized := `["` + strings.Repeat(`\u0000`, 600000) + `"]`
	raw := json.RawMessage(`{"items":[{"id":"old","kind":"other","exact_prompt":"Other","response":` + oversized + `,"state":"draft"},{"id":"a","kind":"other","exact_prompt":"A","state":"draft"},{"id":"b","kind":"other","exact_prompt":"B","state":"draft"},{"id":"c","kind":"short_answer","exact_prompt":123,"response":{"z":true,"a":false},"state":"draft"}]}`)
	task, err := NormalizeTask(map[string]any{"purpose": "application_text", "kind": "short_answer", "exact_prompt": "123"}, "application_text")
	if err != nil {
		t.Fatal(err)
	}
	for _, intent := range []*TaskRequest{nil, task} {
		out, err := WorkbenchContextJSON(context.Background(), raw, intent)
		if err != nil || len(out) == 0 {
			t.Fatal("unselected old response was stringified")
		}
	}
	old, err := NormalizeTask(map[string]any{"purpose": "application_text", "kind": "other", "exact_prompt": "Other", "workbench_item_id": "old"}, "application_text")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := WorkbenchContextJSON(context.Background(), raw, old); err == nil || out != nil {
		t.Fatal("selected oversized repr accepted")
	}
}
