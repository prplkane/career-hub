package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskTransportScalarAndCharacterBounds(t *testing.T) {
	ctx := context.Background()
	good := `{"purpose":"application_text","kind":"short_answer","exact_prompt":"` + strings.Repeat("<", 4000) + `","limits":{"characters":12345678901234567890}}`
	task, e := ParseTaskJSON(ctx, []byte(good), "application_text")
	if e != nil {
		t.Fatal(e.Code)
	}
	wire, e := TaskWireJSON(task)
	if e != nil || bytes.Contains(wire, []byte(`\u003c`)) {
		t.Fatal("wire expanded bounded prompt")
	}
	if _, e := ParseTaskJSON(ctx, wire, "application_text"); e != nil {
		t.Fatal(e.Code)
	}
	for _, raw := range []string{"", `null`, `[]`, `{"purpose":"application_text","kind":"short_answer","exact_prompt":"\ud800"}`, `{"purpose":"assessment","resume_id":"r1"}`, `{"purpose":"assessment"}` + strings.Repeat(" ", 16000)} {
		if _, e := ParseTaskJSON(ctx, []byte(raw), "assessment"); e == nil {
			t.Fatal("invalid task accepted")
		}
	}
}

func TestSavedResponseBindingAndFingerprint(t *testing.T) {
	for _, purpose := range []string{"assessment", "application_text", "resume_delta"} {
		d, r := savedInputFixture()
		evidence, e := ReadSavedEvidence(context.Background(), d, r, "synthetic", savedInputDate)
		if e != nil {
			t.Fatal(e.Code)
		}
		operation := "context"
		if purpose == "assessment" {
			operation = "analyze"
		}
		packet, e := BuildPacket(context.Background(), PacketInput{Job: evidence.Job, Assessment: evidence.Assessment, Sources: evidence.Sources, RegistryGroups: evidence.RegistryGroups, Purpose: purpose, Operation: operation, RegistryMode: "off", Profile: "explicit", Channel: "dev", Persisted: true, WriteReceipt: json.RawMessage(`{"result":"read_only","changed":false}`)}, nil)
		if e != nil {
			t.Fatal(e.Code)
		}
		if e := ValidateSavedResponse(context.Background(), packet, "synthetic", purpose, "off", nil); e != nil {
			t.Fatal("real packet refused", e.Code)
		}
		for _, mode := range []string{"job", "purpose", "registry", "text", "metrics", "task"} {
			raw := bytes.Clone(packet)
			job, p, registry := "synthetic", purpose, "off"
			var task *TaskRequest
			switch mode {
			case "job":
				job = "foreign"
			case "purpose":
				p = "wrong"
			case "registry":
				registry = "exact-auto"
			case "text":
				raw = bytes.Replace(raw, []byte("Synthetic"), []byte("Different"), 1)
			case "metrics":
				raw = bytes.Replace(raw, []byte(`"packet_fingerprint":"sha256:`), []byte(`"packet_fingerprint":"sha255:`), 1)
			case "task":
				task, e = NormalizeTask(map[string]any{"purpose": purpose}, purpose)
				if e != nil {
					continue
				}
			}
			if ValidateSavedResponse(context.Background(), raw, job, p, registry, task) == nil {
				t.Fatal("binding accepted", mode)
			}
		}
	}
}
