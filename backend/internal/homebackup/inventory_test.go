package homebackup

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/runtimepaths"
)

type fakeInventory struct {
	entries  map[string][]runtimepaths.DirectoryEntry
	problems map[string]*runtimepaths.Error
	calls    []string
}

func (f *fakeInventory) ListDirectoryEntries(path string) ([]runtimepaths.DirectoryEntry, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, path)
	entries, present := f.entries[path]
	return entries, present, f.problems[path]
}
func fileEntry(name string, size int64) runtimepaths.DirectoryEntry {
	return runtimepaths.DirectoryEntry{Name: name, Size: size}
}
func dirEntry(name string) runtimepaths.DirectoryEntry {
	return runtimepaths.DirectoryEntry{Name: name, IsDirectory: true}
}

func TestInventoryTypedOrderingAndDatabaseOverlay(t *testing.T) {
	stamp := time.Now().UTC()
	f := &fakeInventory{entries: map[string][]runtimepaths.DirectoryEntry{
		"jobs":         {fileEntry("z", 3), dirEntry("A")},
		"jobs/A":       {{Name: "é.txt", Size: 4, Modified: stamp}},
		"applications": {fileEntry("career_hub.sqlite3-wal", 20), fileEntry("career_hub.sqlite3", 30), fileEntry("x", 1)},
	}}
	i, p := collectInventory(context.Background(), f, nil)
	if p != nil {
		t.Fatal(p)
	}
	want := []inventoryFile{{RelativePath: "applications/x", SizeBytes: 1}, {RelativePath: "jobs/A/é.txt", SizeBytes: 4, Modified: stamp}, {RelativePath: "jobs/z", SizeBytes: 3}}
	if !reflect.DeepEqual(i.Files, want) || !i.Database || !reflect.DeepEqual(i.Companions, []string{databasePath + "-wal"}) {
		t.Fatal(i)
	}
	if f.entries["jobs"][0].Name != "z" {
		t.Fatal("mutated reader-owned slice")
	}
	if len(f.calls) != 9 {
		t.Fatal("rescanned file siblings", f.calls)
	}
	roots := []string{"jobs", "applications"}
	selected, p := selectedRoots(roots)
	if p != nil {
		t.Fatal(p)
	}
	selected[0] = "logs"
	if roots[0] != "jobs" {
		t.Fatal("selection alias")
	}
}

func TestInventoryRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []runtimepaths.DirectoryEntry
		want    string
	}{
		{"orphan", []runtimepaths.DirectoryEntry{fileEntry("career_hub.sqlite3-wal", 1)}, "BACKUP_DATABASE_ORPHANED_FILES"},
		{"database directory", []runtimepaths.DirectoryEntry{dirEntry("career_hub.sqlite3")}, "BACKUP_DATABASE_PATH_INVALID"},
		{"companion directory", []runtimepaths.DirectoryEntry{dirEntry("career_hub.sqlite3-shm")}, "BACKUP_DATABASE_PATH_INVALID"},
		{"traversal", []runtimepaths.DirectoryEntry{fileEntry("../x", 1)}, "BACKUP_PATH_INVALID"},
		{"nested leaf", []runtimepaths.DirectoryEntry{fileEntry("a/b", 1)}, "BACKUP_PATH_INVALID"},
		{"case collision", []runtimepaths.DirectoryEntry{fileEntry("Straße", 1), fileEntry("STRASSE", 1)}, "BACKUP_PATH_COLLISION"},
		{"negative", []runtimepaths.DirectoryEntry{fileEntry("x", -1)}, "BACKUP_FILE_LIMIT"},
		{"oversized", []runtimepaths.DirectoryEntry{fileEntry("x", MaxEntryBytes+1)}, "BACKUP_FILE_LIMIT"},
		{"disappeared", []runtimepaths.DirectoryEntry{dirEntry("missing")}, "BACKUP_SOURCE_CHANGED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeInventory{entries: map[string][]runtimepaths.DirectoryEntry{"applications": tc.entries}}
			i, p := collectInventory(context.Background(), f, []string{"applications"})
			if i != nil || p == nil || p.Code != tc.want {
				t.Fatal(i, p)
			}
		})
	}
	for _, selection := range [][]string{{}, {"jobs", "jobs"}, {"Jobs"}, {"backups"}, {"../jobs"}} {
		if _, p := selectedRoots(selection); p == nil {
			t.Fatal(selection)
		}
	}
	f := &fakeInventory{entries: map[string][]runtimepaths.DirectoryEntry{"jobs": {dirEntry("a")}}, problems: map[string]*runtimepaths.Error{"jobs/a": {Code: "PATH_NOT_DIRECTORY"}}}
	if i, p := collectInventory(context.Background(), f, []string{"jobs"}); i != nil || p == nil || p.Code != "BACKUP_SOURCE_UNAVAILABLE" {
		t.Fatal("inferred file from error", i, p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if _, p := collectInventory(ctx, f, nil); p == nil || p.Code != "BACKUP_CANCELED" {
			t.Fatal(p)
		}
	}
	if _, p := collectInventory(context.Background(), nil, nil); p == nil {
		t.Fatal("nil reader")
	}
}

func TestInventoryMetadataBoundsWithoutLargePayload(t *testing.T) {
	f := &fakeInventory{entries: map[string][]runtimepaths.DirectoryEntry{"jobs": {}}}
	for n := 0; n < 17; n++ {
		f.entries["jobs"] = append(f.entries["jobs"], fileEntry(fmt.Sprint(n), MaxEntryBytes))
	}
	if _, p := collectInventory(context.Background(), f, []string{"jobs"}); p == nil || p.Code != "BACKUP_TOTAL_LIMIT" {
		t.Fatal(p)
	}
	f.entries = map[string][]runtimepaths.DirectoryEntry{}
	path := "jobs"
	for n := 1; n < runtimepaths.MaxComponents; n++ {
		f.entries[path] = []runtimepaths.DirectoryEntry{dirEntry("a")}
		path += "/a"
	}
	f.entries[path] = []runtimepaths.DirectoryEntry{fileEntry("leaf", 1)}
	if _, p := collectInventory(context.Background(), f, []string{"jobs"}); p == nil || p.Code != "BACKUP_INVENTORY_LIMIT" {
		t.Fatal(p)
	}
	f.entries[path] = nil
	if _, p := collectInventory(context.Background(), f, []string{"jobs"}); p != nil {
		t.Fatal(p)
	}
	f.entries = map[string][]runtimepaths.DirectoryEntry{"jobs": {fileEntry(strings.Repeat("a", runtimepaths.MaxPathBytes), 1)}}
	if _, p := collectInventory(context.Background(), f, []string{"jobs"}); p == nil || p.Code != "BACKUP_PATH_INVALID" {
		t.Fatal(p)
	}
}
