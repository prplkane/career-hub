package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/startup"
)

// Fictional start roots in the native path style; start records reject a
// Windows drive path on POSIX, so the portable suite needs the native form.
var fixtureDataRoot, fixtureResourceRoot = `C:\data`, `C:\resources`

func init() {
	if runtime.GOOS != "windows" {
		fixtureDataRoot, fixtureResourceRoot = "/data", "/resources"
	}
}

type fakeRecoveryWorkspace struct {
	plan          *applicationwrite.NewJobRecoveryPlan
	planError     error
	advanceError  error
	advancedPlan  string
	advancedMonth assessmentcontext.Month
}

func (f *fakeRecoveryWorkspace) Plan(context.Context) (*applicationwrite.NewJobRecoveryPlan, error) {
	return f.plan, f.planError
}

func (f *fakeRecoveryWorkspace) Advance(_ context.Context, plan string, month assessmentcontext.Month) (*applicationwrite.NewJobRecoveryStepOutcome, error) {
	f.advancedPlan, f.advancedMonth = plan, month
	if f.advanceError != nil {
		return nil, f.advanceError
	}
	return &applicationwrite.NewJobRecoveryStepOutcome{Plan: f.plan, Step: f.plan.NextStep}, nil
}

func testRecoveryPlan(step string) *applicationwrite.NewJobRecoveryPlan {
	return &applicationwrite.NewJobRecoveryPlan{
		Version: 1, PlanID: "sha256:" + strings.Repeat("a", 64), OperationID: "private_operation",
		JobID: "fictional_job", InitialStatus: "applied", FileState: "complete", LedgerState: "assessed",
		ReceiptState: "missing", Disposition: "advance", NextStep: step,
		ReasonCode: "JOB_INTAKE_RECOVERY_STEP_AVAILABLE", CanAdvance: true,
	}
}

func recoveryRequest(command string, confirm string) string {
	payload := map[string]any{"command": command, "protocol_version": 1, "resource_root": fixtureResourceRoot,
		"data_root": fixtureDataRoot, "channel": "dev"}
	if confirm != "" {
		payload["confirm_plan"] = confirm
	}
	raw, _ := json.Marshal(payload)
	return string(raw) + "\n"
}

func decodeOutput(t *testing.T, output *bytes.Buffer) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestRunRecoveryPlanIsContentMinimized(t *testing.T) {
	t.Parallel()
	workspace := &fakeRecoveryWorkspace{plan: testRecoveryPlan("applied")}
	var output bytes.Buffer
	status := runRecovery(context.Background(), strings.NewReader(recoveryRequest(protocol.DataHomeRecoveryPlanCommand, "")), &output,
		time.Now, func(context.Context, protocol.StartRecord) (recoveryWorkspace, error) { return workspace, nil })
	if status != 0 {
		t.Fatalf("plan failed: %d / %s", status, output.String())
	}
	record := decodeOutput(t, &output)
	if record["event"] != protocol.DataHomeRecoveryPlanCommand || record["job_id"] != "fictional_job" ||
		record["next_step"] != "applied" || record["operation_id"] != nil || record["data_root"] != nil {
		t.Fatalf("wrong plan projection: %#v", record)
	}
}

func TestRunRecoveryAdvanceUsesUTCMonthOnlyForAssessment(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		step    string
		month   assessmentcontext.Month
		settled bool
	}{
		{"assessment", assessmentcontext.Month{Year: 2026, Number: 10}, false},
		{"terminal", assessmentcontext.Month{}, true},
	} {
		t.Run(test.step, func(t *testing.T) {
			t.Parallel()
			plan := testRecoveryPlan(test.step)
			workspace := &fakeRecoveryWorkspace{plan: plan}
			var output bytes.Buffer
			clock := func() time.Time {
				return time.Date(2026, time.September, 30, 23, 0, 0, 0, time.FixedZone("fixture", -7*60*60))
			}
			status := runRecovery(context.Background(), strings.NewReader(recoveryRequest(protocol.DataHomeRecoveryAdvanceCommand, plan.PlanID)), &output,
				clock, func(context.Context, protocol.StartRecord) (recoveryWorkspace, error) { return workspace, nil })
			if status != 0 || workspace.advancedPlan != plan.PlanID || workspace.advancedMonth != test.month {
				t.Fatalf("advance failed: %d / %#v / %s", status, workspace.advancedMonth, output.String())
			}
			record := decodeOutput(t, &output)
			if record["event"] != "data_home_recovery_advanced" || record["step"] != test.step || record["settled"] != test.settled {
				t.Fatalf("wrong advance projection: %#v", record)
			}
		})
	}
}

func TestRunRecoveryMapsOnlyAllowlistedErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"startup", &startup.Error{Code: "DATA_HOME_RECOVERY_AMBIGUOUS"}, "DATA_HOME_RECOVERY_AMBIGUOUS"},
		{"domain", &applicationwrite.Error{Code: "JOB_INTAKE_RECOVERY_PLAN_STALE"}, "JOB_INTAKE_RECOVERY_PLAN_STALE"},
		{"unknown domain", &applicationwrite.Error{Code: "PRIVATE_DETAIL"}, "JOB_INTAKE_RECOVERY_FAILED"},
		{"unknown", errors.New("private native detail"), "JOB_INTAKE_RECOVERY_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			status := runRecovery(context.Background(), strings.NewReader(recoveryRequest(protocol.DataHomeRecoveryPlanCommand, "")), &output,
				time.Now, func(context.Context, protocol.StartRecord) (recoveryWorkspace, error) { return nil, test.err })
			record := decodeOutput(t, &output)
			if status != 4 || record["error_code"] != test.want {
				t.Fatalf("wrong error projection: %d / %#v", status, record)
			}
		})
	}
}
