package runtimepaths

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListFileMetadata(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "b.yaml", "second")
	put(t, dir, "a.yaml", "first")
	stamp := time.Date(2026, 9, 12, 10, 0, 0, 123456000, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "a.yaml"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	r := opened(t, dir)
	entries, e := r.ListFileMetadata()
	if e != nil || len(entries) != 2 || entries[0].Name != "a.yaml" || entries[0].Size != 5 || !entries[0].Modified.Equal(stamp) {
		t.Fatalf("metadata: %+v %v", entries, e)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if entries, e := r.ListFileMetadata(); entries != nil || e == nil {
		t.Fatal("directory accepted")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if entries, e := r.ListFileMetadata(); entries != nil || e == nil || e.Code != "ROOT_CLOSED" {
		t.Fatal("closed root accepted")
	}
}

func TestListFileMetadataRejectsLink(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "a.yaml", "synthetic")
	if err := os.Symlink(filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml")); err != nil {
		t.Skip("native symlink creation unavailable")
	}
	r := opened(t, dir)
	if entries, e := r.ListFileMetadata(); entries != nil || e == nil || e.Code != "PATH_LINK" {
		t.Fatal("link accepted")
	}
}
