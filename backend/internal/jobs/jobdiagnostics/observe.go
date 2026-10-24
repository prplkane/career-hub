package jobdiagnostics

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"strings"

	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// ObservePlannedPublication makes one guarded, bounded observation after an
// interrupted planned run. A missing, ambiguous or unsafe card is unknown;
// this function never replays a write or changes the journal. The job ID is
// recovered only by comparing the journal's key with bounded saved filenames.
func ObservePlannedPublication(ctx context.Context, dataRoot string, expected datahome.Info, plan Decoded) (state PublicationState, problem *StoreError) {
	state = PublicationUnknown
	if ctx == nil || ctx.Err() != nil {
		return state, storeFailure("JOB_DIAGNOSTIC_CANCELED")
	}
	if plan.Operation != "reanalyze" || !ValidRunID(plan.RunID) || plan.Event != "planned" || plan.Sequence != 2 || !plan.HasPlan || plan.JobKeySHA256 == ([32]byte{}) || plan.PriorCardSHA256 == plan.NextCardSHA256 {
		return state, storeFailure("JOB_DIAGNOSTIC_EVENT_INVALID")
	}
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return state, storeFailure("JOB_DIAGNOSTIC_CANCELED")
	}
	defer release()
	if !sameHome(dataRoot, expected) {
		return state, storeFailure("JOB_DIAGNOSTIC_HOME_CHANGED")
	}
	root, openErr := runtimepaths.Open(dataRoot)
	if openErr != nil {
		return state, storeFailure("JOB_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil && problem == nil {
			state, problem = PublicationUnknown, storeFailure("JOB_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return state, storeFailure("JOB_DIAGNOSTIC_OWNERSHIP_REQUIRED")
	}
	exists, existsErr := root.Exists("jobs/normalized")
	if existsErr != nil {
		return state, storeFailure("JOB_DIAGNOSTIC_CARD_UNAVAILABLE")
	}
	if !exists {
		return state, nil
	}
	cards, cardRootErr := runtimepaths.Open(filepath.Join(dataRoot, "jobs", "normalized"))
	if cardRootErr != nil {
		return state, storeFailure("JOB_DIAGNOSTIC_CARD_UNAVAILABLE")
	}
	defer func() {
		if cards.Close() != nil && problem == nil {
			state, problem = PublicationUnknown, storeFailure("JOB_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	names, listErr := cards.ListFileNames()
	if listErr != nil {
		return state, storeFailure("JOB_DIAGNOSTIC_CARD_UNAVAILABLE")
	}
	match := ""
	for _, name := range names {
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		id := strings.TrimSuffix(name, ".yaml")
		if !jobcard.ValidID(id) || sha256.Sum256([]byte(id)) != plan.JobKeySHA256 {
			continue
		}
		if match != "" {
			return state, nil
		}
		match = name
	}
	if match == "" {
		return state, nil
	}
	current, present, readErr := cards.ReadSingleLinkFile(match, cataloginput.YAMLMaxInputBytes)
	if readErr != nil {
		return state, storeFailure("JOB_DIAGNOSTIC_CARD_UNAVAILABLE")
	}
	if ctx.Err() != nil {
		return state, storeFailure("JOB_DIAGNOSTIC_CANCELED")
	}
	if !sameHome(dataRoot, expected) {
		return state, storeFailure("JOB_DIAGNOSTIC_HOME_CHANGED")
	}
	return ClassifyPublication(plan, current, present), nil
}
