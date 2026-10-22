//go:build windows

package readonly

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestPreflightNativeJunctionMissingChildRefusedWithoutWrites(t *testing.T) {
	base, start := fixture(t)
	link := filepath.Join(base, "junction-to-resources")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"New-Item -ItemType Junction -Path "+quote(link)+" -Target "+quote(start.ResourceRoot)+" -ErrorAction Stop | Out-Null")
	cmd.WaitDelay = 2 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		t.Fatal("synthetic junction fixture failed")
	}
	// Remove only the exact owned link before temporary-tree cleanup; do not walk
	// the target or use recursive shell deletion, including on test failure.
	t.Cleanup(func() {
		if err := os.Remove(link); err != nil {
			t.Error("owned junction cleanup failed")
		}
	})
	linkPtr, err := windows.UTF16PtrFromString(link)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := windows.GetFileAttributes(linkPtr)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 || attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		t.Fatal("junction fixture was not a native link")
	}
	before := tree(t, start.ResourceRoot)
	start.DataRoot = filepath.Join(link, "missing", "home")
	requireCode(t, Preflight(start), "DATA_HOME_UNAVAILABLE")
	if !reflect.DeepEqual(before, tree(t, start.ResourceRoot)) {
		t.Fatal("junction target changed")
	}
}

func TestPreflightNativeShortAliasOfResourcesWithMissingChild(t *testing.T) {
	// The contract under test is alias admission, not renaming a newly written
	// directory. Create the long native name directly before obtaining its alias.
	_, start := fixtureWithResourceName(t, "Resource Directory With Long Name")
	long := start.ResourceRoot
	pointer, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 4096)
	n, err := windows.GetShortPathName(pointer, &buffer[0], uint32(len(buffer)))
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 || n >= uint32(len(buffer)) {
		t.Fatal("unexpected alias conversion length")
	}
	short := windows.UTF16ToString(buffer[:n])
	if short == long {
		t.Skip("native 8.3 alias generation disabled")
	}
	start.DataRoot = filepath.Join(short, "missing", "home")
	if lexicalOverlap(start.ResourceRoot, start.DataRoot) {
		t.Fatal("fixture did not exercise nonlexical alias")
	}
	requireCode(t, Preflight(start), "SIDECAR_PATH_BOUNDARY_INVALID")
	if _, err := os.Lstat(filepath.Join(long, "missing")); !os.IsNotExist(err) {
		t.Fatal("native alias caused resource mutation")
	}
}
