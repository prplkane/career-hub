//go:build windows

package runtimepaths

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"
)

const directoryMoveChild = "CAREERHUB_DIRECTORY_MOVE_CHILD"

func TestDirectoryMoveWindowsChild(t *testing.T) {
	if os.Getenv(directoryMoveChild) == "1" {
		directoryMoveChildCases(t)
		return
	}
	rootPath := testHome(t)
	for _, relative := range []string{
		"source",
		"second",
		"third",
		"fourth",
		"fifth",
		"destinations",
		"destinations/existing",
	} {
		if err := os.MkdirAll(filepath.Join(rootPath, filepath.FromSlash(relative)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	put(t, rootPath, "source/payload", "one")
	put(t, rootPath, "second/payload", "two")
	put(t, rootPath, "third/payload", "three")
	put(t, rootPath, "fourth/payload", "four")
	put(t, rootPath, "fifth/payload", "five")

	root := opened(t, rootPath)
	outcome, problem := root.MoveDirectoryNoReplace("source", "destinations/unclaimed")
	if outcome != DirectoryMoveNotMoved || problem == nil || problem.Code != "OWNERSHIP_REQUIRED" {
		t.Fatalf("unclaimed move admitted: %q / %#v", outcome, problem)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestDirectoryMoveWindowsChild$")
	command.Env = append(os.Environ(), directoryMoveChild+"=1", "CAREERHUB_DIRECTORY_MOVE_ROOT="+rootPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("directory move child failed: %v: %.2000s", err, output)
	}

	for relative, want := range map[string]string{
		"destinations/moved/payload":     "one",
		"second/payload":                 "two",
		"destinations/existing":          "",
		"destinations/uncertain/payload": "three",
		"fourth/payload":                 "four",
		"destinations/closed/payload":    "five",
		"destinations/intervening":       "",
	} {
		info, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("missing retained state %s: %v", relative, err)
		}
		if want == "" {
			if !info.IsDir() {
				t.Fatalf("expected directory %s", relative)
			}
			continue
		}
		raw, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(relative)))
		if err != nil || string(raw) != want {
			t.Fatalf("unexpected payload %s: %q / %v", relative, raw, err)
		}
	}
	if _, err := os.Stat(filepath.Join(rootPath, "source")); !os.IsNotExist(err) {
		t.Fatal("committed source name remained")
	}
}

func directoryMoveChildCases(t *testing.T) {
	rootPath := os.Getenv("CAREERHUB_DIRECTORY_MOVE_ROOT")
	root, problem := Open(rootPath)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	defer root.Close()
	if problem := root.ClaimForProcess(); problem != nil {
		t.Fatal(problem.Code)
	}

	outcome, problem := root.MoveDirectoryNoReplace("source", "destinations/moved")
	assertDirectoryMove(t, outcome, problem, DirectoryMoveMoved, "")
	outcome, problem = root.MoveDirectoryNoReplace("second", "destinations/existing")
	assertDirectoryMove(t, outcome, problem, DirectoryMoveNotMoved, "DIRECTORY_EXISTS")
	outcome, problem = root.moveDirectoryNoReplace("third", "destinations/uncertain", directoryMoveOperations{
		move: func(source *os.File, target *os.Root, name string) error {
			if err := moveOwnedDirectory(source, target, name); err != nil {
				return err
			}
			return errors.New("synthetic error after commit")
		},
	})
	assertDirectoryMove(t, outcome, problem, DirectoryMoveMoved, "DIRECTORY_MOVE")
	outcome, problem = root.moveDirectoryNoReplace("fourth", "destinations/intervening", directoryMoveOperations{
		beforeMove: func() {
			if err := os.Mkdir(filepath.Join(rootPath, "destinations", "intervening"), 0700); err != nil {
				t.Fatal(err)
			}
		},
	})
	assertDirectoryMove(t, outcome, problem, DirectoryMoveNotMoved, "DIRECTORY_EXISTS")
	outcome, problem = root.MoveDirectoryNoReplace("destinations", "destinations/nested")
	assertDirectoryMove(t, outcome, problem, DirectoryMoveNotMoved, "DIRECTORY_MOVE_INVALID")
	outcome, problem = root.moveDirectoryNoReplace("fifth", "destinations/closed", directoryMoveOperations{
		close: func(source *os.File) error {
			_ = source.Close()
			return errors.New("synthetic close error")
		},
	})
	assertDirectoryMove(t, outcome, problem, DirectoryMoveMoved, "FILE_CLOSE")
}

func assertDirectoryMove(t *testing.T, got DirectoryMoveOutcome, problem *Error, want DirectoryMoveOutcome, code string) {
	t.Helper()
	if got != want {
		t.Fatalf("outcome %q, want %q", got, want)
	}
	if code == "" {
		if problem != nil {
			t.Fatalf("unexpected problem: %#v", problem)
		}
		return
	}
	if problem == nil || problem.Code != code {
		t.Fatalf("problem %#v, want %s", problem, code)
	}
}

func TestDirectoryMoveRenameABI(t *testing.T) {
	var info directoryMoveRenameInfo
	want := uintptr(20)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 12
	}
	if unsafe.Offsetof(info.FileName) != want || unsafe.Offsetof(info.FileNameLength) != want-4 {
		t.Fatal("FILE_RENAME_INFORMATION ABI mismatch")
	}
}
