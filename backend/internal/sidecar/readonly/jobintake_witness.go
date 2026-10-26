package readonly

import (
	"context"
	"crypto/sha256"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// The fixed file set is private so a caller cannot replace a prepared
// catalog/profile witness with an arbitrary path or an empty check. Directory
// and duplicate identity observations are separate and must be repeated by
// the future coordinator before intent publication under its global gate.
type newJobInputWitness struct {
	path     string
	resource bool
	present  bool
	hash     [32]byte
}

type newJobReadSet struct {
	files    [5]newJobInputWitness
	revision string
	complete bool
}

var newJobReadPaths = [5]struct {
	path     string
	resource bool
}{
	{catalogstore.CoreGroupsPath, true},
	{catalogstore.CustomGroupsPath, false},
	{catalogstore.CoreSkillsPath, true},
	{catalogstore.CustomSkillsPath, false},
	{"career_vault/visa_profile.yaml", false},
}

func captureNewJobReadSet(inputs *jobDigestInputs) (newJobReadSet, error) {
	var result newJobReadSet
	if inputs == nil || inputs.registry == nil || inputs.registry.Snapshot == nil || inputs.data == nil || inputs.resources == nil ||
		len(inputs.data.observed) != 3 || len(inputs.resources.observed) != 2 {
		return result, fail("JOB_INTAKE_WITNESS_INVALID")
	}
	for i, fixed := range newJobReadPaths {
		observed := inputs.data.observed
		if fixed.resource {
			observed = inputs.resources.observed
		}
		found := false
		for _, item := range observed {
			if item.path != fixed.path {
				continue
			}
			if found {
				return newJobReadSet{}, fail("JOB_INTAKE_WITNESS_INVALID")
			}
			result.files[i] = newJobInputWitness{path: fixed.path, resource: fixed.resource, present: item.present, hash: item.hash}
			found = true
		}
		if !found {
			return newJobReadSet{}, fail("JOB_INTAKE_WITNESS_INVALID")
		}
	}
	result.revision = inputs.registry.Snapshot.CatalogRevision()
	result.complete = true
	return result, nil
}

func validNewJobReadSet(prepared *NewJobPreparation) bool {
	if prepared == nil || !prepared.readSet.complete || prepared.readSet.revision == "" ||
		prepared.readSet.revision != prepared.CatalogRevision ||
		prepared.readSet.files[4].hash != prepared.ProfileFileSHA256 ||
		prepared.readSet.files[4].present != prepared.ProfilePresent {
		return false
	}
	for i, fixed := range newJobReadPaths {
		if prepared.readSet.files[i].path != fixed.path || prepared.readSet.files[i].resource != fixed.resource {
			return false
		}
	}
	return prepared.readSet.files[0].present && prepared.readSet.files[2].present
}

// VerifyNewJobReadSet is a host-only, read-only recheck suitable before and
// after a pending new-job intent. The caller must already hold the global
// operation gate; this method does not acquire it or authorize file writes.
// It never rescans saved cards or turns a partial file phase into success.
func (w *Workspace) VerifyNewJobReadSet(ctx context.Context, prepared *NewJobPreparation) (failure error) {
	if err := assessmentCanceled(ctx); err != nil {
		return err
	}
	if w == nil || w.dataHomeID == "" || !validNewJobReadSet(prepared) {
		return fail("JOB_INTAKE_WITNESS_INVALID")
	}
	version, err := validateRoots(w.start)
	if err != nil || version != w.appVersion {
		return fail("JOB_INTAKE_RESOURCE_CHANGED")
	}
	data, pathErr := runtimepaths.Open(w.start.DataRoot)
	if pathErr != nil {
		return fail("JOB_INTAKE_WITNESS_UNAVAILABLE")
	}
	defer func() {
		if data.Close() != nil && failure == nil {
			failure = fail("JOB_INTAKE_WITNESS_UNAVAILABLE")
		}
	}()
	if data.RequireProcessOwnership() != nil {
		return fail("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	resources, pathErr := runtimepaths.Open(w.start.ResourceRoot)
	if pathErr != nil {
		return fail("JOB_INTAKE_WITNESS_UNAVAILABLE")
	}
	defer func() {
		if resources.Close() != nil && failure == nil {
			failure = fail("JOB_INTAKE_WITNESS_UNAVAILABLE")
		}
	}()
	home, admissionErr := datahome.InspectOwnedForNewJobRecovery(data, &w.start.Channel)
	if admissionErr != nil || home == nil || *home != w.startupHome {
		return fail("JOB_INTAKE_HOME_CHANGED")
	}
	for _, witness := range prepared.readSet.files {
		if err := assessmentCanceled(ctx); err != nil {
			return err
		}
		root := data
		if witness.resource {
			root = resources
		}
		raw, present, readErr := root.ReadSingleLinkFile(witness.path, cataloginput.YAMLMaxInputBytes)
		if readErr != nil || present != witness.present || sha256.Sum256(raw) != witness.hash {
			return fail("JOB_INTAKE_INPUT_CHANGED")
		}
	}
	home, admissionErr = datahome.InspectOwnedForNewJobRecovery(data, &w.start.Channel)
	if admissionErr != nil || home == nil || *home != w.startupHome {
		return fail("JOB_INTAKE_HOME_CHANGED")
	}
	version, err = validateResources(resources)
	if err != nil || version != w.appVersion {
		return fail("JOB_INTAKE_RESOURCE_CHANGED")
	}
	return assessmentCanceled(ctx)
}
