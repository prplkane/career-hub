//go:build windows

package runtimepaths

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestObservedRunsRejectsJunction(t *testing.T) {
	dir := testHome(t)
	target := testHome(t)
	createJunction(t, filepath.Join(dir, "run_link"), target)
	root := opened(t, dir)
	if observed, e := root.ObserveRunDirectories(); observed != nil || e == nil || e.Code != "PATH_LINK" {
		t.Fatalf("junction accepted: %v", e)
	}
}

func TestObservedRunsPhaseTiming(t *testing.T) {
	if os.Getenv("CAREERHUB_PROFILE_RUNS") != "1" {
		t.Skip("opt-in synthetic timing")
	}
	dir := testHome(t)
	for i := 0; i < 5000; i++ {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("run_%05d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	root := opened(t, dir)
	started := time.Now()
	observed, e := root.ObserveRunDirectories()
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("initial scan: %s", time.Since(started))
	started = time.Now()
	for _, name := range observed.Names() {
		if _, e := observed.ReadChildFile(name, "extracted.yaml", 2<<20); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("negative leaf reads: %s", time.Since(started))
	started = time.Now()
	if e := observed.Recheck(); e != nil {
		t.Fatal(e)
	}
	t.Logf("final recheck: %s", time.Since(started))
}

func TestObservedRunsNativeDirectoryLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("native 10,001-directory boundary")
	}
	dir := testHome(t)
	for i := 0; i <= MaxDirectoryEntries; i++ {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("run_%05d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	root := opened(t, dir)
	if observed, e := root.ObserveRunDirectories(); observed != nil || e == nil || e.Code != "PATH_DIRECTORY_LIMIT" {
		t.Fatalf("over-limit scan: %v", e)
	}
}
