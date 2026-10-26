package catalogwrite

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
)

// Error has a content-free default string. Registry and Load may contain private
// catalog labels; they are not safe to log wholesale. Committed means replacement
// succeeded, even if a later read or handle close failed. It never promises a
// rollback or invites an automatic retry with the old revision.
type Error struct {
	Stage     string
	Code      string
	Committed bool
	Registry  *catalog.Error
	Load      *catalogstore.Error
	// Model is the structured constructor validation surface. Like Registry,
	// locations can contain catalog labels; do not log it wholesale.
	Model *cataloginput.ValidationError
}

func (e *Error) Error() string { return "catalog mutation could not be completed" }

type GroupRecord struct {
	catalog.Group
	Origin string `json:"origin"`
}

type Result struct {
	Record   GroupRecord `json:"record"`
	Revision string      `json:"revision"`
}

// Service serializes calls through this instance and the process-wide operation
// gate shared with assessment cache publication. The host must still exclude
// noncooperating writers. Raw revision checks are not a cross-process CAS.
type Service struct {
	resourceRoot string
	dataRoot     string
	gate         chan struct{}
	read         func(*runtimepaths.Root, *runtimepaths.Root) (*catalogstore.State, *catalogstore.Error)
	encode       func([]catalog.Group) ([]byte, error)
	replace      func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error)
	now          func() time.Time
}

// New accepts explicit host-resolved existing roots, not cwd/environment/profile
// discovery. Opening/validating them is deferred until the operation, preserving
// unknown-field error precedence. It does not bootstrap or establish ACL policy.
func New(resourceRoot, dataRoot string) *Service {
	return &Service{resourceRoot: resourceRoot, dataRoot: dataRoot, gate: make(chan struct{}, 1),
		read: func(resources, data *runtimepaths.Root) (*catalogstore.State, *catalogstore.Error) {
			return catalogstore.ReadState(resources, data)
		}, encode: encodeGroups,
		replace: (*runtimepaths.Root).ReplaceFile, now: time.Now}
}

func registryError(code, class, message string) *Error {
	return &Error{Stage: "validation", Code: code,
		Registry: &catalog.Error{Class: class, Code: code, Message: message}}
}

func revisionConflict() *Error {
	return registryError("SKILL_REGISTRY_REVISION_CONFLICT", "SkillRegistryRevisionConflict",
		"Skills Registry changed after this editor loaded. Refresh and try again.")
}

func cancelled(ctx context.Context) *Error {
	if ctx.Err() != nil {
		return &Error{Stage: "cancelled", Code: "CATALOG_OPERATION_CANCELLED"}
	}
	return nil
}

