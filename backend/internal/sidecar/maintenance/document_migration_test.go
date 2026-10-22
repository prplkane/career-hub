package maintenance

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/documentmigration"
	"careerhub.local/backend/internal/sidecar/protocol"
)

type fakeDocumentMigrationDomain struct {
	plan          documentmigration.Plan
	apply         documentmigration.ApplyResult
	recover       documentmigration.RecoveryResult
	problem       *documentmigration.Error
	command       string
	confirmedPlan string
}

func (f *fakeDocumentMigrationDomain) Plan(context.Context) (documentmigration.Plan, *documentmigration.Error) {
	f.command = protocol.DocumentMigrationPlanCommand
	return f.plan, f.problem
}

func (f *fakeDocumentMigrationDomain) Apply(_ context.Context, confirmPlan string) (documentmigration.ApplyResult, *documentmigration.Error) {
	f.command, f.confirmedPlan = protocol.DocumentMigrationApplyCommand, confirmPlan
	return f.apply, f.problem
}

func (f *fakeDocumentMigrationDomain) Recover(context.Context) (documentmigration.RecoveryResult, *documentmigration.Error) {
	f.command = protocol.DocumentMigrationRecoverCommand
	return f.recover, f.problem
}

func documentProtocolPlan() documentmigration.Plan {
	return documentmigration.Plan{
		PlanID: "sha256:" + strings.Repeat("a", 64), SourceFormatVersion: 1, TargetFormatVersion: 2,
		DocumentCount: 2, CiphertextFileCount: 3, TotalCiphertextBytes: 512,
	}
}

func TestExecuteDocumentMigrationProjectsOnlyInstalledRecords(t *testing.T) {
	t.Parallel()
	plan := documentProtocolPlan()
	tests := []struct {
		name, command string
		domain        *fakeDocumentMigrationDomain
		want          map[string]any
	}{
		{"plan", protocol.DocumentMigrationPlanCommand, &fakeDocumentMigrationDomain{plan: plan}, map[string]any{
			"event": protocol.DocumentMigrationPlanCommand, "protocol_version": 1, "plan_id": plan.PlanID,
			"source_format_version": 1, "target_format_version": 2, "document_count": 2,
			"ciphertext_file_count": 3, "total_ciphertext_bytes": 512,
		}},
		{"apply", protocol.DocumentMigrationApplyCommand, &fakeDocumentMigrationDomain{apply: documentmigration.ApplyResult{Plan: plan}}, map[string]any{
			"event": protocol.DocumentMigrationCompleteEvent, "protocol_version": 1,
			"plan_id": plan.PlanID, "document_count": 2,
		}},
		{"recover", protocol.DocumentMigrationRecoverCommand, &fakeDocumentMigrationDomain{recover: documentmigration.RecoveryResult{Present: true, Finalized: true}}, map[string]any{
			"event": protocol.DocumentMigrationRecoveredEvent, "protocol_version": 1, "recovered": true,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := protocol.DocumentMigrationMaintenanceRequest{Command: test.command, ConfirmPlan: plan.PlanID}
			record, code := executeDocumentMigration(context.Background(), request, test.domain)
			if code != "" || !reflect.DeepEqual(record, test.want) || test.domain.command != test.command {
				t.Fatalf("wrong adapter result: %#v / %s / %s", record, code, test.domain.command)
			}
			if test.command == protocol.DocumentMigrationApplyCommand && test.domain.confirmedPlan != plan.PlanID {
				t.Fatal("adapter changed the reviewed plan confirmation")
			}
		})
	}
}

func TestExecuteDocumentMigrationAllowlist(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ supplied, want string }{
		{documentmigration.CodePlanMismatch, documentmigration.CodePlanMismatch},
		{documentmigration.CodeRecoveryFailed, documentmigration.CodeRecoveryFailed},
		{documentmigration.CodeOwnershipRequired, documentmigration.CodeApplyFailed},
		{"PRIVATE_NATIVE_DETAIL", documentmigration.CodeApplyFailed},
	} {
		domain := &fakeDocumentMigrationDomain{problem: &documentmigration.Error{Code: test.supplied}}
		record, code := executeDocumentMigration(context.Background(), protocol.DocumentMigrationMaintenanceRequest{
			Command: protocol.DocumentMigrationPlanCommand,
		}, domain)
		if record != nil || code != test.want {
			t.Fatalf("error escaped allowlist: %#v / %q, want %q", record, code, test.want)
		}
	}
}
