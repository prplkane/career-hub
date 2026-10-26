package agentcontext

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPreparationResponseReceiptAndIdentity(t *testing.T) {
	ctx := context.Background()
	d, resources := savedInputFixture()
	evidence, problem := ReadSavedEvidence(ctx, d, resources, "synthetic", savedInputDate)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	for _, purpose := range []string{"assessment", "application_text", "resume_delta"} {
		for _, change := range []string{"valid", "result", "changed", "created", "both", "missing", "applied", "status", "application", "wrong-job", "wrong-purpose", "read-only", "source-reused", "source-created-mismatch"} {
			t.Run(purpose+"/"+change, func(t *testing.T) {
				receipt := map[string]any{"result": "prepared", "changed": true, "job_created": false, "job_reused": true, "status_before": "captured", "status_after": "preparing", "applied_recorded": false}
				operation, application, job, requestPurpose := "prepare", `{"current_status":"preparing"}`, "synthetic", purpose
				var source *SourceRequest
				switch change {
				case "result":
					receipt["result"] = "no_change_needed"
				case "changed":
					receipt["changed"] = false
				case "created":
					receipt["job_created"], receipt["job_reused"] = true, false
				case "both":
					receipt["job_reused"] = false
				case "missing":
					delete(receipt, "status_before")
				case "applied":
					receipt["applied_recorded"] = true
				case "status":
					receipt["status_after"] = "applied"
				case "application":
					application = `{"current_status":"captured"}`
				case "wrong-job":
					job = "another"
				case "wrong-purpose":
					requestPurpose = "another"
				case "read-only":
					operation = "analyze"
				case "source-reused", "source-created-mismatch":
					job, source = "", &SourceRequest{Text: "Different incoming text for an existing posting"}
					if change == "source-created-mismatch" {
						receipt["job_created"], receipt["job_reused"], receipt["status_before"] = true, false, nil
					}
				}
				rawReceipt, _ := json.Marshal(receipt)
				packet, e := BuildPacket(ctx, PacketInput{Job: evidence.Job, Assessment: evidence.Assessment, Sources: evidence.Sources, RegistryGroups: evidence.RegistryGroups, Purpose: purpose, Operation: operation, RegistryMode: "off", Profile: "explicit", Channel: "dev", Persisted: true, WriteReceipt: rawReceipt, Application: json.RawMessage(application)}, nil)
				if e != nil {
					t.Fatal(e.Code)
				}
				e = ValidatePreparationResponse(ctx, packet, job, requestPurpose, "off", nil, source)
				wantValid := change == "valid" || change == "source-reused"
				if (e == nil) != wantValid {
					t.Fatalf("receipt validity = %v, want %v", e == nil, wantValid)
				}
				if ValidateSavedResponse(ctx, packet, "synthetic", purpose, "off", nil) == nil {
					t.Fatal("preparation accepted as read-only")
				}
			})
		}
	}
}

func TestPreparationReceiptStatusSemantics(t *testing.T) {
	for _, before := range []any{nil, "captured", "reviewed", "preparing", "applied", "interview", "offer", "rejected", "withdrawn", "closed"} {
		after := "preparing"
		if before == "applied" || before == "interview" || before == "offer" {
			after = before.(string)
		}
		changed := before == nil || before != after
		result := "no_change_needed"
		if changed {
			result = "prepared"
		}
		raw, _ := json.Marshal(map[string]any{"result": result, "changed": changed, "job_created": false, "job_reused": true, "status_before": before, "status_after": after, "applied_recorded": false})
		receipt, _ := decodeOrderedContextJSON(context.Background(), raw)
		application := packetObject(packetField("current_status", packetString(after)))
		if !validPreparationReceipt(receipt, application, true) {
			t.Fatalf("status refused: %v", before)
		}
	}
}
