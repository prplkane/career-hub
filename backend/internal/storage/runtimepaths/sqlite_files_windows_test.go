//go:build windows

package runtimepaths

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const sqliteFixtureRelative = "applications/career_hub.sqlite3"

func TestSQLiteFilesNativePinsAndAuxiliaryLifecycle(t *testing.T) {
	dir := testHome(t)
	put(t, dir, sqliteFixtureRelative, "original opaque fixture")
	main := filepath.Join(dir, filepath.FromSlash(sqliteFixtureRelative))
	r := opened(t, dir)
	g, e := guardSQLiteFiles(r, sqliteFixtureRelative)
	if e != nil || g == nil {
		t.Fatal("native guard failed", e)
	}
	t.Cleanup(func() { _ = g.Close() })
	mainPin, parentPin := g.main, g.directories[len(g.directories)-1]
	if err := os.Rename(main, main+"-moved"); err == nil {
		_ = os.Rename(main+"-moved", main)
		t.Fatal("main-file rename was not pinned")
	}
	parent := filepath.Dir(main)
	if err := os.Rename(parent, parent+"-moved"); err == nil {
		_ = os.Rename(parent+"-moved", parent)
		t.Fatal("database-parent rename was not pinned")
	}
	// No write-denying lock: native read-only SQLite still participates in
	// ordinary live database locking and auxiliary file creation/deletion.
	if err := os.WriteFile(main, []byte("committed external fixture"), 0600); err != nil {
		t.Fatal("guard unexpectedly denied ordinary in-place writes")
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		put(t, dir, sqliteFixtureRelative+suffix, "transient native companion")
		if e := g.Check(); e != nil {
			t.Fatal(e)
		}
		if err := os.Remove(main + suffix); err != nil {
			t.Fatal("guard retained an auxiliary name", err)
		}
	}
	if e := g.Close(); e != nil {
		t.Fatal(e)
	}
	if e := g.Close(); e != nil {
		t.Fatal("successful close was not idempotent", e)
	}
	code(t, g.Check(), "SQLITE_FILES_CLOSED")
	if !errors.Is(mainPin.Close(), os.ErrClosed) || !errors.Is(parentPin.Close(), os.ErrClosed) {
		t.Fatal("native file/directory handles leaked")
	}
	if err := os.Rename(main, main+"-moved"); err != nil {
		t.Fatal("closed guard still pinned database", err)
	}
	if err := os.Rename(main+"-moved", main); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(main)
	if err != nil || string(raw) != "committed external fixture" {
		t.Fatal("guard modified file bytes")
	}
}

func TestSQLiteFilesRefusesHardlinkedMainAndCompanions(t *testing.T) {
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		t.Run("suffix="+suffix, func(t *testing.T) {
			container := testHome(t)
			dir := filepath.Join(container, "home")
			put(t, dir, sqliteFixtureRelative, "ordinary database fixture")
			foreign := filepath.Join(container, "outside")
			put(t, container, "outside", "outside bytes preserved")
			target := filepath.Join(dir, filepath.FromSlash(sqliteFixtureRelative)) + suffix
			if suffix == "" {
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Link(foreign, target); err != nil {
				t.Fatal("native hardlink fixture unavailable", err)
			}
			g, e := guardSQLiteFiles(opened(t, dir), sqliteFixtureRelative)
			if g != nil {
				_ = g.Close()
				t.Fatal("hardlinked database file admitted")
			}
			code(t, e, "PATH_HARDLINK")
			if err := os.Remove(target); err != nil {
				t.Fatal("failed acquisition leaked a name pin", err)
			}
			raw, err := os.ReadFile(foreign)
			if err != nil || string(raw) != "outside bytes preserved" {
				t.Fatal("guard changed outside hardlink bytes")
			}
		})
	}
}

