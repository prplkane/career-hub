//go:build windows

package startup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
)

const newJobRecoveryStartupDataEnv = "CAREERHUB_TEST_RECOVERY_STARTUP_DATA"
const newJobRecoveryStartupResourcesEnv = "CAREERHUB_TEST_RECOVERY_STARTUP_RESOURCES"
const newJobRecoveryStartupCaseEnv = "CAREERHUB_TEST_RECOVERY_STARTUP_CASE"

func TestNewJobRecoveryWorkspaceOwnedAdmission(t *testing.T) {
	if dataRoot := os.Getenv(newJobRecoveryStartupDataEnv); dataRoot != "" {
		runNewJobRecoveryWorkspaceOwnedAdmission(t, protocol.StartRecord{
			DataRoot: dataRoot, ResourceRoot: os.Getenv(newJobRecoveryStartupResourcesEnv), Channel: "dev",
		}, os.Getenv(newJobRecoveryStartupCaseEnv))
		return
	}
	start := fixture(t)
	if _, problem := datahome.Bootstrap(context.Background(), start.DataRoot, start.Channel); problem != nil {
		t.Fatal(problem.Code)
	}
	writeFixture(t, start.DataRoot, datahome.NewJobRecoveryPath, "opaque fictional pending intake")
	if workspace, err := readonly.Open(start); workspace != nil || err == nil {
		t.Fatal("ordinary startup admitted pending new-job recovery")
	} else if typed, ok := err.(*readonly.Error); !ok || typed.Code != "DATA_HOME_RECOVERY_REQUIRED" {
		t.Fatalf("unexpected ordinary startup refusal: %v", err)
	}
	before := snapshot(t, start.DataRoot)
	for _, scenario := range []string{"plan", "wrong_channel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNewJobRecoveryWorkspaceOwnedAdmission$")
			cmd.Env = append(os.Environ(), newJobRecoveryStartupDataEnv+"="+start.DataRoot,
				newJobRecoveryStartupResourcesEnv+"="+start.ResourceRoot, newJobRecoveryStartupCaseEnv+"="+scenario)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("recovery startup child failed: %v: %.1200s", err, output)
			}
		})
	}
	if after := snapshot(t, start.DataRoot); !reflect.DeepEqual(before, after) {
		t.Fatal("recovery startup admission changed the Data Home")
	}
}

func runNewJobRecoveryWorkspaceOwnedAdmission(t *testing.T, start protocol.StartRecord, scenario string) {
	t.Helper()
	if scenario == "wrong_channel" {
		start.Channel = "stable"
	}
	workspace, err := OpenNewJobRecoveryForProcess(context.Background(), start)
	if scenario == "wrong_channel" {
		requireCode(t, err, "DATA_HOME_CHANNEL_MISMATCH")
		if workspace != nil {
			t.Fatal("wrong channel returned recovery workspace")
		}
		return
	}
	if err != nil || workspace == nil {
		t.Fatalf("recovery workspace failed: %v", err)
	}
	plan, planErr := workspace.Plan(context.Background())
	typed, ok := planErr.(*applicationwrite.Error)
	if plan != nil || !ok || typed.Code != "JOB_INTAKE_INTENT_INVALID" {
		t.Fatalf("opaque intent became a recovery plan: %#v / %v", plan, planErr)
	}
	for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
		if fmt.Sprintf(format, typed) != "Application ledger operation could not be completed." {
			t.Fatal("recovery plan error leaked private state")
		}
	}
}
