//go:build windows

package runtimepaths

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func TestCreationMissingRootDirectoriesAndProbe(t *testing.T) {
	parent := testHome(t)
	dir := filepath.Join(parent, "new", "home")
	r, changed, e := OpenOrCreate(dir)
	if e != nil || !changed {
		t.Fatalf("create root: %v %v", changed, e)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	changed, e = r.EnsureDirectory("career_vault/taxonomy")
	if e != nil || !changed {
		t.Fatalf("mkdir: %v %v", changed, e)
	}
	changed, e = r.EnsureDirectory("career_vault/taxonomy")
	if e != nil || changed {
		t.Fatalf("existing mkdir: %v %v", changed, e)
	}
	before, e := r.ListNames()
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, []string{"career_vault"}) {
		t.Fatal("unexpected names")
	}
	if e := r.ProbeWritable(); e != nil {
		t.Fatal(e)
	}
	after, e := r.ListNames()
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("probe changed root membership")
	}
	r2, changed, e := OpenOrCreate(dir)
	if e != nil || changed {
		t.Fatalf("reopen: %v %v", changed, e)
	}
	if err := r2.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCreationPreservesOpaqueExistingAndPublishesBinary(t *testing.T) {
	dir := testHome(t)
	r := opened(t, dir)
	if _, e := r.EnsureDirectory("catalog"); e != nil {
		t.Fatal(e)
	}
	for _, content := range [][]byte{nil, []byte("new\x00\xff\n")} {
		name := "catalog/empty"
		if len(content) != 0 {
			name = "catalog/binary"
		}
		created, e := r.CreateFile(name, content)
		assertReplaceError(t, created, e, true, "")
		raw, present, e := r.ReadFile(name, 64)
		if e != nil || !present || !bytes.Equal(raw, content) {
			t.Fatal("published bytes changed")
		}
		created, e = r.CreateFile(name, []byte("replacement forbidden"))
		assertReplaceError(t, created, e, false, "FILE_EXISTS")
		raw, _, _ = r.ReadFile(name, 64)
		if !bytes.Equal(raw, content) {
			t.Fatal("existing bytes overwritten")
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "catalog"))
	if err != nil || len(entries) != 2 {
		t.Fatal("stage left behind")
	}
}

func TestObjectCreationUsesObjectBoundWithoutWeakeningMetadataBound(t *testing.T) {
	dir := testHome(t)
	r := opened(t, dir)
	content := bytes.Repeat([]byte{0x5a}, int(MaxReadBytes)+1)
	created, e := r.CreateFile("metadata-too-large", content)
	assertReplaceError(t, created, e, false, "WRITE_LIMIT_INVALID")
	created, e = r.CreateObjectFile("document.chdoc", content)
	assertReplaceError(t, created, e, true, "")
	raw, present, e := r.ReadObjectFile("document.chdoc", int64(len(content)))
	if e != nil || !present || !bytes.Equal(raw, content) {
		t.Fatal("object publication changed bytes")
	}
}

