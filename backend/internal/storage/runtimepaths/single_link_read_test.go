//go:build windows || linux || darwin

package runtimepaths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSingleLinkReadPreflight(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(base, "source.txt")
	if e := os.WriteFile(path, []byte("synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	root, pe := Open(base)
	if pe != nil {
		t.Fatal(pe)
	}
	defer root.Close()
	if b, p, e := root.ReadSingleLinkFile("source.txt", 32); e != nil || !p || string(b) != "synthetic" {
		t.Fatal("ordinary preflight")
	}
	if b, p, e := root.ReadSingleLinkFile("missing", 32); e != nil || p || b != nil {
		t.Fatal("absent preflight")
	}
	if e := os.Link(path, filepath.Join(base, "alias.txt")); e != nil {
		t.Fatal(e)
	}
	if b, p, e := root.ReadSingleLinkFile("source.txt", 32); e == nil || e.Code != "PATH_HARDLINK" || p || b != nil {
		t.Fatal("hardlinked mutation preflight", e)
	}
	if b, p, e := root.ReadFile("source.txt", 32); e != nil || !p || string(b) != "synthetic" {
		t.Fatal("general read policy changed")
	}
	if b, p, e := root.ReadSingleLinkFile("source.txt", MaxReadBytes+1); e == nil || p || b != nil {
		t.Fatal("read limit")
	}
}
