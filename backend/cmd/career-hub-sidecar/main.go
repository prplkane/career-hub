// career-hub-sidecar is an explicitly selected migration build with read-only
// HTTP by default. Maintainer bootstrap additionally prepares the Windows Data
// Home/database and enables the fixed frontend/Profile/material workflows.
// It is not the packaged/shipped backend and never discovers a Data Home.
package main

import (
	"context"
	"io"
	"net/http"
	"os"

	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/maintenance"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	sidecar "careerhub.local/backend/internal/sidecar/runtime"
	"careerhub.local/backend/internal/sidecar/startup"
)

func prepare(ctx context.Context, start protocol.StartRecord) (sidecar.Prepared, error) {
	if err := ctx.Err(); err != nil {
		return sidecar.Prepared{}, err
	}
	workspace, err := readonly.OpenForProcess(start)
	if err != nil {
		if typed, ok := err.(*readonly.Error); ok {
			return sidecar.Prepared{}, &sidecar.StartupError{Code: typed.Code}
		}
		return sidecar.Prepared{}, &sidecar.StartupError{Code: "SIDECAR_STARTUP_FAILED"}
	}
	if err := ctx.Err(); err != nil {
		return sidecar.Prepared{}, err
	}
	return preparedWorkspace(workspace, start.SessionToken), nil
}

func prepareBootstrap(ctx context.Context, start protocol.StartRecord) (sidecar.Prepared, error) {
	return prepareBootstrapWithOrigins(ctx, start, nil)
}

func prepareBootstrapWithOrigins(ctx context.Context, start protocol.StartRecord, origins []string) (sidecar.Prepared, error) {
	workspace, err := startup.OpenForProcess(ctx, start)
	if err != nil {
		if typed, ok := err.(*startup.Error); ok {
			return sidecar.Prepared{}, &sidecar.StartupError{Code: typed.Code}
		}
		return sidecar.Prepared{}, &sidecar.StartupError{Code: "SIDECAR_STARTUP_FAILED"}
	}
	if err := ctx.Err(); err != nil {
		return sidecar.Prepared{}, err
	}
	return preparedApplication(workspace, start, origins...), nil
}

func preparedWorkspace(workspace *readonly.Workspace, sessionToken string) sidecar.Prepared {
	return sidecar.Prepared{
		AppVersion: workspace.AppVersion(),
		Handler: func(port int) (http.Handler, error) {
			handler, err := httpapi.New(port, workspace.AppVersion(), workspace.Registry, workspace.Vault, workspace.Documents)
			if err != nil {
				return nil, err
			}
			if err := httpapi.UseSessionToken(handler, sessionToken); err != nil {
				return nil, err
			}
			return handler, nil
		},
	}
}

func execute(args []string, input io.ReadCloser, output io.WriteCloser) int {
	if len(args) > 1 && args[0] == "--sidecar-bootstrap" {
		var origins []string
		fixedExtensionPort := false
		for i := 1; i < len(args); i += 2 {
			if i+1 >= len(args) {
				return 2
			}
			switch args[i] {
			case "--extension-origin":
				origins = append(origins, args[i+1])
			case "--extension-port":
				if fixedExtensionPort || args[i+1] != "8766" {
					return 2
				}
				fixedExtensionPort = true
			default:
				return 2
			}
		}
		if httpapi.ValidateExtensionOrigins(origins) != nil {
			return 2
		}
		prepare := func(ctx context.Context, start protocol.StartRecord) (sidecar.Prepared, error) {
			return prepareBootstrapWithOrigins(ctx, start, origins)
		}
		if fixedExtensionPort {
			return sidecar.RunWithLoopbackPort(input, output, prepare, 8766)
		}
		return sidecar.Run(input, output, prepare)
	}
	if len(args) != 1 {
		return 2
	}
	switch args[0] {
	case "--sidecar":
		return sidecar.Run(input, output, prepare)
	case "--sidecar-bootstrap":
		return sidecar.Run(input, output, prepareBootstrap)
	case "--desktop-maintenance":
		return maintenance.Run(input, output)
	default:
		return 2
	}
}

func main() {
	// Run is a process boundary, not a reusable embedded service. Exiting also
	// reaps an uncooperative synchronous OS operation after the lifecycle bound
	// and releases the process-lifetime Data Home claim. Never release it merely
	// because Run returned: outstanding callbacks may not yet be quiescent.
	os.Exit(execute(os.Args[1:], os.Stdin, os.Stdout))
}
