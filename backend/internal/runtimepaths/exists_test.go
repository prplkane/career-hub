package runtimepaths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExistsMetadataOnlyKeepsFileChecksStrict(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "sentinel/file", "not read")
	r := opened(t, dir)
	for _, name := range []string{"sentinel", "sentinel/file"} {
		present, e := r.Exists(name)
		if !present || e != nil {
			t.Fatalf("%s: %v %v", name, present, e)
		}
	}
	code(t, r.Check("sentinel"), "PATH_NOT_REGULAR")
	_, _, e := r.ReadFile("sentinel", 10)
	code(t, e, "PATH_NOT_REGULAR")
	if present, e := r.Exists("missing/nested"); present || e != nil {
		t.Fatalf("missing: %v %v", present, e)
	}
	if _, e := os.Lstat(filepath.Join(dir, "missing")); !os.IsNotExist(e) {
		t.Fatal("metadata query created path")
	}
	if present, e := r.Exists("sentinel/file/nested"); present || e == nil || e.Code != "PATH_NOT_DIRECTORY" {
		t.Fatal("non-directory parent accepted")
	}
	if present, e := r.Exists("../escape"); present || e == nil {
		t.Fatal("traversal accepted")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	_, e = r.Exists("sentinel")
	code(t, e, "ROOT_CLOSED")
	var nilRoot *Root
	_, e = nilRoot.Exists("sentinel")
	code(t, e, "ROOT_CLOSED")
}

func TestExistsRefusesLinks(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "regular", "synthetic")
	if err := os.Symlink(filepath.Join(dir, "regular"), filepath.Join(dir, "link")); err != nil {
		t.Skip("native symlink creation unavailable")
	}
	r := opened(t, dir)
	if present, e := r.Exists("link"); present || e == nil || e.Code != "PATH_LINK" {
		t.Fatalf("link: %v %v", present, e)
	}
}
