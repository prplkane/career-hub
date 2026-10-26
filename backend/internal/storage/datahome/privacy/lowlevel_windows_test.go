//go:build windows

package privacy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRootPathAdmission(t *testing.T) {
	for _, value := range []string{`C:\owned\synthetic`, `c:/owned/synthetic/`, `C:\é\数据`} {
		if _, _, ok := rootParts(value); !ok {
			t.Fatalf("ordinary explicit path rejected: %q", value)
		}
	}
	for _, value := range []string{
		"", ".", `C:relative`, `C:\`, `C:\..\profile`, `C:\.\profile`,
		`C:\owned\\profile`, `C:\owned\name.`, `C:\owned\name `,
		`\\server\share\profile`, `\\?\C:\owned\profile`, `\\.\NUL`,
		`C:\owned\file:stream`, `C:\owned\*`, "C:\\owned\\\x00", "C:\\owned\\\xff",
		`C:\CON`, `C:\LPT1.txt`, `C:\COM²`, `C:\conin$`, `C:\CLOCK$`,
		`C:\` + strings.Repeat("x", 256), `C:\` + strings.Repeat("a\\", MaxDepth) + "b",
		`C:\` + strings.Repeat("x", MaxPathBytes),
	} {
		if _, _, ok := rootParts(value); ok {
			t.Fatalf("invalid explicit path accepted: %q", value)
		}
	}
	for _, value := range []string{"COM0", "LPT0", "company", "COM10", "CONTEXT.txt"} {
		if !validName(value) {
			t.Fatalf("ordinary component rejected: %q", value)
		}
	}
}

func TestCancellationAndMissingInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, done := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer done()
	for _, value := range []context.Context{nil, ctx, deadline} {
		if e := VerifyPrivateTree(value, "invalid"); e == nil || e.Code != "ACL_CONTEXT_CANCELED" {
			t.Fatal("context refusal missing")
		}
	}
	if e := VerifyPrivateTree(context.Background(), "invalid"); e == nil || e.Code != "ACL_SETUP_FAILED" {
		t.Fatal("invalid root refusal missing")
	}
	if e := VerifyPrivateTree(context.Background(), filepath.Join(t.TempDir(), "missing")); e == nil || e.Code != "ACL_SETUP_FAILED" {
		t.Fatal("missing root refusal missing")
	}
}

func TestHeldHandleAndMembershipChecks(t *testing.T) {
	container := t.TempDir()
	root := filepath.Join(container, "tree")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	parent, e := openEntry(container)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = parent.file.Close() })
	entry, e := openChild(parent, "tree")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = entry.file.Close() })
	entry.names, e = readNames(entry.file, MaxEntries)
	if e != nil || len(entry.names) != 0 {
		t.Fatal("initial membership failed")
	}
	if e = checkNames(entry); e != nil {
		t.Fatal(e)
	}
	if err := os.Rename(root, root+"-moved"); err == nil {
		t.Fatal("held directory unexpectedly allowed replacement")
	}
	if err := os.WriteFile(filepath.Join(root, "added"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if e = checkNames(entry); e == nil || e.Code != "ACL_TREE_LIMIT" {
		t.Fatal("new membership not detected before overrun")
	}
	entry.names = []string{"added", "deleted"}
	if e = checkNames(entry); e == nil || e.Code != "ACL_SETUP_FAILED" {
		t.Fatal("missing membership not detected")
	}
	if err := entry.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal("owned handle not released")
	}
	// Return the exclusively owned fixture to its t.TempDir cleanup location.
	if err := os.Rename(root+"-moved", root); err != nil {
		t.Fatal(err)
	}
	if _, e = readNames(entry.file, 1); e == nil || e.Code != "ACL_SETUP_FAILED" {
		t.Fatal("closed enumeration handle accepted")
	}
	budget := 0
	if _, e = readPrivateDACL(entry.file, "S-1-5-21-1000", false, &budget); e == nil || e.Code != "ACL_SETUP_FAILED" {
		t.Fatal("closed ACL handle accepted")
	}
	if _, e = inspectHandle(windows.InvalidHandle); e == nil {
		t.Fatal("invalid native handle accepted")
	}
	if _, e = openChild(nil, "tree"); e == nil {
		t.Fatal("nil parent accepted")
	}
	if _, e = openChild(parent, "../outside"); e == nil {
		t.Fatal("relative traversal accepted")
	}
	if e = checkNames(parent); e == nil {
		t.Fatal("unbound parent entry accepted")
	}
}

func TestIdentityComparison(t *testing.T) {
	base := windows.ByHandleFileInformation{VolumeSerialNumber: 1, FileIndexHigh: 2, FileIndexLow: 3, NumberOfLinks: 1}
	if !sameEntry(base, base) {
		t.Fatal("same native identity rejected")
	}
	for i := 0; i < 4; i++ {
		other := base
		switch i {
		case 0:
			other.VolumeSerialNumber++
		case 1:
			other.FileIndexHigh++
		case 2:
			other.FileIndexLow++
		case 3:
			other.FileAttributes = windows.FILE_ATTRIBUTE_DIRECTORY
		}
		if sameEntry(base, other) {
			t.Fatal("changed native identity accepted")
		}
	}
}