// RenameCustomGroup ports update_custom_group. A successful response is verified
// against bytes actually saved. This single-file use case retains group id,
// archive/cascade fields and order; it never changes skills or evidence.
//
// Cancellation is cooperative between filesystem calls and while waiting for the
// service gate. It is checked before replacement, not used to abandon post-save
// verification or manufacture a cancellation after a committed write. The caller
// must not mutate payload concurrently and must supply a non-nil context.
func (s *Service) RenameCustomGroup(ctx context.Context, groupID string, payload map[string]any, expectedRevision string) (result *Result, failure *Error) {
	unknown := make([]string, 0)
	for key := range payload {
		if key != "name" {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) != 0 {
		sort.Strings(unknown)
		return nil, registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", "Unknown group fields: "+strings.Join(unknown, ", "))
	}
	if s == nil || s.gate == nil || ctx == nil {
		return nil, &Error{Stage: "configuration", Code: "CATALOG_SERVICE_UNAVAILABLE"}
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return nil, cancelled(ctx)
	}
	if e := cancelled(ctx); e != nil {
		return nil, e
	}
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return nil, &Error{Stage: "cancelled", Code: "CATALOG_OPERATION_CANCELLED"}
	}
	defer release()
	data, pathErr := runtimepaths.Open(s.dataRoot)
	if pathErr != nil {
		return nil, &Error{Stage: "path", Code: pathErr.Code}
	}
	committed := false
	defer func() {
		if err := data.Close(); err != nil && failure == nil {
			result = nil
			failure = &Error{Stage: "close", Code: "CATALOG_CLOSE_FAILED", Committed: committed}
		}
	}()
	if pending, e := data.Exists(datahome.RegistryRecoveryPath); e != nil || pending {
		return nil, &Error{Stage: "recovery", Code: "CATALOG_RECOVERY_REQUIRED"}
	}
	resources, pathErr := runtimepaths.Open(s.resourceRoot)
	if pathErr != nil {
		return nil, &Error{Stage: "path", Code: pathErr.Code}
	}
	defer func() {
		if err := resources.Close(); err != nil && failure == nil {
			result = nil
			failure = &Error{Stage: "close", Code: "CATALOG_CLOSE_FAILED", Committed: committed}
		}
	}()
	state, loadErr := s.read(resources, data)
	if loadErr != nil {
		return nil, &Error{Stage: "load", Code: "CATALOG_LOAD_FAILED", Load: loadErr}
	}
	if state.Snapshot.Revision() != expectedRevision {
		return nil, revisionConflict()
	}
	coreIDs := make(map[string]bool)
	for _, id := range state.Snapshot.CoreGroupIDs() {
		coreIDs[id] = true
	}
	if coreIDs[groupID] {
		return nil, registryError("CORE_GROUP_READ_ONLY", "SkillRegistryError", "Core skill groups are read-only.")
	}
	index := -1
	for i, group := range state.CustomGroups {
		if group.ID == groupID {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, registryError("SKILL_GROUP_NOT_FOUND", "SkillRegistryError", "Custom skill group was not found.")
	}
	name, ok := payload["name"].(string)
	if !ok || textcompat.Strip(name) == "" {
		return nil, registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", "name must be a non-empty string")
	}
	if !utf8.ValidString(name) {
		return nil, &Error{Stage: "input", Code: "CATALOG_NAME_UNICODE_UNSUPPORTED"}
	}
	name = textcompat.Strip(name)
	if utf8.RuneCountInString(name) > 160 {
		return nil, registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", "name must be 160 characters or fewer")
	}
	state.CustomGroups[index].Name = name
	updated := state.CustomGroups[index]
	groups := make([]catalog.Group, 0, len(coreIDs)+len(state.CustomGroups))
	for _, group := range state.Snapshot.Groups() {
		if coreIDs[group.ID] {
			groups = append(groups, group)
		}
	}
	groups = append(groups, state.CustomGroups...)
	if err := catalog.Validate(groups, state.Snapshot.Skills()); err != nil {
		var domain *catalog.Error
		if errors.As(err, &domain) {
			return nil, &Error{Stage: "validation", Code: domain.Code, Registry: domain}
		}
		return nil, &Error{Stage: "validation", Code: "CATALOG_VALIDATION_FAILED"}
	}
	encoded, err := s.encode(state.CustomGroups)
	if err != nil {
		return nil, &Error{Stage: "encode", Code: "CATALOG_ENCODING_FAILED"}
	}
	// The second precondition includes BOTH raw private catalogs. ReplaceFile
	// additionally checks the group's exact target bytes immediately before swap.
	currentGroups, _, pathErr := data.ReadFile(catalogstore.CustomGroupsPath, cataloginput.YAMLMaxInputBytes)
	if pathErr != nil {
		return nil, &Error{Stage: "precondition", Code: pathErr.Code}
	}
	currentSkills, _, pathErr := data.ReadFile(catalogstore.CustomSkillsPath, cataloginput.YAMLMaxInputBytes)
	if pathErr != nil {
		return nil, &Error{Stage: "precondition", Code: pathErr.Code}
	}
	if catalog.RawRevision(currentGroups, currentSkills) != expectedRevision {
		return nil, revisionConflict()
	}
	if e := cancelled(ctx); e != nil {
		return nil, e
	}
	committed, pathErr = s.replace(data, catalogstore.CustomGroupsPath, state.RawGroups, encoded)
	if pathErr != nil {
		if !committed && pathErr.Code == "FILE_CONFLICT" {
			return nil, revisionConflict()
		}
		return nil, &Error{Stage: "save", Code: pathErr.Code, Committed: committed}
	}
	if !committed {
		return nil, &Error{Stage: "save", Code: "CATALOG_REPLACEMENT_UNCONFIRMED"}
	}
	readback, loadErr := s.read(resources, data)
	if loadErr != nil {
		return nil, &Error{Stage: "readback", Code: "CATALOG_READBACK_FAILED", Committed: true, Load: loadErr}
	}
	if !bytes.Equal(readback.RawGroups, encoded) || !bytes.Equal(readback.RawSkills, currentSkills) {
		return nil, &Error{Stage: "readback", Code: "CATALOG_CHANGED_AFTER_SAVE", Committed: true}
	}
	// Source JSON always returns a list, not null, for absent cascade bookkeeping.
	updated.CascadeArchivedSkillIDs = append([]string{}, updated.CascadeArchivedSkillIDs...)
	return &Result{Record: GroupRecord{Group: updated, Origin: "custom"}, Revision: readback.Snapshot.Revision()}, nil
}
