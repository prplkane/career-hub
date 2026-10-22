package runtimepaths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// macOS's temporary prefix can itself be /var -> /private/var; callers
	// must supply a reviewed non-link absolute root, as the product contract says.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
func put(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func opened(t *testing.T, dir string) *Root {
	t.Helper()
	r, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}
func code(t *testing.T, e *Error, want string) {
	t.Helper()
	if e == nil || e.Code != want {
		t.Fatalf("error=%#v; want %s", e, want)
	}
}

func TestReadOnlyOptionalOwnedAndClosed(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "catalog/terms.yaml", "hello")
	put(t, dir, "empty.yaml", "")
	r := opened(t, dir)
	if e := r.Check("missing/nested.yaml"); e != nil {
		t.Fatal(e)
	}
	if _, err := os.Lstat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatal("optional check created a directory")
	}
	if raw, present, e := r.ReadFile("missing/nested.yaml", 64); e != nil || present || raw != nil {
		t.Fatalf("missing: %q %v %v", raw, present, e)
	}
	if e := r.Check("catalog/terms.yaml"); e != nil {
		t.Fatal(e)
	}
	raw, present, e := r.ReadFile("catalog/terms.yaml", 5)
	if e != nil || !present || string(raw) != "hello" {
		t.Fatalf("read: %q %v %v", raw, present, e)
	}
	raw[0] = 'X'
	again, _, e := r.ReadFile("catalog/terms.yaml", 5)
	if e != nil || string(again) != "hello" {
		t.Fatal("returned bytes shared or source changed")
	}
	if data, present, e := r.ReadFile("empty.yaml", 0); e != nil || !present || len(data) != 0 {
		t.Fatal("empty file is not present")
	}
	_, _, e = r.ReadFile("catalog/terms.yaml", 4)
	code(t, e, "FILE_SIZE_LIMIT")
	_, _, e = r.ReadFile("catalog/terms.yaml", -1)
	code(t, e, "READ_LIMIT_INVALID")
	_, _, e = r.ReadFile("catalog/terms.yaml", MaxReadBytes+1)
	code(t, e, "READ_LIMIT_INVALID")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	code(t, r.Check("catalog/terms.yaml"), "ROOT_CLOSED")
	_, _, e = r.ReadFile("catalog/terms.yaml", 5)
	code(t, e, "ROOT_CLOSED")
}

func TestExplicitRootsAndInvalidPaths(t *testing.T) {
	dir := testHome(t)
	for _, path := range []string{"", "relative", "."} {
		_, e := Open(path)
		code(t, e, "ROOT_ABSOLUTE_REQUIRED")
	}
	_, e := Open(filepath.VolumeName(dir) + string(filepath.Separator))
	code(t, e, "ROOT_TOO_BROAD")
	_, e = Open(filepath.Join(dir, "absent"))
	code(t, e, "ROOT_UNAVAILABLE")
	put(t, dir, "file", "x")
	_, e = Open(filepath.Join(dir, "file"))
	code(t, e, "ROOT_NOT_DIRECTORY")
	r := opened(t, dir)
	for _, path := range []string{"../secret", "a/../secret", "a/./b", "a//b", "a/", "file:stream", "NUL", "nul.txt", "COM1.yaml", "LPT²", "a?b", "a. ", "a\x00b"} {
		t.Run(path, func(t *testing.T) {
			if e := r.Check(path); e == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	for _, path := range []string{"/absolute", `\absolute`, `C:\absolute`, `C:relative`} {
		if e := r.Check(path); e == nil {
			t.Fatal("absolute/drive path accepted")
		}
	}
	code(t, r.Check("a\xffb"), "PATH_UNICODE_UNSUPPORTED")
	code(t, r.Check(strings.Repeat("a/", MaxComponents)+"x"), "PATH_DEPTH_LIMIT")
	code(t, r.Check(strings.Repeat("a", MaxComponentBytes+1)), "PATH_COMPONENT_LIMIT")
	if e := r.Check("catalog\\missing.yaml"); e != nil {
		t.Fatal("portable separator should work", e)
	}
}

func TestTypesAndIndependentRecheck(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "folder/leaf", "x")
	put(t, dir, "plain", "x")
	r := opened(t, dir)
	code(t, r.Check("folder"), "PATH_NOT_REGULAR")
	code(t, r.Check("plain/child"), "PATH_NOT_DIRECTORY")
	if e := r.Check("plain"); e != nil {
		t.Fatal(e)
	}
	if err := os.Remove(filepath.Join(dir, "plain")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "plain"), 0700); err != nil {
		t.Fatal(err)
	}
	_, _, e := r.ReadFile("plain", 64)
	code(t, e, "PATH_NOT_REGULAR")
}

func TestPortableCaseAndCanonicalNormalization(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "Resume.yaml", "one")
	put(t, dir, "cafe\u0301.yaml", "two")
	r := opened(t, dir)
	code(t, r.Check("resume.yaml"), "PATH_CASE_COLLISION")
	data, present, e := r.ReadFile("café.yaml", 20)
	if e != nil || !present || string(data) != "two" {
		t.Fatalf("NFD existing name lookup: %q %v %v", data, present, e)
	}
	put(t, dir, "café.yaml", "three")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "caf") {
			count++
		}
	}
	if count < 2 {
		t.Skip("native filesystem coalesces canonical normalization variants")
	}
	code(t, r.Check("café.yaml"), "PATH_COLLISION")
}

