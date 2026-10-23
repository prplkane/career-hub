package homebackup

import (
	"context"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestRestoreInventoryPhysicalDatabase(t *testing.T) {
	f := &fakeInventory{entries: map[string][]runtimepaths.DirectoryEntry{
		"applications": {fileEntry("career_hub.sqlite3-wal", 20), fileEntry("career_hub.sqlite3", 30), fileEntry("career_hub.sqlite3-shm", 10), fileEntry("career_hub.sqlite3-journal", 4)},
		"jobs":         {},
	}}
	i, p := collectRestoreInventory(context.Background(), f, nil)
	if p != nil {
		t.Fatal(p)
	}
	want := []inventoryFile{{RelativePath: databasePath, SizeBytes: 30}, {RelativePath: databasePath + "-journal", SizeBytes: 4}, {RelativePath: databasePath + "-shm", SizeBytes: 10}, {RelativePath: databasePath + "-wal", SizeBytes: 20}}
	if !reflect.DeepEqual(i.Files, want) || i.Database || len(i.Companions) != 0 || !reflect.DeepEqual(i.Directories, []string{"applications", "jobs"}) {
		t.Fatal(i)
	}
	// Export inventory still delegates the database and companions to SQLite.
	export, p := collectInventory(context.Background(), f, nil)
	if p != nil || len(export.Files) != 0 || !export.Database || len(export.Companions) != 3 {
		t.Fatal(export, p)
	}
}

func TestRestoreInventoryPhysicalBounds(t *testing.T) {
	for _, name := range []string{"career_hub.sqlite3", "career_hub.sqlite3-wal", "career_hub.sqlite3-shm", "career_hub.sqlite3-journal"} {
		for _, size := range []int64{-1, MaxEntryBytes + 1} {
			f := &fakeInventory{entries: map[string][]runtimepaths.DirectoryEntry{"applications": {fileEntry(name, size)}}}
			if i, p := collectRestoreInventory(context.Background(), f, nil); i != nil || p == nil || p.Code != "BACKUP_FILE_LIMIT" {
				t.Fatal(name, i, p)
			}
		}
		f := &fakeInventory{entries: map[string][]runtimepaths.DirectoryEntry{"applications": {dirEntry(name)}}}
		if i, p := collectRestoreInventory(context.Background(), f, nil); i != nil || p == nil || p.Code != "BACKUP_DATABASE_PATH_INVALID" {
			t.Fatal(name, i, p)
		}
	}
}
