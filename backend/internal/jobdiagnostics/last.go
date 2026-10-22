package jobdiagnostics

import (
	"context"
	"path/filepath"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

// ReadLastEvent reads only the last complete fixed event from the final
// bounded segment. A missing journal is empty; a damaged tail is never
// interpreted as no in-flight run. This is read-only recovery preparation.
func ReadLastEvent(ctx context.Context, dataRoot string, expected datahome.Info) (last *Decoded, problem *StoreError) {
	if ctx == nil || ctx.Err() != nil {
		return nil, storeFailure("JOB_DIAGNOSTIC_CANCELED")
	}
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return nil, storeFailure("JOB_DIAGNOSTIC_CANCELED")
	}
	defer release()
	defer func() {
		if problem == nil {
			if ctx.Err() != nil {
				last, problem = nil, storeFailure("JOB_DIAGNOSTIC_CANCELED")
			} else if !sameHome(dataRoot, expected) {
				last, problem = nil, storeFailure("JOB_DIAGNOSTIC_HOME_CHANGED")
			}
		}
	}()
	if !sameHome(dataRoot, expected) {
		return nil, storeFailure("JOB_DIAGNOSTIC_HOME_CHANGED")
	}
	root, openErr := runtimepaths.Open(dataRoot)
	if openErr != nil {
		return nil, storeFailure("JOB_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil && problem == nil {
			last, problem = nil, storeFailure("JOB_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return nil, storeFailure("JOB_DIAGNOSTIC_OWNERSHIP_REQUIRED")
	}
	exists, existsErr := root.Exists(segmentDirectory)
	if existsErr != nil {
		return nil, storeFailure("JOB_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	if !exists {
		return nil, nil
	}
	segments, openErr := runtimepaths.Open(filepath.Join(dataRoot, filepath.FromSlash(segmentDirectory)))
	if openErr != nil {
		return nil, storeFailure("JOB_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	defer func() {
		if segments.Close() != nil && problem == nil {
			last, problem = nil, storeFailure("JOB_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	names, listErr := segments.ListFileNames()
	if listErr != nil || len(names) > MaxDiagnosticSegments {
		return nil, storeFailure("JOB_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	number, nameErr := nextSegment(names)
	if nameErr != nil {
		return nil, nameErr
	}
	if number == 0 {
		return nil, nil
	}
	raw, found, readErr := segments.ReadSingleLinkFile(formatSegmentName(number), runtimepaths.MaxReadBytes)
	if readErr != nil || !found {
		return nil, storeFailure("JOB_DIAGNOSTIC_READ_UNAVAILABLE")
	}
	decoded, tailErr := tailEvent(raw)
	if tailErr != nil {
		return nil, tailErr
	}
	return &decoded, nil
}
