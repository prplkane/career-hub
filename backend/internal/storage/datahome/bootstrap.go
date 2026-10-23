package datahome

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"runtime"
	"time"

	"careerhub.local/backend/internal/storage/datahome/privacy"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// BootstrapError reports an incomplete startup without promising rollback.
// MayHaveChanged includes directory creation, ACL dispatch, file publication and
// the transient writable probe. Codes contain no paths, SIDs or native prose.
type BootstrapError struct {
	Stage          string
	Code           string
	CauseCode      string
	MayHaveChanged bool
}

func (e *BootstrapError) Error() string { return "data home bootstrap could not be completed" }

var bootstrapDirectories = [...]string{
	"applications", "career_vault", "documents", "jobs", "outputs", "resume_versions",
	"templates", "logs", "security-reports", "backups", "secrets",
}

type bootstrapDefault struct{ path, content string }

// Byte-for-byte defaults, not reserialized YAML: the importer hashes these to
// distinguish an untouched application default from user-owned content.
var bootstrapDefaults = [...]bootstrapDefault{
	{"career_vault/application_demographics.yaml", "schema_version: 1\ncountries:\n  US:\n    gender: ''\n    hispanic_latino: ''\n    veteran_status: ''\n    disability_status: ''\n"},
	{"career_vault/application_helper.yaml", "schema_version: 2\nenabled_fields: []\n"},
	{"career_vault/hard_blocker_policy.yaml", "schema_version: 1\nclasses:\n  citizenship_work_authorization: true\n  active_clearance: true\n  minimum_experience_years: true\n"},
	{"career_vault/taxonomy/skill_groups.yaml", "- id: user_my_custom_skills\n  name: My custom skills\n  archived: false\n"},
	{"career_vault/taxonomy/technologies.yaml", "[]\n"},
}

func validChannel(channel string) bool {
	return channel == "dev" || channel == "stable"
}

// MarkerBytes matches data_home_marker_bytes, including sorted keys and final LF.
func MarkerBytes(channel, id string) ([]byte, *Error) {
	if !validChannel(channel) {
		return nil, fail("INVALID_CHANNEL")
	}
	if !validHomeID(id) {
		return nil, fail("INVALID_DATA_HOME")
	}
	return []byte(fmt.Sprintf("{\"application\":\"CareerHub\",\"channel\":%q,\"data_home_id\":%q,\"format_version\":2}\n", channel, id)), nil
}

