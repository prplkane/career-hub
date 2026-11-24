package main

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/vault/vaultsetup"
)

func importVaultSetup(ctx context.Context, w *readonly.Workspace, root, resources string, request httpapi.VaultSetupRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := &httpapi.ApplicationOutcome{}
	failure := func(code string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		return out, &httpapi.ApplicationError{Code: code, Category: "vault_setup"}
	}
	home, err := w.StartupHome()
	if err != nil {
		return failure("WORKSPACE_UNAVAILABLE")
	}
	var bundle *vaultsetup.Bundle
	var problem *vaultsetup.Error
	exportFile := request.Bundle != nil
	if exportFile {
		if len(request.Files) != 0 || request.NewProfileName != nil {
			return failure("VAULT_SETUP_INVALID")
		}
		bundle, problem = vaultsetup.PrepareBundle(ctx, request.Bundle)
	} else if request.NewProfileName != nil {
		if len(request.Files) != 0 {
			return failure("VAULT_SETUP_INVALID")
		}
		bundle, problem = vaultsetup.PrepareEmpty(ctx, *request.NewProfileName)
	} else {
		bundle, problem = vaultsetup.Prepare(ctx, request.Files)
	}
	if problem != nil {
		return failure(problem.Code)
	}
	owner := vaultsetup.New(root, home).WithResources(resources)
	var result *vaultsetup.Outcome
	if request.Apply {
		result, problem = owner.Apply(ctx, bundle, request.ConfirmedPlan)
	} else {
		result, problem = owner.Preview(ctx, bundle)
	}
	if result != nil {
		out.Committed, out.CommitUnknown, out.CleanupFailed = result.Committed, result.CommitUnknown, result.CleanupFailed
		out.Partial = result.Committed && (!result.Complete || problem != nil)
		out.RevisionRefreshRequired = result.Committed || result.CommitUnknown
	}
	if problem != nil {
		return failure(problem.Code)
	}
	if result == nil || result.Plan == nil {
		return failure("LOCAL_RESPONSE_INVALID")
	}
	switch {
	case request.Apply && exportFile:
		// Counts only: never file names, contents or paths.
		out.Result, err = json.Marshal(map[string]any{"contract_version": 1, "state": "configured", "created": result.Created, "retained": len(result.Plan.Retain), "replaced": result.Replaced})
	case request.Apply:
		out.Result, err = json.Marshal(map[string]any{"contract_version": 1, "state": "configured", "created": result.Created, "retained": len(result.Plan.Retain)})
	case exportFile:
		plan := result.Plan
		out.Result, err = json.Marshal(map[string]any{"contract_version": plan.ContractVersion, "plan_id": plan.PlanID, "create": plan.Create, "retain": plan.Retain, "replace": plan.Replace})
	default:
		out.Result, err = json.Marshal(result.Plan)
	}
	if err != nil {
		return failure("LOCAL_RESPONSE_INVALID")
	}
	return out, nil
}
