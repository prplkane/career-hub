package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreparationFailureStages(t *testing.T) {
	w := httptest.NewRecorder()
	out := &ApplicationOutcome{Committed: true, Partial: true, Result: json.RawMessage(`{"private":"NEVER_EXPOSE"}`), PreparationStages: &PreparationStages{IntakePhase: "terminal", IntakeComplete: true, PreparationComplete: true}}
	applicationFailure(w, out, "AGENT_PREPARE_CONTEXT_UNAVAILABLE", "agent_prepare")
	var body map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || strings.Contains(w.Body.String(), "NEVER_EXPOSE") {
		t.Fatal("private phase response")
	}
	var stages PreparationStages
	if json.Unmarshal(body["preparation_stages"], &stages) != nil || stages != *out.PreparationStages {
		t.Fatal("lost phase facts")
	}
}
