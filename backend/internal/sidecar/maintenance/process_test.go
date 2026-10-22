package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documentmigration"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sidecar/protocol"
)

type failedMaintenanceWriter struct{}

func (failedMaintenanceWriter) Write([]byte) (int, error) {
	return 0, errors.New("private write failure")
}

type fakeDocumentRecoveryInventory struct {
	present map[string]bool
	problem *runtimepaths.Error
}

func (f fakeDocumentRecoveryInventory) RequireProcessOwnership() *runtimepaths.Error {
	return f.problem
}
func (f fakeDocumentRecoveryInventory) Exists(path string) (bool, *runtimepaths.Error) {
	if f.problem != nil {
		return false, f.problem
	}
	return f.present[path], nil
}

func documentPlanRequest() string {
	raw, _ := json.Marshal(map[string]any{"command": protocol.DocumentMigrationPlanCommand, "protocol_version": 1, "data_root": fixtureDataRoot})
	return string(raw) + "\n"
}

func TestRunProcessRoutesOneExactRecord(t *testing.T) {
	plan := testRecoveryPlan("applied")
	tests := []struct {
		name    string
		request string
		deps    processDependencies
		event   string
	}{
		{
			name:    "new job",
			request: recoveryRequest(protocol.DataHomeRecoveryPlanCommand, ""),
			deps: processDependencies{
				clock: time.Now,
				openRecovery: func(context.Context, protocol.StartRecord) (recoveryWorkspace, error) {
					return &fakeRecoveryWorkspace{plan: plan}, nil
				},
			},
			event: protocol.DataHomeRecoveryPlanCommand,
		},
		{
			name:    "document",
			request: documentPlanRequest(),
			deps: processDependencies{
				document: func(_ context.Context, request protocol.DocumentMigrationMaintenanceRequest) (map[string]any, string) {
					if request.Command != protocol.DocumentMigrationPlanCommand || request.DataRoot != fixtureDataRoot {
						t.Fatal("dispatcher changed the parsed document request")
					}
					return map[string]any{"event": protocol.DocumentMigrationPlanCommand, "protocol_version": 1}, ""
				},
			},
			event: protocol.DocumentMigrationPlanCommand,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if status := runProcess(context.Background(), strings.NewReader(test.request), &output, test.deps); status != 0 {
				t.Fatalf("maintenance failed: %d / %s", status, output.String())
			}
			if record := decodeOutput(t, &output); record["event"] != test.event {
				t.Fatalf("wrong routed record: %#v", record)
			}
		})
	}
}

func TestRunProcessRejectsUnknownCommandAndOutputFailure(t *testing.T) {
	t.Parallel()
	request := strings.NewReader(`{"command":"private","protocol_version":1}` + "\n")
	var output bytes.Buffer
	if status := runProcess(context.Background(), request, &output, processDependencies{}); status != 3 {
		t.Fatalf("unknown command status = %d", status)
	}
	if record := decodeOutput(t, &output); record["error_code"] != "SIDECAR_COMMAND_UNSUPPORTED" {
		t.Fatalf("unknown command escaped: %#v", record)
	}
	if status := runProcess(context.Background(), strings.NewReader("{}\n"), failedMaintenanceWriter{}, processDependencies{}); status != 6 {
		t.Fatalf("output failure status = %d", status)
	}
}

func TestRunProcessCollapsesPanicsWithoutPrivateDetails(t *testing.T) {
	t.Parallel()
	request := documentPlanRequest()
	var output bytes.Buffer
	status := runProcess(context.Background(), strings.NewReader(request), &output, processDependencies{
		document: func(context.Context, protocol.DocumentMigrationMaintenanceRequest) (map[string]any, string) {
			panic("private native detail")
		},
	})
	record := decodeOutput(t, &output)
	if status != 4 || record["error_code"] != "SIDECAR_MAINTENANCE_FAILED" || strings.Contains(output.String(), "private") {
		t.Fatalf("panic escaped process boundary: %d / %#v", status, record)
	}
}

func TestDocumentRecoveryKindAdmissionIsOwnerSpecific(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, command, want string
		present             map[string]bool
	}{
		{"plan clear", protocol.DocumentMigrationPlanCommand, "", nil},
		{"plan document pending", protocol.DocumentMigrationPlanCommand, documentmigration.CodeRecoveryRequired,
			map[string]bool{datahome.DocumentRecoveryPath: true}},
		{"plan foreign pending", protocol.DocumentMigrationPlanCommand, documentmigration.CodeDataHomeInvalid,
			map[string]bool{datahome.NewJobRecoveryPath: true}},
		{"recover clear finalizer", protocol.DocumentMigrationRecoverCommand, "", nil},
		{"recover document pending", protocol.DocumentMigrationRecoverCommand, "",
			map[string]bool{datahome.DocumentRecoveryPath: true}},
		{"recover foreign pending", protocol.DocumentMigrationRecoverCommand, documentmigration.CodeRecoveryFailed,
			map[string]bool{datahome.NewJobRecoveryPath: true}},
		{"recover approval pending", protocol.DocumentMigrationRecoverCommand, documentmigration.CodeRecoveryFailed,
			map[string]bool{datahome.ApprovalRecoveryPath: true}},
		{"recover ambiguous", protocol.DocumentMigrationRecoverCommand, documentmigration.CodeRecoveryFailed,
			map[string]bool{datahome.DocumentRecoveryPath: true, datahome.NewJobRecoveryPath: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := admitDocumentRecoveryKind(fakeDocumentRecoveryInventory{present: test.present}, test.command); got != test.want {
				t.Fatalf("admission = %q, want %q", got, test.want)
			}
		})
	}
}