func TestSQLiteFilesMissingUnsafePathsAndStickyFailure(t *testing.T) {
	dir := testHome(t)
	r := opened(t, dir)
	g, e := guardSQLiteFiles(r, sqliteFixtureRelative)
	if g != nil || e != nil {
		t.Fatal("missing main did not remain missing", e)
	}
	names, err := os.ReadDir(dir)
	if err != nil || len(names) != 0 {
		t.Fatal("missing database check created files")
	}
	_, e = guardSQLiteFiles(r, "../outside")
	code(t, e, "PATH_INVALID_COMPONENT")
	put(t, dir, sqliteFixtureRelative, "db")
	main := filepath.Join(dir, filepath.FromSlash(sqliteFixtureRelative))
	if err := os.Mkdir(main+"-wal", 0700); err != nil {
		t.Fatal(err)
	}
	g, e = guardSQLiteFiles(r, sqliteFixtureRelative)
	if g != nil {
		_ = g.Close()
		t.Fatal("companion directory admitted")
	}
	code(t, e, "PATH_NOT_REGULAR")
	if err := os.Remove(main + "-wal"); err != nil {
		t.Fatal(err)
	}
	g, e = guardSQLiteFiles(r, sqliteFixtureRelative)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = g.Close() })
	put(t, dir, "outside", "opaque")
	if err := os.Link(filepath.Join(dir, "outside"), main+"-wal"); err != nil {
		t.Fatal(err)
	}
	code(t, g.Check(), "PATH_HARDLINK")
	code(t, g.Close(), "PATH_HARDLINK")
	if err := os.Remove(main + "-wal"); err != nil {
		t.Fatal("failed final check leaked companion handle")
	}
	code(t, g.Close(), "PATH_HARDLINK")
	if err := os.Rename(main, main+"-released"); err != nil {
		t.Fatal("failed final check leaked main pin")
	}
}

func TestSQLiteFilesRejectsNativeJunctionParent(t *testing.T) {
	dir, foreign := testHome(t), testHome(t)
	put(t, foreign, "career_hub.sqlite3", "outside fixture")
	createJunction(t, filepath.Join(dir, "applications"), foreign)
	g, e := guardSQLiteFiles(opened(t, dir), sqliteFixtureRelative)
	if g != nil {
		_ = g.Close()
		t.Fatal("junction database parent admitted")
	}
	code(t, e, "PATH_LINK")
	raw, err := os.ReadFile(filepath.Join(foreign, "career_hub.sqlite3"))
	if err != nil || string(raw) != "outside fixture" {
		t.Fatal("junction target changed")
	}
}

func TestSQLiteFilesCleanupContinuesAfterInvalidatedHandle(t *testing.T) {
	for _, closeRoot := range []bool{false, true} {
		dir := testHome(t)
		put(t, dir, sqliteFixtureRelative, "unchanged main")
		r := opened(t, dir)
		g, e := guardSQLiteFiles(r, sqliteFixtureRelative)
		if e != nil || g == nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = g.Close() })
		want := "FILE_CLOSE"
		if closeRoot {
			want = "ROOT_CLOSED"
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
		} else if err := g.main.Close(); err != nil {
			t.Fatal(err)
		}
		code(t, g.Close(), want)
		code(t, g.Close(), want)
		parent := filepath.Join(dir, "applications")
		if err := os.Rename(parent, parent+"-released"); err != nil {
			t.Fatal("one failed close prevented remaining handle cleanup")
		}
	}
	var absent *SQLiteFiles
	code(t, absent.Check(), "SQLITE_FILES_CLOSED")
	if absent.Close() != nil {
		t.Fatal("nil guard close failed")
	}
}

func TestSQLiteFilesNativeOwnedAPI(t *testing.T) {
	if dir := os.Getenv("CAREERHUB_SQLITE_FILES_HELPER_ROOT"); dir != "" {
		r := opened(t, dir)
		g, e := r.GuardSQLiteFiles(sqliteFixtureRelative)
		if g != nil {
			t.Fatal("guard exists without process claim")
		}
		code(t, e, "OWNERSHIP_REQUIRED")
		if e := r.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
		other := opened(t, filepath.Join(filepath.Dir(dir), "other"))
		_, e = other.GuardSQLiteFiles(sqliteFixtureRelative)
		code(t, e, "OWNERSHIP_REQUIRED")
		alias := opened(t, strings.ToUpper(dir))
		g, e = alias.GuardSQLiteFiles(sqliteFixtureRelative)
		if e != nil || g == nil {
			t.Fatal("claimed native identity was not admitted", e)
		}
		if e := g.Close(); e != nil {
			t.Fatal(e)
		}
		return // Only this helper test runs; OS exit releases process ownership.
	}
	container := testHome(t)
	dir := filepath.Join(container, "home")
	put(t, dir, sqliteFixtureRelative, "synthetic opaque main")
	put(t, filepath.Join(container, "other"), sqliteFixtureRelative, "other main")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSQLiteFilesNativeOwnedAPI$")
	cmd.Env = append(os.Environ(), "CAREERHUB_SQLITE_FILES_HELPER_ROOT="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = 500 * time.Millisecond
	if output, err := cmd.CombinedOutput(); err != nil || ctx.Err() != nil {
		t.Fatalf("owned SQLite helper failed: %.1024s", output)
	}
}
