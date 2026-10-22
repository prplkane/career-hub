package catalogwrite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/skills/catalog"
)

const fixtureGroups = "# private comment\r\n- {id: user_first, name: First}\r\n- {id: user_archived, name: Archived, archived: true, cascade_archived_skill_ids: [user_archived_term]}\r\n"
const fixtureSkills = "- {id: user_archived_term, name: Old skill, category: tool, primary_group_id: user_archived, archived: true}\n"

type renameFixture struct {
	data, resources string
	service         *Service
	revision        string
}

func putFixture(t *testing.T, root, path string, raw []byte) {
	t.Helper()
	path = filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func newRenameFixture(t *testing.T) renameFixture {
	t.Helper()
	f := renameFixture{data: t.TempDir(), resources: t.TempDir()}
	putFixture(t, f.resources, catalogstore.CoreGroupsPath, []byte("- {id: shipped, name: Built in}\n"))
	putFixture(t, f.resources, catalogstore.CoreSkillsPath, []byte("[]\n"))
	putFixture(t, f.data, catalogstore.CustomGroupsPath, []byte(fixtureGroups))
	putFixture(t, f.data, catalogstore.CustomSkillsPath, []byte(fixtureSkills))
	putFixture(t, f.data, "career_vault/profile.yaml", []byte("synthetic unrelated fixture\n"))
	f.service = New(f.resources, f.data)
	f.revision = catalog.RawRevision([]byte(fixtureGroups), []byte(fixtureSkills))
	return f
}

func fileBytes(t *testing.T, root, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type observedFile struct {
	Raw     string
	Mode    os.FileMode
	ModTime int64
}

func observeTree(t *testing.T, root string) map[string]observedFile {
	t.Helper()
	result := map[string]observedFile{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = observedFile{string(raw), info.Mode(), info.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRenameCustomGroupPreservesPrivateIdentityAndUnrelatedFiles(t *testing.T) {
	for _, id := range []string{"user_first", "user_archived"} {
		t.Run(id, func(t *testing.T) {
			f := newRenameFixture(t)
			beforeData, beforeResources := observeTree(t, f.data), observeTree(t, f.resources)
			result, err := f.service.RenameCustomGroup(context.Background(), id, map[string]any{"name": "\u001c \u00a0Renamed 🧪\u3000"}, f.revision)
			if err != nil {
				t.Fatalf("rename: %#v", err)
			}
			if result.Record.ID != id || result.Record.Name != "Renamed 🧪" || result.Record.Origin != "custom" {
				t.Fatalf("result: %#v", result)
			}
			if result.Record.Archived != (id == "user_archived") {
				t.Fatal("archive state changed")
			}
			wantCascade := []string{}
			if id == "user_archived" {
				wantCascade = []string{"user_archived_term"}
			}
			if !reflect.DeepEqual(result.Record.CascadeArchivedSkillIDs, wantCascade) {
				t.Fatal("cascade changed")
			}
			after := observeTree(t, f.data)
			if len(beforeData) != len(after) {
				t.Fatal("unexpected file or temporary file")
			}
			for path, info := range beforeData {
				if path != catalogstore.CustomGroupsPath && !reflect.DeepEqual(info, after[path]) {
					t.Fatalf("changed unrelated %s", path)
				}
			}
			if !reflect.DeepEqual(beforeResources, observeTree(t, f.resources)) {
				t.Fatal("core files changed")
			}
			raw := fileBytes(t, f.data, catalogstore.CustomGroupsPath)
			groups, decodeErr := cataloginput.DecodeGroups(raw, true)
			if decodeErr != nil || len(groups) != 2 || groups[0].ID != "user_first" || groups[1].ID != "user_archived" {
				t.Fatalf("private file/order: %#v", groups)
			}
			if result.Revision != catalog.RawRevision(raw, []byte(fixtureSkills)) {
				t.Fatal("revision not from saved raw bytes")
			}
		})
	}
}

func TestRenameValidationOrderLeavesAllFilesUnchanged(t *testing.T) {
	cases := []struct {
		name, id, revision, code, message string
		payload                           map[string]any
	}{
		{"unknown before load", "shipped", "stale", "SKILL_REGISTRY_INVALID", "Unknown group fields: a, z", map[string]any{"z": 1, "a": 2}},
		{"stale before core", "shipped", "stale", "SKILL_REGISTRY_REVISION_CONFLICT", "", nil},
		{"core before invalid name", "shipped", "", "CORE_GROUP_READ_ONLY", "", nil},
		{"missing before invalid name", "missing", "", "SKILL_GROUP_NOT_FOUND", "", nil},
		{"missing name", "user_first", "", "SKILL_REGISTRY_INVALID", "name must be a non-empty string", nil},
		{"wrong name type", "user_first", "", "SKILL_REGISTRY_INVALID", "name must be a non-empty string", map[string]any{"name": 123}},
		{"empty name", "user_first", "", "SKILL_REGISTRY_INVALID", "name must be a non-empty string", map[string]any{"name": "\u001c\u3000"}},
		{"too long", "user_first", "", "SKILL_REGISTRY_INVALID", "name must be 160 characters or fewer", map[string]any{"name": strings.Repeat("🧪", 161)}},
		{"collision", "user_first", "", "SKILL_GROUP_NAME_CONFLICT", "", map[string]any{"name": "BUILT IN"}},
		{"invalid unicode", "user_first", "", "CATALOG_NAME_UNICODE_UNSUPPORTED", "", map[string]any{"name": string([]byte{0xff})}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRenameFixture(t)
			beforeData, beforeResources := observeTree(t, f.data), observeTree(t, f.resources)
			if tc.revision == "" {
				tc.revision = f.revision
			}
			if tc.name == "unknown before load" {
				f.service.dataRoot = "invalid-relative-path"
			}
			result, err := f.service.RenameCustomGroup(context.Background(), tc.id, tc.payload, tc.revision)
			if result != nil || err == nil || err.Code != tc.code || err.Committed {
				t.Fatalf("unexpected error: %#v", err)
			}
			if tc.message != "" && (err.Registry == nil || err.Registry.Message != tc.message) {
				t.Fatalf("wrong message: %#v", err.Registry)
			}
			if !reflect.DeepEqual(beforeData, observeTree(t, f.data)) || !reflect.DeepEqual(beforeResources, observeTree(t, f.resources)) {
				t.Fatal("validation changed files")
			}
		})
	}
}

func TestRenameShadowedCoreAndPreRenameReconciliation(t *testing.T) {
	f := newRenameFixture(t)
	putFixture(t, f.resources, catalogstore.CoreGroupsPath, []byte("- {id: user_first, name: Shipped old name}\n"))
	if _, err := f.service.RenameCustomGroup(context.Background(), "user_first", map[string]any{"name": "Editable shadow"}, f.revision); err != nil {
		t.Fatalf("shadowed core ID refused: %#v", err)
	}
	f = newRenameFixture(t)
	putFixture(t, f.resources, catalogstore.CoreGroupsPath, []byte("- {id: shipped, name: First}\n"))
	before := observeTree(t, f.data)
	if _, err := f.service.RenameCustomGroup(context.Background(), "user_first", map[string]any{"name": "First (built-in)"}, f.revision); err == nil || err.Code != "SKILL_GROUP_NAME_CONFLICT" {
		t.Fatalf("pre-rename effective core not validated: %#v", err)
	}
	if !reflect.DeepEqual(before, observeTree(t, f.data)) {
		t.Fatal("conflict wrote")
	}
}

func TestRenameSameNameStillRewritesAndCountsUnicodeScalars(t *testing.T) {
	f := newRenameFixture(t)
	result, err := f.service.RenameCustomGroup(context.Background(), "user_first", map[string]any{"name": "  First  "}, f.revision)
	if err != nil || result.Revision == f.revision {
		t.Fatalf("source same-name rewrite lost: %#v", err)
	}
	result, err = f.service.RenameCustomGroup(context.Background(), "user_first", map[string]any{"name": strings.Repeat("🧪", 160)}, result.Revision)
	if err != nil || len(result.Record.Name) != 640 {
		t.Fatalf("160 scalars refused: %#v", err)
	}
}

func TestRenameSecondRevisionGuardChecksBothRawFiles(t *testing.T) {
	for _, path := range []string{catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath} {
		t.Run(path, func(t *testing.T) {
			f := newRenameFixture(t)
			var intervening []byte
			f.service.encode = func(groups []catalog.Group) ([]byte, error) {
				intervening = append(fileBytes(t, f.data, path), []byte("# another writer\n")...)
				putFixture(t, f.data, path, intervening)
				return encodeGroups(groups)
			}
			if _, err := f.service.RenameCustomGroup(context.Background(), "user_first", map[string]any{"name": "Rejected edit"}, f.revision); err == nil || err.Code != "SKILL_REGISTRY_REVISION_CONFLICT" || err.Committed {
				t.Fatalf("second guard: %#v", err)
			}
			if !bytes.Equal(intervening, fileBytes(t, f.data, path)) {
				t.Fatal("intervening bytes overwritten")
			}
			groups := fileBytes(t, f.data, catalogstore.CustomGroupsPath)
			if bytes.Contains(groups, []byte("Rejected edit")) {
				t.Fatal("stale edit saved")
			}
		})
	}
}

func TestRenameFailuresReportWhetherReplacementCommitted(t *testing.T) {
	for _, stage := range []string{"encode", "replace", "conflict", "after_replace", "readback", "readback_changed", "unconfirmed"} {
		t.Run(stage, func(t *testing.T) {
			f := newRenameFixture(t)
			switch stage {
			case "encode":
				f.service.encode = func([]catalog.Group) ([]byte, error) { return nil, errors.New("private fixture error") }
			case "replace", "conflict", "unconfirmed":
				f.service.replace = func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error) {
					if stage == "unconfirmed" {
						return false, nil
					}
					code := "FILE_RENAME"
					if stage == "conflict" {
						code = "FILE_CONFLICT"
					}
					return false, &runtimepaths.Error{Code: code}
				}
			case "after_replace":
				f.service.replace = func(root *runtimepaths.Root, path string, old, replacement []byte) (bool, *runtimepaths.Error) {
					committed, err := root.ReplaceFile(path, old, replacement)
					if err != nil {
						return committed, err
					}
					return true, &runtimepaths.Error{Code: "FILE_CLOSE"}
				}
			case "readback", "readback_changed":
				calls := 0
				f.service.read = func(resources, data *runtimepaths.Root) (*catalogstore.State, *catalogstore.Error) {
					calls++
					if calls == 2 && stage == "readback" {
						return nil, &catalogstore.Error{Stage: "read", Code: "SYNTHETIC_FAILURE"}
					}
					state, err := catalogstore.ReadState(resources, data)
					if calls == 2 && stage == "readback_changed" && err == nil {
						state.RawSkills = []byte("different")
					}
					return state, err
				}
			}
			result, err := f.service.RenameCustomGroup(context.Background(), "user_first", map[string]any{"name": "New name"}, f.revision)
			wantCommitted := stage == "after_replace" || stage == "readback" || stage == "readback_changed"
			if result != nil || err == nil || err.Committed != wantCommitted {
				t.Fatalf("false commit outcome: %#v", err)
			}
			if stage == "conflict" && err.Code != "SKILL_REGISTRY_REVISION_CONFLICT" {
				t.Fatalf("wrong conflict: %#v", err)
			}
			actual := fileBytes(t, f.data, catalogstore.CustomGroupsPath)
			if bytes.Contains(actual, []byte("New name")) != wantCommitted {
				t.Fatal("commit flag disagrees with actual bytes")
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("default error leaked dependency text")
			}
		})
	}
}

func TestRenameSerializesStaleConcurrentEditsAndCancelsWaiters(t *testing.T) {
	f := newRenameFixture(t)
	var wg sync.WaitGroup
	wg.Add(2)
	errorsOut := make(chan *Error, 2)
	for _, name := range []string{"One", "Two"} {
		go func() {
			defer wg.Done()
			_, err := f.service.RenameCustomGroup(context.Background(), "user_first", map[string]any{"name": name}, f.revision)
			errorsOut <- err
		}()
	}
	wg.Wait()
	close(errorsOut)
	success, conflict := 0, 0
	for err := range errorsOut {
		if err == nil {
			success++
		} else if err.Code == "SKILL_REGISTRY_REVISION_CONFLICT" {
			conflict++
		} else {
			t.Fatalf("unexpected: %#v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("per-instance serialization missing")
	}
	f.service.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.service.RenameCustomGroup(ctx, "user_first", map[string]any{"name": "Cancelled"}, f.revision)
	<-f.service.gate
	if err == nil || err.Stage != "cancelled" || err.Committed {
		t.Fatalf("wait cancellation: %#v", err)
	}
}

func TestRenameMissingCatalogDoesNotBootstrap(t *testing.T) {
	f := newRenameFixture(t)
	emptyData := t.TempDir()
	service := New(f.resources, emptyData)
	_, err := service.RenameCustomGroup(context.Background(), "user_missing", map[string]any{"name": "Name"}, catalog.RawRevision(nil, nil))
	if err == nil || err.Code != "SKILL_GROUP_NOT_FOUND" || err.Committed {
		t.Fatalf("missing group result: %#v", err)
	}
	entries, osErr := os.ReadDir(emptyData)
	if osErr != nil || len(entries) != 0 {
		t.Fatal("missing request bootstrapped private data")
	}
}

func TestRenameCancellationBeforeAndAfterCommit(t *testing.T) {
	for _, afterCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[afterCommit], func(t *testing.T) {
			f := newRenameFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if afterCommit {
				f.service.replace = func(root *runtimepaths.Root, path string, old, replacement []byte) (bool, *runtimepaths.Error) {
					committed, err := root.ReplaceFile(path, old, replacement)
					cancel()
					return committed, err
				}
			} else {
				f.service.encode = func(groups []catalog.Group) ([]byte, error) { cancel(); return encodeGroups(groups) }
			}
			result, err := f.service.RenameCustomGroup(ctx, "user_first", map[string]any{"name": "New name"}, f.revision)
			if afterCommit {
				if err != nil || result == nil {
					t.Fatalf("committed success obscured by cancellation: %#v", err)
				}
			} else if result != nil || err == nil || err.Stage != "cancelled" || err.Committed {
				t.Fatalf("pre-commit cancellation: %#v", err)
			}
			if bytes.Contains(fileBytes(t, f.data, catalogstore.CustomGroupsPath), []byte("New name")) != afterCommit {
				t.Fatal("cancellation disagrees with actual bytes")
			}
		})
	}
}
