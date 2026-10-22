package runtimepaths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryEntriesTypedAndConfined(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "jobs/z.txt", "synthetic")
	if err := os.Mkdir(filepath.Join(dir, "jobs", "a"), 0700); err != nil {
		t.Fatal(err)
	}
	r := opened(t, dir)
	items, present, p := r.ListDirectoryEntries("jobs")
	if p != nil || !present || len(items) != 2 {
		t.Fatal(items, present, p)
	}
	if items[0].Name != "a" || !items[0].IsDirectory || items[1].Name != "z.txt" || items[1].IsDirectory || items[1].Size != 9 || items[1].Modified.IsZero() {
		t.Fatal(items)
	}
	if items, present, p := r.ListDirectoryEntries("missing"); items != nil || present || p != nil {
		t.Fatal(items, present, p)
	}
	if items, present, p := r.ListDirectoryEntries("jobs/a"); items == nil || len(items) != 0 || !present || p != nil {
		t.Fatal(items, present, p)
	}
	for _, path := range []string{"jobs/z.txt", "../jobs", "jobs/z.txt/nested"} {
		if items, present, p := r.ListDirectoryEntries(path); items != nil || present || p == nil {
			t.Fatal(path, items, present, p)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, p = r.ListDirectoryEntries("jobs")
	code(t, p, "ROOT_CLOSED")
	var nilRoot *Root
	_, _, p = nilRoot.ListDirectoryEntries("jobs")
	code(t, p, "ROOT_CLOSED")
}

func TestDirectoryEntriesRejectCollision(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "jobs/Straße", "one")
	put(t, dir, "jobs/STRASSE", "two")
	items, present, p := opened(t, dir).ListDirectoryEntries("jobs")
	if items != nil || present {
		t.Fatal("partial inventory returned")
	}
	code(t, p, "PATH_COLLISION")
}

func TestDirectoryEntriesRejectSymlink(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "jobs/ordinary", "one")
	if err := os.Symlink(filepath.Join(dir, "jobs", "ordinary"), filepath.Join(dir, "jobs", "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	items, present, p := opened(t, dir).ListDirectoryEntries("jobs")
	if items != nil || present {
		t.Fatal("partial inventory returned")
	}
	code(t, p, "PATH_LINK")
}
