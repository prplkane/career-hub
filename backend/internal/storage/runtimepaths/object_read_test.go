package runtimepaths

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestObjectReadKeepsCatalogLimitAndManagedChecks(t *testing.T) {
	directory := testHome(t)
	// Larger than the document plaintext bound, including envelope headroom;
	// no domain encryption implementation is coupled into this path package.
	contents := bytes.Repeat([]byte("o"), (20<<20)+8192)
	target := filepath.Join(directory, "large.chdoc")
	if e := os.WriteFile(target, contents, 0600); e != nil {
		t.Fatal(e)
	}
	before, e := os.Stat(target)
	if e != nil {
		t.Fatal(e)
	}
	expected := sha256.Sum256(contents)
	r := opened(t, directory)
	if raw, found, e := r.ReadFile("large.chdoc", MaxReadBytes); raw != nil || found || e == nil || e.Code != "FILE_SIZE_LIMIT" {
		t.Fatal("catalog limit broadened")
	}
	if raw, found, e := r.ReadFile("large.chdoc", int64(len(contents))); raw != nil || found || e == nil || e.Code != "READ_LIMIT_INVALID" {
		t.Fatal("catalog caller can request larger bound")
	}
	raw, found, problem := r.ReadObjectFile("large.chdoc", int64(len(contents)))
	if problem != nil || !found || sha256.Sum256(raw) != expected {
		t.Fatal("full bounded object read failed", problem)
	}
	for _, limit := range []int64{-1, MaxObjectReadBytes + 1, int64(len(contents) - 1)} {
		if data, found, e := r.ReadObjectFile("large.chdoc", limit); data != nil || found || e == nil {
			t.Fatal("invalid/short object bound admitted")
		}
	}
	for _, relative := range []string{"../large.chdoc", "large.chdoc:stream", "NUL"} {
		if data, found, e := r.ReadObjectFile(relative, MaxObjectReadBytes); data != nil || found || e == nil {
			t.Fatal("object read skipped path admission")
		}
	}
	if data, found, e := r.ReadObjectFile("missing/ledger.chdoc", MaxObjectReadBytes); data != nil || found || e != nil {
		t.Fatal("missing object semantics changed")
	}
	after, e := os.Stat(target)
	if e != nil || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Fatal("read changed file metadata")
	}
	if e := os.Rename(target, filepath.Join(directory, "renamed.chdoc")); e != nil {
		t.Fatal("read leaked file handle")
	}
	if entries, e := os.ReadDir(directory); e != nil || len(entries) != 1 {
		t.Fatal("read created storage")
	}
	if e := r.Close(); e != nil {
		t.Fatal(e)
	}
	if data, found, e := r.ReadObjectFile("renamed.chdoc", 1); data != nil || found || e == nil || e.Code != "ROOT_CLOSED" {
		t.Fatal("closed object reader")
	}
	var absent *Root
	if _, _, e := absent.ReadObjectFile("test", 1); e == nil || e.Code != "ROOT_CLOSED" {
		t.Fatal("nil object reader")
	}
}

func TestListFileNamesChecksTypesWithoutOpeningContents(t *testing.T) {
	directory := testHome(t)
	put(t, directory, "b.chdoc", "not decrypted")
	put(t, directory, "a.chdoc", "")
	r := opened(t, directory)
	names, e := r.ListFileNames()
	if e != nil || !reflect.DeepEqual(names, []string{"a.chdoc", "b.chdoc"}) {
		t.Fatal("file-name scan", e)
	}
	names[0] = "mutation"
	again, e := r.ListFileNames()
	if e != nil || again[0] != "a.chdoc" {
		t.Fatal("returned names share storage")
	}
	if e := os.Mkdir(filepath.Join(directory, "folder"), 0700); e != nil {
		t.Fatal(e)
	}
	if names, e := r.ListFileNames(); names != nil || e == nil || e.Code != "PATH_NOT_REGULAR" {
		t.Fatal("directory treated as a blob")
	}
	if names, e := r.ListNames(); e != nil || len(names) != 3 {
		t.Fatal("general list was narrowed")
	}
	if e := r.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := r.ListFileNames(); e == nil || e.Code != "ROOT_CLOSED" {
		t.Fatal("closed list")
	}
	var missing *Root
	if _, e := missing.ListFileNames(); e == nil || e.Code != "ROOT_CLOSED" {
		t.Fatal("nil list")
	}
}
