package catalogstore

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type fakeReader struct {
	files     map[string][]byte
	checks    map[string]*runtimepaths.Error
	reads     map[string]*runtimepaths.Error
	trace     *[]string
	afterRead func(string)
}

func (r *fakeReader) Check(path string) *runtimepaths.Error {
	*r.trace = append(*r.trace, "check:"+path)
	return r.checks[path]
}

func (r *fakeReader) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	*r.trace = append(*r.trace, "read:"+path)
	if limit != cataloginput.YAMLMaxInputBytes {
		panic("unbounded catalog read")
	}
	if err := r.reads[path]; err != nil {
		return nil, false, err
	}
	value, present := r.files[path]
	owned := bytes.Clone(value)
	if r.afterRead != nil {
		r.afterRead(path)
	}
	return owned, present, nil
}

func TestLoadChecksBothCustomPathsBeforeOrderedReads(t *testing.T) {
	trace := []string{}
	r := &fakeReader{trace: &trace}
	snapshot, err := load(r, r)
	if err != nil || len(snapshot.Groups()) != len(catalog.DefaultGroups()) || len(snapshot.Skills()) != 0 {
		t.Fatalf("empty snapshot: %v", err)
	}
	expected := []string{"check:" + CustomGroupsPath, "check:" + CustomSkillsPath,
		"read:" + CoreGroupsPath, "read:" + CustomGroupsPath, "read:" + CoreSkillsPath, "read:" + CustomSkillsPath}
	if !reflect.DeepEqual(trace, expected) {
		t.Fatalf("read order: %v", trace)
	}
	trace = nil
	r.checks = map[string]*runtimepaths.Error{CustomSkillsPath: {Code: "SYNTHETIC_PATH_FAILURE"}}
	r.files = map[string][]byte{CoreGroupsPath: []byte("[")}
	if snapshot, err = load(r, r); snapshot != nil || err == nil || err.Stage != "path" || err.Source != "custom_skills" {
		t.Fatalf("path error precedence: %v", err)
	}
	if len(trace) != 2 {
		t.Fatalf("catalog read after path failure: %v", trace)
	}
}

func TestLoadFailureStagesNeverReturnPartialSnapshots(t *testing.T) {
	for _, entry := range []struct{ path, role string }{
		{CoreGroupsPath, "core_groups"}, {CustomGroupsPath, "custom_groups"},
		{CoreSkillsPath, "core_skills"}, {CustomSkillsPath, "custom_skills"},
	} {
		for _, raw := range [][]byte{[]byte("["), {0xff}, []byte("[{}]")} {
			trace := []string{}
			r := &fakeReader{trace: &trace, files: map[string][]byte{entry.path: raw}}
			snapshot, err := load(r, r)
			if snapshot != nil || err == nil || err.Stage != "input" || err.Source != entry.role || err.Input == nil {
				t.Fatalf("input failure role %s: %v", entry.role, err)
			}
		}
		trace := []string{}
		r := &fakeReader{trace: &trace, reads: map[string]*runtimepaths.Error{entry.path: {Code: "SYNTHETIC_READ_FAILURE"}}}
		snapshot, err := load(r, r)
		if snapshot != nil || err == nil || err.Stage != "read" || err.Source != entry.role {
			t.Fatalf("read failure role %s: %v", entry.role, err)
		}
	}
	trace := []string{}
	r := &fakeReader{trace: &trace, files: map[string][]byte{
		CustomGroupsPath: []byte("[{}]"), CoreSkillsPath: []byte("["),
	}}
	if _, err := load(r, r); err == nil || err.Source != "custom_groups" || err.Input.Stage != "model" {
		t.Fatalf("first decoded file failure lost: %v", err)
	}
	trace = nil
	r.files = map[string][]byte{CustomSkillsPath: []byte("- {id: one, name: One, category: tool, parent: missing}")}
	if snapshot, err := load(r, r); snapshot != nil || err == nil || err.Stage != "registry" || err.Registry == nil {
		t.Fatalf("effective registry failure lost: %v", err)
	}
}

