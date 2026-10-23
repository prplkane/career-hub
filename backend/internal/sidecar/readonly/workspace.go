// Package readonly composes initialized Data Home admission and fixed resource
// catalogs. It does not bootstrap, write, discover profiles, or own a server.
package readonly

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const maxVersionBytes = 1024

var versionPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// Error contains only an allowlisted public category. No nested catalog error,
// native cause, path, identity, or input value is retained or formatted.
type Error struct{ Code string }

func (e *Error) Error() string              { return "Read-only workspace validation failed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func fail(code string) error                { return &Error{Code: code} }

// Workspace retains the initially admitted identity and application version,
// not filesystem handles or a cached registry. Its methods do not mutate state.
type Workspace struct {
	start       protocol.StartRecord
	appVersion  string
	dataHomeID  string
	startupHome datahome.Info
}

// Open validates both fixed resource and initialized data inputs, including an
// initial effective registry, before the process can publish readiness.
func Open(start protocol.StartRecord) (*Workspace, error) {
	return open(start, nil)
}

// OpenForProcess adds irreversible process-lifetime ownership before reading
// private marker/catalog files. The caller must exit after its sidecar Run.
func OpenForProcess(start protocol.StartRecord) (*Workspace, error) {
	return open(start, (*runtimepaths.Root).ClaimForProcess)
}

func open(start protocol.StartRecord, claim func(*runtimepaths.Root) *runtimepaths.Error) (*Workspace, error) {
	if err := validateStart(start); err != nil {
		return nil, err
	}
	version, err := validateRootsWithClaim(start, claim)
	if err != nil {
		return nil, err
	}
	w := &Workspace{start: start, appVersion: version}
	_, identity, err := w.readRegistry("")
	if err != nil {
		return nil, err
	}
	w.dataHomeID = identity
	// Capture the complete marker while still in startup admission. Later
	// requests must compare against this identity, never promote a fresh read
	// into a new baseline.
	home, admission := datahome.Inspect(start.DataRoot, &start.Channel)
	if admission != nil || home == nil || home.DataHomeID != identity {
		return nil, fail("DATA_HOME_IDENTITY_CHANGED")
	}
	w.startupHome = *home
	return w, nil
}

func validateStart(start protocol.StartRecord) error {
	if start.Port != 0 {
		return fail("SIDECAR_START_INVALID_PORT")
	}
	if start.Channel != "dev" && start.Channel != "stable" {
		return fail("SIDECAR_START_INVALID_CHANNEL")
	}
	for _, path := range []string{start.ResourceRoot, start.DataRoot} {
		if !utf8.ValidString(path) || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || len(path) > runtimepaths.MaxPathBytes {
			return fail("SIDECAR_START_INVALID_PATH")
		}
	}
	return nil
}

// AppVersion is the initially validated explicit resource VERSION, not proof of
// installed executable identity. It is not reread for every health response.
func (w *Workspace) AppVersion() string {
	if w == nil {
		return ""
	}
	return w.appVersion
}

// StartupHome returns an immutable copy of the initially admitted marker.
// It performs no request-time marker read and grants no new filesystem access.
func (w *Workspace) StartupHome() (datahome.Info, error) {
	if w == nil || w.dataHomeID == "" || !w.startupHome.Initialized {
		return datahome.Info{}, fail("DATA_HOME_UNAVAILABLE")
	}
	return w.startupHome, nil
}

// MatchesStart prevents a host coordinator from inspecting one Workspace and
// publishing into another root or channel with a copied Home marker.
func (w *Workspace) MatchesStart(start protocol.StartRecord) bool {
	if w == nil || w.dataHomeID == "" {
		return false
	}
	// Home and resource identity only: the window token (ADR SEC-250) is a
	// per-process secret, not part of which Data Home this is.
	mine, theirs := w.start, start
	mine.SessionToken, theirs.SessionToken = "", ""
	return mine == theirs
}

// CheckAdmission rechecks boundaries and the identity/channel/recovery state
// bound when this workspace opened. It does not reload catalogs, mutate state,
// reserve the namespace or replace process ownership. Startup uses it around
// explicit preparation, including after that service finishes native cleanup.
func (w *Workspace) CheckAdmission() error {
	if w == nil || w.dataHomeID == "" {
		return fail("DATA_HOME_UNAVAILABLE")
	}
	if _, err := validateRoots(w.start); err != nil {
		return err
	}
	info, err := datahome.Inspect(w.start.DataRoot, &w.start.Channel)
	if err != nil {
		return admissionError(err)
	}
	if info == nil || !info.Initialized {
		return fail("DATA_HOME_UNAVAILABLE")
	}
	if info.DataHomeID != w.dataHomeID {
		return fail("DATA_HOME_IDENTITY_CHANGED")
	}
	return nil
}

// Registry returns a fresh independently owned catalog payload. Admission is
// checked before and after loading, but these reads are not a transaction/lock.
// The HTTP/process owner must coordinate writers and bound the operation.
func (w *Workspace) Registry() (map[string]any, error) {
	if w == nil || w.dataHomeID == "" {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if _, err := validateRoots(w.start); err != nil {
		return nil, err
	}
	payload, _, err := w.readRegistry(w.dataHomeID)
	return payload, err
}

func (w *Workspace) readRegistry(expectedID string) (map[string]any, string, error) {
	return admittedRegistry(expectedID,
		func() (*datahome.Info, *datahome.Error) { return datahome.Inspect(w.start.DataRoot, &w.start.Channel) },
		func() (*catalog.Snapshot, *catalogstore.Error) {
			return catalogstore.Load(w.start.ResourceRoot, w.start.DataRoot)
		})
}

// The private seam tests changes between observations without races or writes
// outside synthetic fixtures. No alternative backend is publicly configurable.
func admittedRegistry(expectedID string, inspect func() (*datahome.Info, *datahome.Error), load func() (*catalog.Snapshot, *catalogstore.Error)) (map[string]any, string, error) {
	before, err := inspect()
	if err != nil {
		return nil, "", admissionError(err)
	}
	if before == nil || !before.Initialized {
		return nil, "", fail("DATA_HOME_UNAVAILABLE")
	}
	if expectedID != "" && before.DataHomeID != expectedID {
		return nil, "", fail("DATA_HOME_IDENTITY_CHANGED")
	}
	snapshot, loadErr := load()
	if loadErr != nil || snapshot == nil {
		return nil, "", fail("SIDECAR_SCHEMA_INITIALIZATION_FAILED")
	}
	after, err := inspect()
	if err != nil {
		return nil, "", admissionError(err)
	}
	if after == nil || !after.Initialized {
		return nil, "", fail("DATA_HOME_UNAVAILABLE")
	}
	if before.DataHomeID != after.DataHomeID || before.Channel != after.Channel || before.FormatVersion != after.FormatVersion {
		return nil, "", fail("DATA_HOME_IDENTITY_CHANGED")
	}
	return snapshot.Payload(), before.DataHomeID, nil
}

func admissionError(err *datahome.Error) error {
	switch err.Code {
	case "DATA_HOME_UNAVAILABLE", "INVALID_DATA_HOME", "DATA_HOME_NEWER_THAN_APP", "DATA_HOME_MIGRATION_REQUIRED", "DATA_HOME_RECOVERY_REQUIRED", "DATA_HOME_CHANNEL_MISMATCH", "DATA_HOME_INPUT_UNSUPPORTED":
		return fail(err.Code)
	default:
		return fail("DATA_HOME_UNAVAILABLE")
	}
}

func validateRoots(start protocol.StartRecord) (version string, failure error) {
	return validateRootsWithClaim(start, nil)
}

func validateRootsWithClaim(start protocol.StartRecord, claim func(*runtimepaths.Root) *runtimepaths.Error) (version string, failure error) {
	if lexicalOverlap(start.ResourceRoot, start.DataRoot) {
		return "", fail("SIDECAR_PATH_BOUNDARY_INVALID")
	}
	resources, pathErr := runtimepaths.Open(start.ResourceRoot)
	if pathErr != nil {
		return "", fail("SIDECAR_RESOURCE_INVALID")
	}
	defer func() {
		if err := resources.Close(); err != nil && failure == nil {
			version, failure = "", fail("SIDECAR_RESOURCE_INVALID")
		}
	}()
	data, pathErr := runtimepaths.Open(start.DataRoot)
	if pathErr != nil {
		return "", fail("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		if err := data.Close(); err != nil && failure == nil {
			version, failure = "", fail("DATA_HOME_UNAVAILABLE")
		}
	}()
	// Managed opens reject link/reparse components first. Identity comparison of
	// only the configured roots and their ancestors also catches native aliases
	// (for example short Windows names or case-insensitive macOS roots).
	if overlaps, err := identityOverlap(start.ResourceRoot, start.DataRoot); err != nil || overlaps {
		return "", fail("SIDECAR_PATH_BOUNDARY_INVALID")
	}
	version, err := validateResources(resources)
	if err != nil {
		return "", err
	}
	if claim != nil {
		if e := claim(data); e != nil {
			switch e.Code {
			case "OWNERSHIP_IN_USE":
				return "", fail("DATA_HOME_IN_USE")
			case "OWNERSHIP_UNSUPPORTED":
				return "", fail("DATA_HOME_OWNERSHIP_UNSUPPORTED")
			default:
				return "", fail("DATA_HOME_OWNERSHIP_UNAVAILABLE")
			}
		}
	}
	return version, nil
}

func validateResources(resources *runtimepaths.Root) (string, error) {
	for _, path := range []string{"src/intake_ui_static", catalogstore.CoreGroupsPath, catalogstore.CoreSkillsPath} {
		present, err := resources.Exists(path)
		if err != nil || !present {
			return "", fail("SIDECAR_RESOURCE_INVALID")
		}
		if path != "src/intake_ui_static" && resources.Check(path) != nil {
			return "", fail("SIDECAR_RESOURCE_INVALID")
		}
	}
	raw, present, pathErr := resources.ReadFile("VERSION", maxVersionBytes)
	if pathErr != nil || !present {
		return "", fail("SIDECAR_RESOURCE_INVALID")
	}
	return parseVersion(raw)
}

func parseVersion(raw []byte) (string, error) {
	if len(raw) > maxVersionBytes {
		return "", fail("SIDECAR_RESOURCE_INVALID")
	}
	for _, b := range raw {
		if b > 127 {
			return "", fail("SIDECAR_RESOURCE_INVALID")
		}
	}
	// Python read_text uses universal newlines even with explicit ASCII.
	text := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
	version := strings.TrimSuffix(text, "\n")
	if text != version+"\n" || !versionPattern.MatchString(version) {
		return "", fail("SIDECAR_RESOURCE_INVALID")
	}
	return version, nil
}

func lexicalOverlap(a, b string) bool {
	// Cleaning is used only for refusal, never to turn an invalid configured
	// path into one that bypasses runtimepaths' component validation.
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func identityOverlap(a, b string) (bool, error) {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		if overlaps, err := identityWithin(pair[0], pair[1]); err != nil || overlaps {
			return overlaps, err
		}
	}
	return false, nil
}

func identityWithin(rootPath, descendant string) (bool, error) {
	root, err := os.Stat(rootPath)
	if err != nil {
		return false, err
	}
	for path := filepath.Clean(descendant); ; path = filepath.Dir(path) {
		candidate, err := os.Stat(path)
		if err != nil {
			return false, err
		}
		if os.SameFile(root, candidate) {
			return true, nil
		}
		if parent := filepath.Dir(path); parent == path {
			break
		}
	}
	return false, nil
}
