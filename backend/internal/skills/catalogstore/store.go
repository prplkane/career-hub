// Package catalogstore composes read-only managed files with the catalog input
// and effective-registry boundaries. It never bootstraps a Data Home or writes.
package catalogstore

import (
	"errors"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const (
	CoreGroupsPath   = "src/data/skill_groups.yaml"
	CoreSkillsPath   = "src/data/job_technologies.yaml"
	CustomGroupsPath = "career_vault/taxonomy/skill_groups.yaml"
	CustomSkillsPath = "career_vault/taxonomy/technologies.yaml"
)

// Error identifies one failed boundary without retaining an absolute path or OS
// error prose. Input model locations and registry messages can contain catalog
// text: the structured payload is not a generally redacted diagnostic record.
// Stage is path, read, input or registry; Source is one fixed role label.
type Error struct {
	Stage    string
	Source   string
	Code     string
	Input    *cataloginput.DecodeError
	Registry *catalog.Error
}

func (e *Error) Error() string { return "catalog snapshot could not be loaded" }

type reader interface {
	Check(string) *runtimepaths.Error
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}

// State retains the raw private inputs used to build Snapshot. These are owned
// copies for one operation, not a live transaction. Mutation services must write
// CustomGroups, never Snapshot.Groups(): effective groups include reconciled core
// records that must not be promoted into the user's private file.
type State struct {
	Snapshot     *catalog.Snapshot
	CustomGroups []catalog.Group
	CustomSkills []catalog.Term
	CoreGroups   []catalog.Group
	CoreSkills   []catalog.Term
	RawGroups    []byte
	RawSkills    []byte
}

// ReadState borrows managed readers. Internal composition can wrap an open root
// to observe the exact read set; it cannot change the fixed paths or limits.
// It neither closes nor writes them. The caller owns writer synchronization.
func ReadState(resources, data reader) (*State, *Error) {
	return loadState(resources, data)
}

// Load consumes explicit, existing host-resolved roots. It does not infer paths
// from cwd, an environment variable, a private marker or the installed profile.
// Resource and data roots may coincide for an explicitly selected legacy dev
// layout; each file still belongs to exactly one role. Production selects them
// separately. Caller must prevent concurrent cross-file writer transactions.
func Load(resourceRoot, dataRoot string) (snapshot *catalog.Snapshot, failure *Error) {
	data, err := runtimepaths.Open(dataRoot)
	if err != nil {
		return nil, &Error{Stage: "path", Source: "data_root", Code: err.Code}
	}
	defer func() {
		if err := data.Close(); err != nil && failure == nil {
			snapshot = nil
			failure = &Error{Stage: "read", Source: "data_root", Code: "CATALOG_CLOSE_FAILED"}
		}
	}()
	resources, err := runtimepaths.Open(resourceRoot)
	if err != nil {
		return nil, &Error{Stage: "path", Source: "resource_root", Code: err.Code}
	}
	defer func() {
		if err := resources.Close(); err != nil && failure == nil {
			snapshot = nil
			failure = &Error{Stage: "read", Source: "resource_root", Code: "CATALOG_CLOSE_FAILED"}
		}
	}()
	return load(resources, data)
}

func load(resources, data reader) (*catalog.Snapshot, *Error) {
	state, failure := loadState(resources, data)
	if failure != nil {
		return nil, failure
	}
	return state.Snapshot, nil
}

func loadState(resources, data reader) (*State, *Error) {
	// Python resolves both managed custom paths before attempting any catalog
	// decoding. Preserve that ordering even if the first core file is malformed.
	for _, entry := range []struct{ path, role string }{
		{CustomGroupsPath, "custom_groups"}, {CustomSkillsPath, "custom_skills"},
	} {
		if err := data.Check(entry.path); err != nil {
			return nil, &Error{Stage: "path", Source: entry.role, Code: err.Code}
		}
	}
	coreGroups, _, failure := groups(resources, CoreGroupsPath, "core_groups")
	if failure != nil {
		return nil, failure
	}
	customGroups, rawGroups, failure := groups(data, CustomGroupsPath, "custom_groups")
	if failure != nil {
		return nil, failure
	}
	coreSkills, _, failure := skills(resources, CoreSkillsPath, "core_skills")
	if failure != nil {
		return nil, failure
	}
	customSkills, rawSkills, failure := skills(data, CustomSkillsPath, "custom_skills")
	if failure != nil {
		return nil, failure
	}
	// Exact owned custom bytes already used for decoding feed the raw revision;
	// do not reopen files after model validation to compute a different revision.
	snapshot, buildErr := catalog.Build(coreGroups, customGroups, coreSkills, customSkills, rawGroups, rawSkills)
	if buildErr != nil {
		var domain *catalog.Error
		if errors.As(buildErr, &domain) {
			return nil, &Error{Stage: "registry", Registry: domain}
		}
		return nil, &Error{Stage: "registry", Code: "CATALOG_BUILD_FAILED"}
	}
	return &State{Snapshot: snapshot, CustomGroups: customGroups, CustomSkills: customSkills,
		CoreGroups: coreGroups, CoreSkills: coreSkills, RawGroups: rawGroups, RawSkills: rawSkills}, nil
}

func groups(source reader, path, role string) ([]catalog.Group, []byte, *Error) {
	raw, present, err := source.ReadFile(path, cataloginput.YAMLMaxInputBytes)
	if err != nil {
		return nil, nil, &Error{Stage: "read", Source: role, Code: err.Code}
	}
	if !present {
		return nil, nil, nil
	}
	values, inputErr := cataloginput.DecodeGroups(raw, true)
	if inputErr != nil {
		return nil, nil, &Error{Stage: "input", Source: role, Input: inputErr}
	}
	return values, raw, nil
}

func skills(source reader, path, role string) ([]catalog.Term, []byte, *Error) {
	raw, present, err := source.ReadFile(path, cataloginput.YAMLMaxInputBytes)
	if err != nil {
		return nil, nil, &Error{Stage: "read", Source: role, Code: err.Code}
	}
	if !present {
		return nil, nil, nil
	}
	values, inputErr := cataloginput.DecodeTerms(raw, true)
	if inputErr != nil {
		return nil, nil, &Error{Stage: "input", Source: role, Input: inputErr}
	}
	return values, raw, nil
}
