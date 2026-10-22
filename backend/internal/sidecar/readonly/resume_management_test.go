package readonly

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/intakeread"
)

func TestManagementPythonUTCUsesObservedModification(t *testing.T) {
	when := time.Unix(1700000000, 123456000)
	if got := pythonUTC(when); got != "2023-11-14T22:13:20.123456+00:00" {
		t.Fatal(got)
	}
	if got := pythonUTC(time.Unix(1700000000, 0)); got != "2023-11-14T22:13:20+00:00" {
		t.Fatal(got)
	}
	if got := pythonUTC(time.Unix(1700000000, 123456700)); got != "2023-11-14T22:13:20.123457+00:00" {
		t.Fatal(got)
	}
}

func TestManagementInvalidIdentityIsNotUsable(t *testing.T) {
	if got, ok := resumeRowID(applications.ResumeEntry{}); ok || got != "" {
		t.Fatal("missing ID was accepted")
	}
}

func TestManagementApprovalSourceMismatchIsProvisionalSafetyDelta(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "migration_intake_approval_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Base map[string]json.RawMessage `json:"base"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	approval, err := json.Marshal(fixture.Base)
	if err != nil {
		t.Fatal(err)
	}
	input := intakeread.WorkflowInput{ApprovalPresent: true, ApprovalRaw: approval}
	if got := intakeread.ReviewWorkflow(input).Status; got != "saved_to_vault" {
		t.Fatalf("source-shaped pure workflow: %s", got)
	}
	if got := approvalWorkflow("src_other_fictional_1234567890", input); got != "review_error" {
		t.Fatalf("mismatch must fail closed: %s", got)
	}
	if got := approvalWorkflow("src_fictional_1234567890", input); got != "saved_to_vault" {
		t.Fatalf("matching approval: %s", got)
	}
}
