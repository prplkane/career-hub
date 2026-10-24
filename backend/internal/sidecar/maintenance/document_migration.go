package maintenance

import (
	"context"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/documentmigration"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type documentMigrationDomain interface {
	Plan(context.Context) (documentmigration.Plan, *documentmigration.Error)
	Apply(context.Context, string) (documentmigration.ApplyResult, *documentmigration.Error)
	Recover(context.Context) (documentmigration.RecoveryResult, *documentmigration.Error)
}

type ownedDocumentMigration struct {
	root    *runtimepaths.Root
	backend keys.Backend
}

func (owner *ownedDocumentMigration) Plan(ctx context.Context) (documentmigration.Plan, *documentmigration.Error) {
	if ctx == nil || ctx.Err() != nil {
		return documentmigration.Plan{}, &documentmigration.Error{Code: documentmigration.CodeApplyFailed}
	}
	plan, problem := documentmigration.BuildPlanOwned(owner.root)
	if problem == nil && ctx.Err() != nil {
		return documentmigration.Plan{}, &documentmigration.Error{Code: documentmigration.CodeApplyFailed}
	}
	return plan, problem
}

func (owner *ownedDocumentMigration) Apply(ctx context.Context, confirmPlan string) (documentmigration.ApplyResult, *documentmigration.Error) {
	return documentmigration.ApplyOwned(ctx, owner.root, owner.backend, confirmPlan)
}

func (owner *ownedDocumentMigration) Recover(ctx context.Context) (documentmigration.RecoveryResult, *documentmigration.Error) {
	return documentmigration.RecoverOwned(ctx, owner.root, owner.backend)
}

// executeDocumentMigrationOwned assumes the caller opened and process-claimed
// request.DataRoot as root. It performs no path discovery, secret-store
// selection, stdio, process routing, or startup recovery dispatch.
func executeDocumentMigrationOwned(ctx context.Context, request protocol.DocumentMigrationMaintenanceRequest,
	root *runtimepaths.Root, backend keys.Backend) (map[string]any, string) {
	return executeDocumentMigration(ctx, request, &ownedDocumentMigration{root: root, backend: backend})
}

func executeDocumentMigration(ctx context.Context, request protocol.DocumentMigrationMaintenanceRequest,
	domain documentMigrationDomain) (map[string]any, string) {
	if ctx == nil || domain == nil {
		return nil, documentmigration.CodeApplyFailed
	}
	switch request.Command {
	case protocol.DocumentMigrationPlanCommand:
		plan, problem := domain.Plan(ctx)
		if problem != nil {
			return nil, documentMigrationErrorCode(problem)
		}
		record, err := protocol.DocumentMigrationPlanRecord(protocol.DocumentMigrationPlanOutput{
			PlanID: plan.PlanID, SourceFormatVersion: plan.SourceFormatVersion,
			TargetFormatVersion: plan.TargetFormatVersion, DocumentCount: plan.DocumentCount,
			CiphertextFileCount: plan.CiphertextFileCount, TotalCiphertextBytes: plan.TotalCiphertextBytes,
		})
		if err != nil {
			return nil, documentmigration.CodeApplyFailed
		}
		return record, ""
	case protocol.DocumentMigrationApplyCommand:
		result, problem := domain.Apply(ctx, request.ConfirmPlan)
		if problem != nil {
			return nil, documentMigrationErrorCode(problem)
		}
		record, err := protocol.DocumentMigrationCompleteRecord(result.Plan.PlanID, result.Plan.DocumentCount)
		if err != nil {
			return nil, documentmigration.CodeApplyFailed
		}
		return record, ""
	case protocol.DocumentMigrationRecoverCommand:
		result, problem := domain.Recover(ctx)
		if problem != nil {
			return nil, documentMigrationErrorCode(problem)
		}
		return protocol.DocumentMigrationRecoveredRecord(result.Present), ""
	default:
		return nil, documentmigration.CodeApplyFailed
	}
}

func documentMigrationErrorCode(problem *documentmigration.Error) string {
	if problem == nil {
		return documentmigration.CodeApplyFailed
	}
	switch problem.Code {
	case documentmigration.CodeDataHomeInvalid, documentmigration.CodeNotRequired,
		documentmigration.CodeRequiresWindows, documentmigration.CodeSourceInvalid,
		documentmigration.CodeRecoveryRequired, documentmigration.CodePathInvalid,
		documentmigration.CodePlanMismatch, documentmigration.CodeSourceChanged,
		documentmigration.CodeActivationFailed, documentmigration.CodeRecoveryFailed,
		documentmigration.CodeApplyFailed, documentmigration.CodeStagingFailed:
		return problem.Code
	default:
		return documentmigration.CodeApplyFailed
	}
}
