//go:build windows

package runtimepaths

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNativeJunctionRejected(t *testing.T) {
	dir := testHome(t)
	target := testHome(t)
	put(t, target, "outside", "synthetic")
	link := filepath.Join(dir, "junction")
	createJunction(t, link, target)
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !isLinkLike(info) {
		t.Fatal("junction metadata was not rejected")
	}
	r := opened(t, dir)
	code(t, r.Check("junction/outside"), "PATH_LINK")
	_, _, e := r.ReadFile("junction/outside", 100)
	code(t, e, "PATH_LINK")
	_, e = Open(link)
	code(t, e, "ROOT_LINK")
	raw, err := os.ReadFile(filepath.Join(target, "outside"))
	if err != nil || string(raw) != "synthetic" {
		t.Fatal("junction target changed")
	}
}

func TestNativeDanglingJunctionAndIndependentRead(t *testing.T) {
	dir := testHome(t)
	target := filepath.Join(testHome(t), "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	r := opened(t, dir)
	if e := r.Check("later/optional"); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(dir, "later")
	createJunction(t, link, target)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	code(t, r.Check("later/optional"), "PATH_LINK")
	_, _, e := r.ReadFile("later/optional", 100)
	code(t, e, "PATH_LINK")
	_, e = Open(link)
	code(t, e, "ROOT_LINK")
}

func createJunction(t *testing.T, link, target string) {
	t.Helper()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "New-Item -ItemType Junction -Path "+quote(link)+" -Target "+quote(target)+" -ErrorAction Stop | Out-Null")
	cmd.WaitDelay = 2 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			t.Skip("native junction fixture timed out; no junction evidence")
		}
		t.Skipf("native junction fixture unavailable: %v", err)
	}
}