func newHomeID() (string, error) {
	var id [16]byte
	if _, err := io.ReadFull(rand.Reader, id[:]); err != nil {
		return "", err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return "dh_" + hex.EncodeToString(id[:]), nil
}

type bootstrapOperations struct {
	apply  func(context.Context, string) (bool, *privacy.Error)
	verify func(context.Context, string) *privacy.Error
	// verifyRoot re-checks an existing Home's root DACL and owner on every
	// start (ADR SEC-250); nil only in focused tests that do not exercise it.
	verifyRoot func(context.Context, string) *privacy.Error
	seed       func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
	probe      func(*runtimepaths.Root) *runtimepaths.Error
	newID      func() (string, error)
	claim      func(*runtimepaths.Root) *runtimepaths.Error
}

// Bootstrap creates only the source's empty application-owned structure. The
// production provider is Windows-only until other native permission/publication
// providers pass acceptance. It never offers a permissive skip-ACL flag.
//
// Caller must exclude other backend writers for the entire startup. A marker,
// no-replace publication and these point-in-time checks are not a writer lease.
// Existing initialized Windows homes keep their ACLs and opaque private files;
// their root DACL and owner are re-verified on every start.
// This function is not yet connected to the installed sidecar.
func Bootstrap(ctx context.Context, root, channel string) (*Info, *BootstrapError) {
	return bootstrap(ctx, root, channel, defaultBootstrapOperations())
}

// BootstrapForProcess claims this home before any private-file inspection,
// permission enforcement or seeding. Ownership survives errors/cancellation
// until OS process exit. Use only in a dedicated process that exits after Run.
func BootstrapForProcess(ctx context.Context, root, channel string) (*Info, *BootstrapError) {
	ops := defaultBootstrapOperations()
	ops.claim = (*runtimepaths.Root).ClaimForProcess
	return bootstrap(ctx, root, channel, ops)
}

func defaultBootstrapOperations() bootstrapOperations {
	return bootstrapOperations{
		apply: privacy.ApplyPrivateTree, verify: privacy.VerifyPrivateTree, verifyRoot: privacy.VerifyPrivateRoot,
		seed: (*runtimepaths.Root).CreateFile, probe: (*runtimepaths.Root).ProbeWritable,
		newID: newHomeID,
	}
}

func bootstrap(ctx context.Context, root, channel string, ops bootstrapOperations) (info *Info, failure *BootstrapError) {
	if !validChannel(channel) {
		return nil, &BootstrapError{Stage: "channel", Code: "INVALID_CHANNEL"}
	}
	if ctx == nil {
		return nil, &BootstrapError{Stage: "cancelled", Code: "DATA_HOME_STARTUP_CANCELLED"}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return nil, &BootstrapError{Stage: "cancelled", Code: "DATA_HOME_STARTUP_CANCELLED"}
	}
	if runtime.GOOS != "windows" {
		return nil, &BootstrapError{Stage: "platform", Code: "DATA_HOME_BOOTSTRAP_UNSUPPORTED"}
	}
	held, changed, pathErr := runtimepaths.OpenOrCreate(root)
	problem := func(stage, code, cause string) *BootstrapError {
		return &BootstrapError{Stage: stage, Code: code, CauseCode: cause, MayHaveChanged: changed}
	}
	if pathErr != nil {
		return nil, problem("root", "DATA_HOME_UNWRITABLE", pathErr.Code)
	}
	defer func() {
		if err := held.Close(); err != nil && failure == nil {
			info, failure = nil, problem("close", "DATA_HOME_UNAVAILABLE", "ROOT_CLOSE")
		}
	}()
	checkContext := func() *BootstrapError {
		if ctx.Err() != nil {
			return problem("cancelled", "DATA_HOME_STARTUP_CANCELLED", "")
		}
		return nil
	}
	if ops.claim != nil {
		if e := checkContext(); e != nil {
			return nil, e
		}
		if e := ops.claim(held); e != nil {
			code := "DATA_HOME_OWNERSHIP_UNAVAILABLE"
			switch e.Code {
			case "OWNERSHIP_IN_USE":
				code = "DATA_HOME_IN_USE"
			case "OWNERSHIP_UNSUPPORTED":
				code = "DATA_HOME_OWNERSHIP_UNSUPPORTED"
			}
			return nil, problem("ownership", code, e.Code)
		}
		if e := checkContext(); e != nil {
			return nil, e
		}
	}
	marked, pathErr := held.Exists(MarkerPath)
	if pathErr != nil {
		return nil, problem("marker", "DATA_HOME_UNAVAILABLE", pathErr.Code)
	}
	if marked {
		if _, e := inspect(held, &channel); e != nil {
			return nil, problem("admission", e.Code, e.Unsupported)
		}
		// An existing Home keeps its ACLs, so confirm before any write that they
		// were not loosened and the owner is still this user; fail closed.
		if ops.verifyRoot != nil {
			if e := ops.verifyRoot(ctx, root); e != nil {
				return nil, problem("permissions", e.Code, "")
			}
		}
	} else {
		// Safety correction: source can discover the nested document sentinel only
		// after seeding and publishing the marker. Recovery must win before writes.
		for _, sentinel := range orderedRecoverySentinels {
			exists, e := held.Exists(sentinel.path)
			if e != nil {
				return nil, problem("recovery", "DATA_HOME_UNAVAILABLE", e.Code)
			}
			if exists {
				return nil, problem("recovery", "DATA_HOME_RECOVERY_REQUIRED", "")
			}
		}
		names, e := held.ListNames()
		if e != nil {
			return nil, problem("partial", "DATA_HOME_UNREADABLE", e.Code)
		}
		allowed := map[string]bool{MarkerPath: true}
		for _, directory := range bootstrapDirectories {
			allowed[directory] = true
		}
		for _, name := range names {
			if !allowed[name] {
				return nil, problem("partial", "PARTIAL_DATA_HOME", "")
			}
		}
	}
	// Existing opaque bytes are preserved, not parsed or reset. Directory leaves,
	// aliases and links are refused rather than treated as a present default file.
	for _, item := range bootstrapDefaults {
		if e := held.Check(item.path); e != nil {
			return nil, problem("default_path", "DATA_HOME_UNWRITABLE", e.Code)
		}
	}
	if e := checkContext(); e != nil {
		return nil, e
	}
	if !marked {
		mayChange, e := ops.apply(ctx, root)
		changed = changed || mayChange
		if e != nil {
			return nil, problem("permissions", e.Code, "")
		}
	}
	for _, directory := range bootstrapDirectories {
		if e := checkContext(); e != nil {
			return nil, e
		}
		mayChange, e := held.EnsureDirectory(directory)
		changed = changed || mayChange
		if e != nil {
			return nil, problem("directories", "DATA_HOME_UNWRITABLE", e.Code)
		}
	}
	for _, item := range bootstrapDefaults {
		if e := checkContext(); e != nil {
			return nil, e
		}
		mayChange, e := held.EnsureDirectory(path.Dir(item.path))
		changed = changed || mayChange
		if e != nil {
			return nil, problem("directories", "DATA_HOME_UNWRITABLE", e.Code)
		}
		present, e := held.Exists(item.path)
		if e != nil {
			return nil, problem("default_path", "DATA_HOME_UNWRITABLE", e.Code)
		}
		if present {
			if e := held.Check(item.path); e != nil {
				return nil, problem("default_path", "DATA_HOME_UNWRITABLE", e.Code)
			}
			continue
		}
		changed = true // A failed seed may have created an owned staging entry.
		created, e := ops.seed(held, item.path, []byte(item.content))
		changed = changed || created
		if e != nil {
			code := "DATA_HOME_UNWRITABLE"
			if e.Code == "FILE_EXISTS" {
				code = "DEFAULT_CONFLICT"
			}
			return nil, problem("defaults", code, e.Code)
		}
	}
	if !marked {
		if e := checkContext(); e != nil {
			return nil, e
		}
		// Verify actual inherited permissions of newly created descendants too,
		// before declaring initialization complete via the marker.
		if e := ops.verify(ctx, root); e != nil {
			return nil, problem("permissions", e.Code, "")
		}
		id, identityErr := ops.newID()
		if identityErr != nil {
			return nil, problem("identity", "DATA_HOME_ID_UNAVAILABLE", "")
		}
		content, markerErr := MarkerBytes(channel, id)
		if markerErr != nil {
			return nil, problem("identity", markerErr.Code, "")
		}
		if e := checkContext(); e != nil {
			return nil, e
		}
		created, e := ops.seed(held, MarkerPath, content)
		changed = changed || created
		if e != nil {
			code := "DATA_HOME_UNWRITABLE"
			if e.Code == "FILE_EXISTS" {
				code = "DEFAULT_CONFLICT"
			}
			return nil, problem("marker", code, e.Code)
		}
	}
	if e := checkContext(); e != nil {
		return nil, e
	}
	changed = true // The probe itself creates/removes an owned temporary entry.
	if e := ops.probe(held); e != nil {
		return nil, problem("probe", "DATA_HOME_UNWRITABLE", e.Code)
	}
	result, e := inspect(held, &channel)
	if e != nil {
		return nil, problem("readback", e.Code, e.Unsupported)
	}
	return result, nil
}
