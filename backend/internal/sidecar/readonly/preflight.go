package readonly

import (
	"path/filepath"
	"runtime"
	"strings"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sidecar/protocol"
)

// Preflight validates fixed resources and the resource/data boundary without
// creating a Data Home. A missing candidate is checked through its existing
// managed ancestry. No handles or reservation survive this call; the caller must
// quiesce writers and revalidate after initialization. Initialized Open retains
// its original validation order and does not call this earlier core decoder.
func Preflight(start protocol.StartRecord) (failure error) {
	if err := validateStart(start); err != nil {
		return err
	}
	if lexicalOverlap(start.ResourceRoot, start.DataRoot) {
		return fail("SIDECAR_PATH_BOUNDARY_INVALID")
	}
	resources, pathErr := runtimepaths.Open(start.ResourceRoot)
	if pathErr != nil {
		return fail("SIDECAR_RESOURCE_INVALID")
	}
	defer func() {
		if err := resources.Close(); err != nil && failure == nil {
			failure = fail("SIDECAR_RESOURCE_INVALID")
		}
	}()
	if _, err := validateResources(resources); err != nil {
		return err
	}
	data, existingPath, complete, err := candidateAncestor(start.DataRoot)
	if err != nil {
		return err
	}
	defer func() {
		if err := data.Close(); err != nil && failure == nil {
			failure = fail("DATA_HOME_UNAVAILABLE")
		}
	}()
	// For a missing candidate its existing ancestor may legitimately also be an
	// ancestor of the resource root (new sibling). Only resource-within-candidate
	// checks are meaningful once the complete candidate itself already exists.
	if overlaps, err := identityWithin(start.ResourceRoot, existingPath); err != nil || overlaps {
		return fail("SIDECAR_PATH_BOUNDARY_INVALID")
	}
	if complete {
		if overlaps, err := identityWithin(existingPath, start.ResourceRoot); err != nil || overlaps {
			return fail("SIDECAR_PATH_BOUNDARY_INVALID")
		}
	}
	return validateCoreInputs(resources)
}

// candidateAncestor first lets the existing managed absolute-path validator
// validate every configured component. On a missing root, probe prefixes only
// through managed opens/Exists, never raw Lstat through an unchecked link.
// The drive/filesystem anchor alone is deliberately not an admissible Root.
func candidateAncestor(raw string) (_ *runtimepaths.Root, _ string, _ bool, failure error) {
	if root, e := runtimepaths.Open(raw); e == nil {
		return root, raw, true, nil
	} else if e.Code != "ROOT_UNAVAILABLE" {
		return nil, "", false, fail("DATA_HOME_UNAVAILABLE")
	}
	path := raw
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, "/", "\\")
	}
	volume := filepath.VolumeName(path)
	rest := strings.TrimSuffix(path[len(volume)+1:], string(filepath.Separator))
	parts := strings.Split(rest, string(filepath.Separator))
	prefix := volume + string(filepath.Separator) + parts[0]
	held, e := runtimepaths.Open(prefix)
	if e != nil {
		return nil, "", false, fail("DATA_HOME_UNAVAILABLE")
	}
	keep := false
	defer func() {
		if !keep {
			_ = held.Close()
		}
	}()
	for i := 1; i < len(parts); i++ {
		// Validates the whole remaining relative path, including names beyond a
		// first missing component, and checks existing link/collision boundaries.
		if _, e := held.Exists(strings.Join(parts[i:], "/")); e != nil {
			return nil, "", false, fail("DATA_HOME_UNAVAILABLE")
		}
		nextPath := prefix + string(filepath.Separator) + parts[i]
		next, e := runtimepaths.Open(nextPath)
		if e != nil {
			present, observed := held.Exists(parts[i])
			if e.Code != "ROOT_UNAVAILABLE" || observed != nil || present {
				return nil, "", false, fail("DATA_HOME_UNAVAILABLE")
			}
			keep = true
			return held, prefix, false, nil
		}
		if closeErr := held.Close(); closeErr != nil {
			_ = next.Close()
			return nil, "", false, fail("DATA_HOME_UNAVAILABLE")
		}
		held, prefix = next, nextPath
	}
	keep = true
	return held, prefix, true, nil
}

func validateCoreInputs(resources *runtimepaths.Root) error {
	groups, present, pathErr := resources.ReadFile(catalogstore.CoreGroupsPath, cataloginput.YAMLMaxInputBytes)
	if pathErr != nil || !present {
		return fail("SIDECAR_RESOURCE_INVALID")
	}
	if _, err := cataloginput.DecodeGroups(groups, true); err != nil {
		return fail("SIDECAR_SCHEMA_INITIALIZATION_FAILED")
	}
	skills, present, pathErr := resources.ReadFile(catalogstore.CoreSkillsPath, cataloginput.YAMLMaxInputBytes)
	if pathErr != nil || !present {
		return fail("SIDECAR_RESOURCE_INVALID")
	}
	if _, err := cataloginput.DecodeTerms(skills, true); err != nil {
		return fail("SIDECAR_SCHEMA_INITIALIZATION_FAILED")
	}
	// Cross-file graph/reconciliation validation needs the custom registry and
	// stays in Open, after bootstrap. Never pretend core must be independent.
	return nil
}