func TestRawRevisionUsesSameOwnedBytesAsDecodedModels(t *testing.T) {
	trace := []string{}
	old := []byte("# before\r\n- {id: one, name: Before, category: tool}\r\n")
	r := &fakeReader{trace: &trace, files: map[string][]byte{CustomSkillsPath: old}}
	r.afterRead = func(path string) {
		if path == CustomSkillsPath {
			r.files[path] = []byte("- {id: two, name: After, category: tool}\n")
		}
	}
	snapshot, err := load(r, r)
	if err != nil || len(snapshot.Skills()) != 1 || snapshot.Skills()[0].Name != "Before" || snapshot.Revision() != catalog.RawRevision(nil, old) {
		t.Fatalf("mixed snapshot: %v", err)
	}
	reads := 0
	for _, event := range trace {
		if event == "read:"+CustomSkillsPath {
			reads++
		}
	}
	if reads != 1 {
		t.Fatalf("custom file reread %d times", reads)
	}
	if snapshot.Revision() == catalog.RawRevision(nil, r.files[CustomSkillsPath]) {
		t.Fatal("revision came from later bytes")
	}
}

func fixtureFile(t *testing.T, root, relative string, raw []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	// macOS temporary directories can start with the /var symlink. The host
	// supplies a resolved root; the reader must not weaken its link policy.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func fixtureSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[relative] = "directory"
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[relative] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativeSeparatedRootsAndReadImmutability(t *testing.T) {
	base := fixtureRoot(t)
	resources, data := filepath.Join(base, "resources"), filepath.Join(base, "Données privées")
	if err := os.MkdirAll(resources, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	core := []byte("- {id: one, name: Core, category: tool, archived: true}\n")
	custom := []byte("# custom\r\n- {id: one, name: Private, category: custom_skill, archived: false}\r\n")
	fixtureFile(t, resources, CoreSkillsPath, core)
	fixtureFile(t, data, CustomSkillsPath, custom)
	fixtureFile(t, data, CoreSkillsPath, []byte("[broken decoy"))
	before := fixtureSnapshot(t, base)
	snapshot, err := Load(resources, data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, fixtureSnapshot(t, base)) {
		t.Fatal("read mutated files or directories")
	}
	skills := snapshot.Skills()
	if len(skills) != 1 || skills[0].Name != "Private" || skills[0].Archived || snapshot.SkillOrigin("one") != "custom" {
		t.Fatalf("root/overlay confusion: %#v", skills)
	}
	if snapshot.Revision() != catalog.RawRevision(nil, custom) {
		t.Fatal("revision bytes changed")
	}
	fixtureFile(t, data, CustomSkillsPath, append([]byte("# extra comment\n"), custom...))
	changed, err := Load(resources, data)
	if err != nil || changed.Revision() == snapshot.Revision() || changed.CatalogRevision() != snapshot.CatalogRevision() {
		t.Fatalf("raw and semantic revisions conflated: %v", err)
	}
}

func TestNativeEmptyRootsAndMissingRootNeverBootstrap(t *testing.T) {
	base := fixtureRoot(t)
	resources, data := filepath.Join(base, "resources"), filepath.Join(base, "data")
	if err := os.Mkdir(resources, 0700); err != nil {
		t.Fatal(err)
	}
	before := fixtureSnapshot(t, base)
	if snapshot, err := Load(resources, data); snapshot != nil || err == nil || err.Source != "data_root" || strings.Contains(err.Error(), base) {
		t.Fatalf("missing root: %v", err)
	}
	if !reflect.DeepEqual(before, fixtureSnapshot(t, base)) {
		t.Fatal("missing root was created")
	}
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	before = fixtureSnapshot(t, base)
	snapshot, err := Load(resources, data)
	if err != nil || len(snapshot.Skills()) != 0 || len(snapshot.Groups()) != len(catalog.DefaultGroups()) {
		t.Fatalf("empty roots: %v", err)
	}
	if !reflect.DeepEqual(before, fixtureSnapshot(t, base)) {
		t.Fatal("empty catalog read bootstrapped data")
	}
	if snapshot, err = Load(filepath.Join(base, "missing-resources"), data); snapshot != nil || err == nil || err.Source != "resource_root" {
		t.Fatalf("missing resources: %v", err)
	}
}
