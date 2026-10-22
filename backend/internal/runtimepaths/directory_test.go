package runtimepaths

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestListRelativeDirectoryNamesStayConfinedAndSorted(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "documents", "files", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"z.dpapi", "a.dpapi"} {
		if err := os.WriteFile(filepath.Join(base, "documents", "files", name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, problem := Open(base)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	defer root.Close()

	names, present, problem := root.ListDirectoryNames("documents/files")
	if problem != nil || !present || !reflect.DeepEqual(names, []string{"a.dpapi", "nested", "z.dpapi"}) {
		t.Fatalf("unexpected names: %v / %v / %#v", names, present, problem)
	}
	if files, found, failure := root.ListDirectoryFileNames("documents/files"); files != nil || found || failure == nil || failure.Code != "PATH_NOT_REGULAR" {
		t.Fatalf("nested directory was accepted as a file: %v / %v / %#v", files, found, failure)
	}
	missing, found, failure := root.ListDirectoryNames("documents/missing")
	if failure != nil || found || missing != nil {
		t.Fatalf("missing directory was not optional: %v / %v / %#v", missing, found, failure)
	}
	if names, found, failure := root.ListDirectoryNames("documents/files/a.dpapi"); names != nil || found || failure == nil || failure.Code != "PATH_NOT_DIRECTORY" {
		t.Fatalf("regular leaf was accepted as a directory: %v / %v / %#v", names, found, failure)
	}
}

func TestListRelativeDirectoryFileNamesMatchesTopLevelPolicy(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "documents", "files"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.dpapi", "a.dpapi"} {
		if err := os.WriteFile(filepath.Join(base, "documents", "files", name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, problem := Open(base)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	defer root.Close()
	names, present, problem := root.ListDirectoryFileNames("documents/files")
	if problem != nil || !present || !reflect.DeepEqual(names, []string{"a.dpapi", "b.dpapi"}) {
		t.Fatalf("unexpected file names: %v / %v / %#v", names, present, problem)
	}
	stamp := time.Date(2026, 9, 20, 18, 30, 0, 123456000, time.UTC)
	if err := os.Chtimes(filepath.Join(base, "documents", "files", "a.dpapi"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	metadata, present, problem := root.ListDirectoryFileMetadata("documents/files")
	if problem != nil || !present || len(metadata) != 2 || metadata[0].Name != "a.dpapi" ||
		metadata[0].Size != int64(len("a.dpapi")) || !metadata[0].Modified.Equal(stamp) {
		t.Fatalf("unexpected file metadata: %+v / %v / %#v", metadata, present, problem)
	}
}