func TestCreationInvalidMissingClosedAndBounds(t *testing.T) {
	dir := testHome(t)
	r := opened(t, dir)
	for _, name := range []string{"../escape", "one//two", "AUX", "bad:stream"} {
		if created, e := r.EnsureDirectory(name); e == nil || created {
			t.Fatal("unsafe mkdir accepted")
		}
		if created, e := r.CreateFile(name, nil); e == nil || created {
			t.Fatal("unsafe create accepted")
		}
	}
	created, e := r.CreateFile("missing/child", nil)
	assertReplaceError(t, created, e, false, "PATH_PARENT_MISSING")
	if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatal("created missing parent")
	}
	if _, e := r.EnsureDirectory("directory"); e != nil {
		t.Fatal(e)
	}
	created, e = r.CreateFile("directory", nil)
	assertReplaceError(t, created, e, false, "PATH_NOT_REGULAR")
	put(t, dir, "ordinary", "opaque")
	created, e = r.EnsureDirectory("ordinary/child")
	assertReplaceError(t, created, e, false, "PATH_NOT_DIRECTORY")
	created, e = r.CreateFile("oversized", make([]byte, MaxReadBytes+1))
	assertReplaceError(t, created, e, false, "WRITE_LIMIT_INVALID")
	if _, _, e := OpenOrCreate("relative"); e == nil {
		t.Fatal("relative root accepted")
	}
	if _, _, e := OpenOrCreate(filepath.VolumeName(dir) + `\`); e == nil {
		t.Fatal("drive root accepted")
	}
	if _, _, e := OpenOrCreate(filepath.Join(dir, "ordinary", "child")); e == nil {
		t.Fatal("file ancestor accepted")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	created, e = r.EnsureDirectory("closed")
	assertReplaceError(t, created, e, false, "ROOT_CLOSED")
	created, e = r.CreateFile("closed", nil)
	assertReplaceError(t, created, e, false, "ROOT_CLOSED")
	code(t, r.ProbeWritable(), "ROOT_CLOSED")
	_, e = r.ListNames()
	code(t, e, "ROOT_CLOSED")
}

func TestCreationCaseAndLinkRefusal(t *testing.T) {
	dir := testHome(t)
	r := opened(t, dir)
	put(t, dir, "Existing/name", "untouched")
	if created, e := r.EnsureDirectory("existing/child"); e == nil || created {
		t.Fatal("case alias accepted")
	}
	if created, e := r.CreateFile("Existing/NAME", nil); e == nil || created {
		t.Fatal("leaf case alias accepted")
	}
	out := testHome(t)
	if err := os.Symlink(out, filepath.Join(dir, "link")); err != nil {
		t.Skip("Windows symlink privilege is unavailable")
	}
	if created, e := r.EnsureDirectory("link/child"); e == nil || created {
		t.Fatal("link mkdir accepted")
	}
	if created, e := r.CreateFile("link/file", nil); e == nil || created {
		t.Fatal("link create accepted")
	}
	if _, changed, e := OpenOrCreate(filepath.Join(dir, "link", "home")); e == nil || changed {
		t.Fatal("link root accepted")
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 0 {
		t.Fatal("outside link target changed")
	}
}

func TestCreationJunctionRefusesWritesWithoutSymlinkPrivilege(t *testing.T) {
	dir, target := testHome(t), testHome(t)
	r := opened(t, dir)
	createJunction(t, filepath.Join(dir, "junction"), target)
	if changed, e := r.EnsureDirectory("junction/child"); e == nil || changed {
		t.Fatal("junction directory creation accepted")
	}
	if created, e := r.CreateFile("junction/child", []byte("forbidden")); e == nil || created {
		t.Fatal("junction file creation accepted")
	}
	if createdRoot, changed, e := OpenOrCreate(filepath.Join(dir, "junction", "nested")); e == nil || changed {
		if createdRoot != nil {
			_ = createdRoot.Close()
		}
		t.Fatal("junction configured root accepted")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatal("junction target changed")
	}
}

func TestCreationFaultsAndCommittedOutcome(t *testing.T) {
	for _, mode := range []string{"write", "short", "sync", "close", "after_publish"} {
		t.Run(mode, func(t *testing.T) {
			dir := testHome(t)
			r := opened(t, dir)
			ops := createOperations{}
			switch mode {
			case "write":
				ops.write = func(*os.File, []byte) (int, error) { return 0, errors.New("secret injected text") }
			case "short":
				ops.write = func(f *os.File, b []byte) (int, error) { return f.Write(b[:1]) }
			case "sync":
				ops.sync = func(*os.File) error { return errors.New("secret injected text") }
			case "close":
				ops.close = func(f *os.File) error { _ = f.Close(); return errors.New("secret injected text") }
			case "after_publish":
				ops.afterPublish = func() *Error { return failure("SYNTHETIC_AFTER_PUBLISH") }
			}
			created, e := r.createFile("target", []byte("payload"), ops)
			if e == nil || created != (mode == "after_publish") || strings.Contains(e.Error(), "secret") {
				t.Fatalf("wrong failure outcome: %v %v", created, e)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if created {
				want = 1
			}
			if len(entries) != want {
				t.Fatal("stage not cleaned or target changed")
			}
		})
	}
}

func TestCreationConcurrentInstancesNeverOverwrite(t *testing.T) {
	dir := testHome(t)
	one, two := opened(t, dir), opened(t, dir)
	var wg sync.WaitGroup
	out := make(chan *Error, 2)
	for i, r := range []*Root{one, two} {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := r.CreateFile("target", []byte{byte(i)}); out <- e }()
	}
	wg.Wait()
	close(out)
	success, exists := 0, 0
	for e := range out {
		if e == nil {
			success++
		} else if e.Code == "FILE_EXISTS" {
			exists++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || exists != 1 {
		t.Fatal("native no-replace did not arbitrate creators")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "target"))
	if err != nil || len(raw) != 1 {
		t.Fatal("invalid publication")
	}
}

func TestCreationNativeInterveningCreatorAndABI(t *testing.T) {
	var info creationRenameInfo
	want := uintptr(20)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 12
	}
	if unsafe.Offsetof(info.FileName) != want || unsafe.Offsetof(info.FileNameLength) != want-4 {
		t.Fatal("FILE_RENAME_INFORMATION ABI mismatch")
	}
	dir := testHome(t)
	r := opened(t, dir)
	f, name, identity, e := newCreationStage(r.base)
	if e != nil {
		t.Fatal(e)
	}
	if _, err := f.Write([]byte("ours")); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	created, e := publishCreationWith(r.base, name, "target", identity, []byte("ours"), func() { put(t, dir, "target", "intervening") })
	assertReplaceError(t, created, e, false, "FILE_EXISTS")
	raw, err := os.ReadFile(filepath.Join(dir, "target"))
	if err != nil || string(raw) != "intervening" {
		t.Fatal("native operation overwrote intervening creator")
	}
	if e := cleanupCreationStage(r.base, name, identity); e != nil {
		t.Fatal(e)
	}
}

func TestCreationCleanupNeverDeletesForeignIdentity(t *testing.T) {
	dir := testHome(t)
	r := opened(t, dir)
	f, name, identity, e := newCreationStage(r.base)
	if e != nil {
		t.Fatal(e)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, "retained-owned")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, name, "foreign bytes")
	code(t, cleanupCreationStage(r.base, name, identity), "FILE_CLEANUP")
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || string(raw) != "foreign bytes" {
		t.Fatal("cleanup removed foreign name")
	}
	if _, err := os.Stat(filepath.Join(dir, "retained-owned")); err != nil {
		t.Fatal("cleanup removed moved owned file")
	}
}

func TestCreationStageChangedAndCleanupHardlinkRefusal(t *testing.T) {
	dir := testHome(t)
	r := opened(t, dir)
	created, e := r.createFile("target", []byte("ours"), createOperations{
		beforePublish: func(parent *os.Root, stage, _ string) {
			f, err := parent.OpenFile(stage, os.O_WRONLY|os.O_TRUNC, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Write([]byte("changed")); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		},
	})
	assertReplaceError(t, created, e, false, "FILE_STAGE")
	names, e := r.ListNames()
	if e != nil || len(names) != 0 {
		t.Fatal("changed stage not cleaned")
	}
	f, stage, identity, e := newCreationStage(r.base)
	if e != nil {
		t.Fatal(e)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, stage), filepath.Join(dir, "another-link")); err != nil {
		t.Fatal(err)
	}
	code(t, cleanupCreationStage(r.base, stage, identity), "FILE_CLEANUP")
	for _, name := range []string{stage, "another-link"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("hardlink cleanup deleted an entry")
		}
	}
}

func TestCreationListNamesOwnsResultsAndIsBounded(t *testing.T) {
	dir := testHome(t)
	r := opened(t, dir)
	put(t, dir, "z", "opaque")
	put(t, dir, "a", "opaque")
	names, e := r.ListNames()
	if e != nil || !reflect.DeepEqual(names, []string{"a", "z"}) {
		t.Fatal("names not sorted")
	}
	names[0] = "changed result"
	names, e = r.ListNames()
	if e != nil || names[0] != "a" {
		t.Fatal("names share output storage")
	}
	// Exact bound then one over, with empty ordinary fixture files only.
	for i := 2; i <= MaxDirectoryEntries; i++ {
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("entry-%05d", i)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if i == MaxDirectoryEntries-1 {
			names, e = r.ListNames()
			if e != nil || len(names) != MaxDirectoryEntries {
				t.Fatal("exact directory bound refused")
			}
		}
	}
	names, e = r.ListNames()
	if names != nil {
		t.Fatal("oversized directory returned partial names")
	}
	code(t, e, "PATH_DIRECTORY_LIMIT")
}
