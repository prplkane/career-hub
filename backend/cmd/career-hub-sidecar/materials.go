package main

import (
	"context"
	"net/http"

	"careerhub.local/backend/internal/applications/applicationtexts"
	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/materials/materialintegrity"
	"careerhub.local/backend/internal/materials/materialwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	sidecar "careerhub.local/backend/internal/sidecar/runtime"
)

func materialActions(w *readonly.Workspace, start protocol.StartRecord) httpapi.MaterialActions {
	owner := materialwrite.New(start.DataRoot)
	invoke := func(ctx context.Context, call func() (*materialwrite.Outcome, *materialwrite.Error)) (*materialwrite.Outcome, *materialwrite.Error) {
		if ctx == nil || ctx.Err() != nil {
			return &materialwrite.Outcome{}, &materialwrite.Error{Stage: "context", Code: "MATERIAL_CANCELED", SourceClass: "ApplicationMaterialError"}
		}
		if w.CheckAdmission() != nil {
			return &materialwrite.Outcome{}, &materialwrite.Error{Stage: "admission", Code: "MATERIAL_WORKSPACE_UNAVAILABLE", SourceClass: "ApplicationMaterialError"}
		}
		out, e := call()
		// An owner may have published before admission drift/cleanup failure.
		// Preserve those facts but never expose data from a changed workspace.
		if w.CheckAdmission() != nil {
			if out != nil {
				out.Result, out.Content = nil, nil
			}
			return out, &materialwrite.Error{Stage: "admission", Code: "MATERIAL_WORKSPACE_CHANGED", SourceClass: "ApplicationMaterialError"}
		}
		return out, e
	}
	actions := httpapi.MaterialActions{
		ApplicationText: func(ctx context.Context, job string, draft applicationtexts.Draft) (*materialwrite.Outcome, *materialwrite.Error) {
			return invoke(ctx, func() (*materialwrite.Outcome, *materialwrite.Error) {
				return owner.SaveApplicationText(ctx, job, draft)
			})
		},
		Draft: func(ctx context.Context, r materials.Request) (*materialwrite.Outcome, *materialwrite.Error) {
			return invoke(ctx, func() (*materialwrite.Outcome, *materialwrite.Error) { return owner.Run(ctx, r) })
		},
		Review: func(ctx context.Context, r materialintegrity.ReviewRequest) (*materialwrite.Outcome, *materialwrite.Error) {
			return invoke(ctx, func() (*materialwrite.Outcome, *materialwrite.Error) { return owner.Review(ctx, start.ResourceRoot, r) })
		},
		Export: func(ctx context.Context, r materialwrite.ExportRequest) (*materialwrite.Outcome, *materialwrite.Error) {
			return invoke(ctx, func() (*materialwrite.Outcome, *materialwrite.Error) { return owner.Export(ctx, start.ResourceRoot, r) })
		},
		Download: func(ctx context.Context, r materialwrite.ExportReadRequest) (*materialwrite.Outcome, *materialwrite.Error) {
			return invoke(ctx, func() (*materialwrite.Outcome, *materialwrite.Error) {
				return owner.ReadExport(ctx, start.ResourceRoot, r)
			})
		},
		Legacy: func(ctx context.Context, r materialwrite.LegacyExportRequest) (*materialwrite.Outcome, *materialwrite.Error) {
			return invoke(ctx, func() (*materialwrite.Outcome, *materialwrite.Error) { return owner.ReadLegacyExport(ctx, r) })
		},
	}
	return actions
}

func preparedMaterials(w *readonly.Workspace, start protocol.StartRecord) sidecar.Prepared {
	actions := materialActions(w, start)
	return sidecar.Prepared{AppVersion: w.AppVersion(), Handler: func(port int) (http.Handler, error) {
		return httpapi.NewWithMaterials(port, w.AppVersion(), w.Registry, w.Vault, w.Documents, actions)
	}}
}
