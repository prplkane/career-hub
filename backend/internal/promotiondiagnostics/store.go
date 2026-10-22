package promotiondiagnostics

import (
	"bytes"
	"context"
	"errors"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

const directory = "career_vault/logs/native-intake"

var ErrStore = errors.New("DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED")

// Write never dispatches or recovers product writes. The operation gate is held
// only for this snapshot, not across independently gated intake/link owners.
// observed may be true alongside an error (e.g. closing the root failed); callers
// must not dispatch the next phase unless both observation and cleanup succeed.
func Write(ctx context.Context, dataRoot string, expected datahome.Info, before *Record, after Record) (observed bool, problem error) {
	if ctx == nil || ctx.Err() != nil || !expected.Initialized || expected.DataHomeID == "" || expected.Channel == "" {
		return false, ErrStore
	}
	raw, err := Encode(after)
	if err != nil || before == nil && after.Sequence != 1 || before != nil && !ValidTransition(*before, after) {
		return false, ErrInvalid
	}
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return false, ErrStore
	}
	defer release()
	root, openErr := runtimepaths.Open(dataRoot)
	if openErr != nil {
		return false, ErrStore
	}
	defer func() {
		if root.Close() != nil {
			problem = ErrStore
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return false, ErrStore
	}
	sameHome := func() bool {
		var home *datahome.Info
		var failure *datahome.Error
		if before == nil {
			home, failure = datahome.Inspect(dataRoot, &expected.Channel)
		} else {
			// A failed intake may have left its intent. Record its failure only;
			// do not admit product writes or bypass other recovery sentinels.
			home, failure = datahome.InspectOwnedForNewJobRecovery(root, &expected.Channel)
		}
		return failure == nil && home != nil && *home == expected
	}
	if !sameHome() {
		return false, ErrStore
	}
	if _, failure := root.EnsureDirectory(directory); failure != nil {
		return false, ErrStore
	}
	if before == nil {
		entries, _, failure := root.ListDirectoryFileMetadata(directory)
		if failure != nil || len(entries) >= runtimepaths.MaxDirectoryEntries {
			return false, ErrStore
		}
	}
	if ctx.Err() != nil || !sameHome() {
		return false, ErrStore
	}
	name := directory + "/" + after.OperationID + ".json"
	var changed bool
	var failure *runtimepaths.Error
	if before == nil {
		changed, failure = root.CreateFile(name, raw)
	} else {
		prior, _ := Encode(*before)
		changed, failure = root.ReplaceFile(name, prior, raw)
	}
	retained, found, readErr := root.ReadSingleLinkFile(name, 4096)
	observed = found && readErr == nil && bytes.Equal(raw, retained)
	if failure != nil || !changed || !observed || ctx.Err() != nil || !sameHome() {
		return observed, ErrStore
	}
	return observed, nil
}
