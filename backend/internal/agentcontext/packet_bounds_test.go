package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPacketRefusalsAreAllOrNone(t *testing.T) {
	for _, scenario := range []string{"purpose", "operation", "registry", "foreign_review", "foreign_application", "incomplete_review", "invalid_source_scalar", "invalid_library_scalar", "invalid_receipt", "invalid_application", "task_mismatch", "missing_resume", "invalid_workbench", "oversized", "missing_style"} {
		t.Run(scenario, func(t *testing.T) {
			c := basePacketCase(t, "assessment")
			switch scenario {
			case "purpose":
				c.Input.Purpose = "unknown"
			case "operation":
				c.Input.Operation = "submit"
			case "registry":
				c.Input.RegistryMode = "automatic_write"
			case "foreign_review":
				c.Input.Assessment = bytes.Replace(c.Input.Assessment, []byte(`"job_id":"fixture"`), []byte(`"job_id":"foreign"`), 1)
			case "foreign_application":
				c.Input.Application = json.RawMessage(`{"job_id":"foreign"}`)
			case "incomplete_review":
				c.Input.Assessment = json.RawMessage(`{"job_id":"fixture","job_card_path":"fixture"}`)
			case "invalid_source_scalar":
				c.Input.Sources.Profile = json.RawMessage(`{"current_location":"\ud800"}`)
			case "invalid_library_scalar":
				c.Input.ResumeLibrary = json.RawMessage(`[{"id":"r0","display_name":"\ud800"}]`)
			case "invalid_receipt":
				c.Input.WriteReceipt = json.RawMessage(`[]`)
			case "invalid_application":
				c.Input.Application = json.RawMessage(`[]`)
			case "task_mismatch":
				c.Input.Task, _ = NormalizeTask(map[string]any{"purpose": "resume_delta"}, "resume_delta")
			case "missing_resume":
				c.Input.Purpose = "resume_delta"
				c.Input.Task, _ = NormalizeTask(map[string]any{"purpose": "resume_delta", "resume_id": "missing"}, "resume_delta")
			case "invalid_workbench":
				c.Input.Purpose, c.Input.Workbench = "application_text", json.RawMessage(`{"items":"not a list"}`)
			case "oversized":
				c.Input.Profile = strings.Repeat("x", contextJSONLimit+1)
			case "missing_style":
				c.Input.Sources.StyleRules = json.RawMessage(`{}`)
			}
			calls := 0
			raw, err := BuildPacket(context.Background(), c.Input, func(context.Context, json.RawMessage) (json.RawMessage, *string, *Error) {
				calls++
				return nil, nil, failure("UNEXPECTED_CALLBACK")
			})
			if err == nil || raw != nil || calls != 0 {
				t.Fatalf("expected all-or-none refusal without digest call: error=%v output=%d calls=%d", err, len(raw), calls)
			}
		})
	}
	c := basePacketCase(t, "resume_delta")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if raw, err := BuildPacket(ctx, c.Input, nil); err == nil || raw != nil {
		t.Fatal("pre-canceled packet returned output")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if raw, err := BuildPacket(ctx, c.Input, func(_ context.Context, selected json.RawMessage) (json.RawMessage, *string, *Error) {
		cancel()
		return selected, nil, nil
	}); err == nil || raw != nil {
		t.Fatal("late cancellation returned partial packet")
	}
	if raw, err := BuildPacket(context.Background(), c.Input, func(context.Context, json.RawMessage) (json.RawMessage, *string, *Error) {
		return json.RawMessage(`[{"id":"foreign","display_name":"wrong"}]`), nil, nil
	}); err == nil || raw != nil {
		t.Fatal("foreign digest selection returned a packet")
	}
}

func TestPacketReuseAndPurposePrivacy(t *testing.T) {
	snapshotID := ""
	for _, purpose := range []string{"assessment", "application_text", "resume_delta"} {
		c := basePacketCase(t, purpose)
		before := jsonFixture(t, c.Input)
		raw, calls, err := runNativePacket(t, c)
		if err != nil {
			t.Fatal(err.Code)
		}
		if !bytes.Equal(before, jsonFixture(t, c.Input)) {
			t.Fatal("borrowed source input mutated")
		}
		again, _, err := runNativePacket(t, c)
		if err != nil || !bytes.Equal(raw, again) {
			t.Fatal("repeat packet changed")
		}
		var p map[string]any
		_ = decodeNumbers(raw, &p)
		id := p["context_snapshot"].(map[string]any)["id"].(string)
		if snapshotID != "" && snapshotID != id {
			t.Fatal("snapshot depends on purpose")
		}
		snapshotID = id
		if (len(calls) > 0) != (purpose == "resume_delta") {
			t.Fatal("non-resume packet read digest history")
		}
		_, writing := p["writing"]
		_, resume := p["resume_delta"]
		if writing != (purpose == "application_text") || resume != (purpose == "resume_delta") {
			t.Fatal("unrelated purpose section leaked")
		}
		if strings.Contains(string(raw), "PRIVATE_") {
			t.Fatal("unrelated private data leaked")
		}
		if writing {
			items := p["writing"].(map[string]any)["workbench"].(map[string]any)["items"].([]any)
			for _, v := range items {
				if _, ok := v.(map[string]any)["response"]; ok {
					t.Fatal("unrelated response present")
				}
			}
		}
		metrics := p["context_metrics"].(map[string]any)
		value, err := decodeOrderedContextJSON(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		value.Entries = value.Entries[:len(value.Entries)-1]
		beforeMetrics, err := encodeOrderedContextJSON(context.Background(), value, false)
		if err != nil || packetHash(beforeMetrics) != metrics["packet_fingerprint"] {
			t.Fatal("fingerprint includes metrics or different bytes")
		}
	}
}

func TestPacketIgnoresOutOfPurposeReadModels(t *testing.T) {
	c := basePacketCase(t, "assessment")
	c.Input.Workbench = json.RawMessage(`{"items":"not a workbench"}`)
	if raw, _, err := runNativePacket(t, c); err != nil || raw == nil {
		t.Fatal("assessment accessed workbench")
	}
	c.Input.Persisted = false
	c.Input.Application = json.RawMessage(`"not an application"`)
	if raw, _, err := runNativePacket(t, c); err != nil || raw == nil {
		t.Fatal("ephemeral packet accessed application")
	}
	c.Input.ResumeInitialized = false
	c.Input.ResumeLibrary = json.RawMessage(`"\ud800"`)
	if raw, _, err := runNativePacket(t, c); err != nil || raw == nil {
		t.Fatal("uninitialized library bypassed early warning")
	}
}