func TestFullCasefoldCollision(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "straße.yaml", "one")
	put(t, dir, "strasse.yaml", "two")
	r := opened(t, dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Skip("filesystem coalesces full fold variants")
	}
	code(t, r.Check("straße.yaml"), "PATH_COLLISION")
}

func TestSymlinksIncludingDanglingAndRootAncestor(t *testing.T) {
	dir := testHome(t)
	outside := testHome(t)
	put(t, outside, "secret", "outside")
	put(t, dir, "real/inside", "inside")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "link")); err != nil {
		t.Skipf("native symlink fixture unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "absent"), filepath.Join(dir, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "directory")); err != nil {
		t.Fatal(err)
	}
	r := opened(t, dir)
	for _, path := range []string{"link", "dangling", "directory/inside"} {
		code(t, r.Check(path), "PATH_LINK")
		_, _, e := r.ReadFile(path, 100)
		code(t, e, "PATH_LINK")
	}
	_, e := Open(filepath.Join(dir, "directory"))
	code(t, e, "ROOT_LINK")
	_, e = Open(filepath.Join(dir, "directory", "missing"))
	code(t, e, "ROOT_LINK")
}

func TestRootReplacementDetected(t *testing.T) {
	outer := testHome(t)
	dir := filepath.Join(outer, "root")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "a", "first")
	r := opened(t, dir)
	if err := os.Rename(dir, filepath.Join(outer, "original")); err != nil {
		t.Skipf("native open-directory rename not supported: %v", err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "a", "replacement")
	_, _, e := r.ReadFile("a", 64)
	code(t, e, "PATH_CHANGED")
}

func TestDirectoryEntryBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("bounded native directory-entry limit fixture")
	}
	dir := testHome(t)
	for i := 0; i <= MaxDirectoryEntries; i++ {
		put(t, dir, fmt.Sprintf("entry-%05d", i), "")
	}
	r := opened(t, dir)
	code(t, r.Check("absent"), "PATH_DIRECTORY_LIMIT")
}

func TestUnicodeGuardAndStreamSafety(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("not a supported filesystem platform")
	}
	for _, name := range []string{"résumé", "cafe\u0301", "日本語", "a\u034fb", "new-\U0001CC00"} {
		if _, e := normalizeName(name); e != nil {
			t.Fatalf("supported Unicode: %v", e)
		}
	}
	_, e := normalizeName("a\u0897")
	code(t, e, "PATH_UNICODE_UNSUPPORTED")
	_, e = normalizeName("a" + strings.Repeat("\u0301", 31))
	code(t, e, "PATH_UNICODE_STREAM_UNSUPPORTED")
	_, e = normalizeName("a\u034f" + strings.Repeat("\u0301", 31))
	code(t, e, "PATH_UNICODE_STREAM_UNSUPPORTED")
	actual, e := normalizeName("a\u034fb")
	if e != nil || actual != "a\u034fb" {
		t.Fatal("legitimate CGJ was lost")
	}
}
