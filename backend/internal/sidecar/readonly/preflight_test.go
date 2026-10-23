package readonly

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
)

func TestPreflightMissingSiblingAndNestedCandidateNeverCreates(t *testing.T) {
	base, start := fixture(t)
	for _, candidate := range []string{start.DataRoot, filepath.Join(base, "new-home"), filepath.Join(base, "new-parent", "new-home"), filepath.Join(base, "resources-sibling", "new-home")} {
		start.DataRoot = candidate
		before := tree(t, base)
		if err := Preflight(start); err != nil {
			t.Fatalf("candidate preflight failed: %v", err)
		}
		if !reflect.DeepEqual(before, tree(t, base)) {
			t.Fatal("preflight changed synthetic tree")
		}
	}
}

func TestPreflightRefusalsPreserveMissingCandidates(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*testing.T, *protocol.StartRecord)
		code string
	}{
		{"invalid_port", func(t *testing.T, s *protocol.StartRecord) { s.Port = 1 }, "SIDECAR_START_INVALID_PORT"},
		{"overlap_missing", func(t *testing.T, s *protocol.StartRecord) { s.DataRoot = filepath.Join(s.ResourceRoot, "new", "home") }, "SIDECAR_PATH_BOUNDARY_INVALID"},
		{"missing_resource", func(t *testing.T, s *protocol.StartRecord) { s.ResourceRoot += "-absent" }, "SIDECAR_RESOURCE_INVALID"},
		{"missing_static", func(t *testing.T, s *protocol.StartRecord) { removeFixture(t, s.ResourceRoot, "src/intake_ui_static") }, "SIDECAR_RESOURCE_INVALID"},
		{"invalid_version", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.ResourceRoot, "VERSION", "not-a-version\n")
		}, "SIDECAR_RESOURCE_INVALID"},
		{"missing_core", func(t *testing.T, s *protocol.StartRecord) {
			removeFixture(t, s.ResourceRoot, catalogstore.CoreSkillsPath)
		}, "SIDECAR_RESOURCE_INVALID"},
		{"groups_yaml", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.ResourceRoot, catalogstore.CoreGroupsPath, "[unclosed")
		}, "SIDECAR_SCHEMA_INITIALIZATION_FAILED"},
		{"groups_model", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.ResourceRoot, catalogstore.CoreGroupsPath, "- {name: missing-id}\n")
		}, "SIDECAR_SCHEMA_INITIALIZATION_FAILED"},
		{"skills_yaml", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.ResourceRoot, catalogstore.CoreSkillsPath, "[unclosed")
		}, "SIDECAR_SCHEMA_INITIALIZATION_FAILED"},
		{"skills_model", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.ResourceRoot, catalogstore.CoreSkillsPath, "- {id: missing-category, name: Example}\n")
		}, "SIDECAR_SCHEMA_INITIALIZATION_FAILED"},
		{"skills_bound", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, s.ResourceRoot, catalogstore.CoreSkillsPath, strings.Repeat(" ", cataloginput.YAMLMaxInputBytes+1))
		}, "SIDECAR_RESOURCE_INVALID"},
		{"file_parent", func(t *testing.T, s *protocol.StartRecord) {
			writeFixture(t, filepath.Dir(s.ResourceRoot), "not-a-directory", "opaque")
			s.DataRoot = filepath.Join(filepath.Dir(s.ResourceRoot), "not-a-directory", "missing")
		}, "DATA_HOME_UNAVAILABLE"},
		{"invalid_missing_component", func(t *testing.T, s *protocol.StartRecord) { s.DataRoot += string(filepath.Separator) + "CON" }, "DATA_HOME_UNAVAILABLE"},
		{"dot_component", func(t *testing.T, s *protocol.StartRecord) {
			s.DataRoot += string(filepath.Separator) + ".." + string(filepath.Separator) + "other"
		}, "DATA_HOME_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, start := fixture(t)
			start.DataRoot = filepath.Join(base, "missing-parent", "missing-home")
			test.edit(t, &start)
			before := tree(t, base)
			requireCode(t, Preflight(start), test.code)
			if !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("failed preflight changed files")
			}
		})
	}
}

func TestPreflightDriveOnlyAncestorIsReadOnlyRefusal(t *testing.T) {
	base, start := fixture(t)
	// Only read/stat this randomly named absent candidate; never create a fixture
	// at the drive root. Actual writable fixtures remain inside t.TempDir.
	start.DataRoot = filepath.Join(filepath.VolumeName(base)+string(filepath.Separator), "careerhub-uncreated-"+rand.Text())
	if _, err := os.Lstat(start.DataRoot); !os.IsNotExist(err) {
		t.Fatal("expected absent synthetic candidate")
	}
	requireCode(t, Preflight(start), "DATA_HOME_UNAVAILABLE")
	if _, err := os.Lstat(start.DataRoot); !os.IsNotExist(err) {
		t.Fatal("drive-root-only candidate was created")
	}
}

func TestPreflightLeavesCombinedGraphAndPrivateInputForOpen(t *testing.T) {
	base, start := fixture(t)
	writeFixture(t, start.ResourceRoot, catalogstore.CoreSkillsPath, "- {id: core, name: Core, category: tool, parent: custom}\n")
	writeFixture(t, start.DataRoot, catalogstore.CustomSkillsPath, "- {id: custom, name: Custom, category: tool}\n")
	before := tree(t, base)
	if err := Preflight(start); err != nil {
		t.Fatal("preflight introduced independent-core graph rule")
	}
	if _, err := Open(start); err != nil {
		t.Fatalf("valid combined graph rejected: %v", err)
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("graph checks changed files")
	}
	writeFixture(t, start.DataRoot, catalogstore.CustomSkillsPath, "[private malformed")
	if err := Preflight(start); err != nil {
		t.Fatal("preflight interpreted private custom content")
	}
	_, err := Open(start)
	requireCode(t, err, "SIDECAR_SCHEMA_INITIALIZATION_FAILED")
}

func TestPreflightMissingDescendantRejectsLinkedAncestor(t *testing.T) {
	base, start := fixture(t)
	link := filepath.Join(base, "linked-resources")
	if err := os.Symlink(start.ResourceRoot, link); err != nil {
		t.Skip("native symlink creation unavailable")
	}
	start.DataRoot = filepath.Join(link, "missing-child")
	requireCode(t, Preflight(start), "DATA_HOME_UNAVAILABLE")
	if _, err := os.Lstat(filepath.Join(start.ResourceRoot, "missing-child")); !os.IsNotExist(err) {
		t.Fatal("linked candidate created an outside child")
	}
}
