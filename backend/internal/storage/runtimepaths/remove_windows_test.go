//go:build windows

package runtimepaths

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRemoveWindowsRefusesLinksAndLockedFiles(t *testing.T) {
	t.Run("junction", func(t *testing.T) {
		dir, root := replacementFixture(t)
		outside := testHome(t)
		put(t, outside, "intent.json", "synthetic outside")
		createJunction(t, filepath.Join(dir, "junction"), outside)
		removed, e := root.RemoveFile("junction/intent.json", []byte("synthetic outside"))
		if removed || e == nil || e.Code != "PATH_LINK" {
			t.Fatal("junction deletion admitted")
		}
		raw, e2 := os.ReadFile(filepath.Join(outside, "intent.json"))
		if e2 != nil || string(raw) != "synthetic outside" {
			t.Fatal("junction target removed")
		}
	})
	t.Run("hardlink", func(t *testing.T) {
		dir, root := replacementFixture(t)
		p := filepath.Join(dir, "catalog", "terms.yaml")
		alias := filepath.Join(dir, "catalog", "alias.yaml")
		if e := os.Link(p, alias); e != nil {
			t.Fatal("synthetic hardlink unavailable")
		}
		removed, e := root.RemoveFile("catalog/terms.yaml", []byte("old\x00\xff"))
		if removed || e == nil || e.Code != "PATH_HARDLINK" {
			t.Fatal("hardlinked intent removed")
		}
		for _, p := range []string{p, alias} {
			raw, e := os.ReadFile(p)
			if e != nil || string(raw) != "old\x00\xff" {
				t.Fatal("hardlink changed")
			}
		}
	})
	t.Run("locked", func(t *testing.T) {
		dir, root := replacementFixture(t)
		path, e := syscall.UTF16PtrFromString(filepath.Join(dir, "catalog", "terms.yaml"))
		if e != nil {
			t.Fatal(e)
		}
		handle, e := syscall.CreateFile(path, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
		if e != nil {
			t.Fatal("native fixture lock unavailable")
		}
		defer syscall.CloseHandle(handle)
		removed, problem := root.RemoveFile("catalog/terms.yaml", []byte("old\x00\xff"))
		if removed || problem == nil || problem.Code != "FILE_REMOVE" {
			t.Fatal("locked intent claimed removed")
		}
		assertReplacementState(t, dir, "old\x00\xff")
	})
}
