package agentcontext

import (
	"context"
	"encoding/json"
	"testing"

	"careerhub.local/backend/internal/jobs/jobevaluation"
)

type packetCase struct {
	Input         PacketInput    `json:"input"`
	Task          map[string]any `json:"task"`
	DigestFailure *string        `json:"digest_failure"`
}

func basePacketCase(t *testing.T, purpose string) packetCase {
	t.Helper()
	model := baseModelCase(t)
	var card map[string]any
	_ = decodeNumbers(model.Job, &card)
	card["required_skills"] = []string{"Go", "SQL", "Certification"}
	card["education_requirements"] = []string{"Degree"}
	card["status"], card["content_fingerprint"] = "reviewed", "fingerprint"
	card["compensation"] = json.RawMessage(`{"currency":"USD","max":123000.0,"min":9007199254740993}`)
	model.Review.MatchedRequired = []jobevaluation.MatchedRequirement{
		{Category: "skill", Requirement: "Go", EvidenceIDs: []string{"e"}},
		{Category: "skill", Requirement: "SQL", EvidenceIDs: []string{"p"}},
		{Category: "skill", Requirement: "Certification", EvidenceIDs: []string{"c"}},
	}
	var assessment map[string]any
	_ = decodeNumbers(jsonFixture(t, model.Review), &assessment)
	assessment["job_card_path"], assessment["job_status"], assessment["assessment_mode"] = model.JobCardPath, "reviewed", "advisory_only"
	return packetCase{Input: PacketInput{
		Job: jsonFixture(t, card), Assessment: jsonFixture(t, assessment), Sources: model.Sources,
		Profile: "fixture", Channel: "dev", Purpose: purpose, Operation: "context", Persisted: true, RegistryMode: "off",
		ResumeInitialized: true, ResumeLibrary: jsonFixture(t, baseResumeCase().Library),
		Application: json.RawMessage(`{"current_status":"Preparing","resume_selection":{"state":"selected","resume":{"id":"r11","display_name":"Eleven"}}}`),
		Workbench:   json.RawMessage(`{"items":[{"id":"draft_1","kind":"short_answer","exact_prompt":"Why?","response":"PRIVATE_UNRELATED_RESPONSE","limits":{"words":100,"characters":500},"state":"draft"}]}`),
	}}
}

func runNativePacket(t *testing.T, c packetCase) ([]byte, [][]any, *Error) {
	t.Helper()
	var err *Error
	if c.Task != nil {
		c.Input.Task, err = NormalizeTask(c.Task, c.Input.Purpose)
		if err != nil {
			return nil, nil, err
		}
	}
	calls := [][]any{}
	reader := func(ctx context.Context, raw json.RawMessage) (json.RawMessage, *string, *Error) {
		var rows []map[string]any
		if decodeNumbers(raw, &rows) != nil {
			t.Fatal("digest callback input")
		}
		ids := []any{}
		for _, row := range rows {
			ids = append(ids, row["id"])
			row["digest_status"], row["digest_supported"], row["digest_processed_at"], row["private"] = "processed", true, "2026-01-01", "PRIVATE_DIGEST"
		}
		calls = append(calls, ids)
		if c.DigestFailure != nil {
			return nil, c.DigestFailure, nil
		}
		return jsonFixture(t, rows), nil, nil
	}
	raw, err := BuildPacket(context.Background(), c.Input, reader)
	return raw, calls, err
}
