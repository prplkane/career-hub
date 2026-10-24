//go:build windows

package runtimepaths

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReplaceWindowsRefusesJunctionWithoutWritingTarget(t *testing.T) {
	dir, root := replacementFixture(t)
	target := testHome(t)
	put(t, target, "terms.yaml", "outside synthetic bytes")
	createJunction(t, filepath.Join(dir, "junction"), target)
	committed, problem := root.ReplaceFile("junction/terms.yaml", []byte("outside synthetic bytes"), []byte("must not write"))
	assertReplaceError(t, committed, problem, false, "PATH_LINK")
	raw, err := os.ReadFile(filepath.Join(target, "terms.yaml"))
	if err != nil || string(raw) != "outside synthetic bytes" {
		t.Fatal("replacement touched its synthetic junction target")
	}
	assertReplacementState(t, dir, "old\x00\xff")
}

func TestReplaceWindowsLockedTarget(t *testing.T) {
	dir, root := replacementFixture(t)
	path, err := syscall.UTF16PtrFromString(filepath.Join(dir, "catalog", "terms.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(path, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal("cannot lock synthetic replacement target against delete sharing")
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = syscall.CloseHandle(handle)
		}
	})
	committed, problem := root.ReplaceFile("catalog/terms.yaml", []byte("old\x00\xff"), []byte("replacement"))
	assertReplaceError(t, committed, problem, false, "FILE_RENAME")
	assertReplacementState(t, dir, "old\x00\xff")
	closed = true
	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	committed, problem = root.ReplaceFile("catalog/terms.yaml", []byte("old\x00\xff"), []byte("replacement"))
	assertReplaceError(t, committed, problem, true, "")
	assertReplacementState(t, dir, "replacement")
}
