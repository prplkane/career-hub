//go:build windows

package runtimepaths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryEntriesFreshDeepTree(t *testing.T) {
	for attempt := 0; attempt < 3; attempt++ {
		dir := testHome(t)
		leaf := "jobs/" + strings.Repeat("d/", 62) + "leaf.txt"
		full := filepath.Join(dir, filepath.FromSlash(leaf))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
		root := opened(t, dir)
		path := "jobs"
		// No sleep/retry to hide stale enumeration metadata. Each level must
		// bind its enumerated identity to fresh rooted directory observations.
		for depth := 0; depth <= 62; depth++ {
			entries, present, p := root.ListDirectoryEntries(path)
			if p != nil || !present || len(entries) != 1 {
				t.Fatalf("depth %d: %v", depth, p)
			}
			if entries[0].IsDirectory != (depth < 62) {
				t.Fatal("wrong entry type")
			}
			path += "/d"
		}
	}
}

func TestDirectoryEntriesRejectNativeJunction(t *testing.T) {
	dir, target := testHome(t), testHome(t)
	if err := os.Mkdir(filepath.Join(dir, "jobs"), 0700); err != nil {
		t.Fatal(err)
	}
	createJunction(t, filepath.Join(dir, "jobs", "linked"), target)
	items, present, p := opened(t, dir).ListDirectoryEntries("jobs")
	if items != nil || present {
		t.Fatal("partial inventory returned")
	}
	code(t, p, "PATH_LINK")
}
