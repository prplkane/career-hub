package runtimepaths

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func replacementFixture(t *testing.T) (string, *Root) {
	t.Helper()
	dir := testHome(t)
	put(t, dir, "catalog/terms.yaml", "old\x00\xff")
	put(t, dir, "catalog/unrelated.yaml", "untouched")
	return dir, opened(t, dir)
}

func assertReplacementState(t *testing.T, dir, expected string) {
	t.Helper()
	for name, want := range map[string]string{"terms.yaml": expected, "unrelated.yaml": "untouched"} {
		raw, err := os.ReadFile(filepath.Join(dir, "catalog", name))
		if err != nil || string(raw) != want {
			t.Fatalf("synthetic %s has unexpected bytes", name)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "catalog"))
	if err != nil || len(entries) != 2 {
		t.Fatal("replacement left a stage or changed unrelated directory membership")
	}
}

func assertReplaceError(t *testing.T, committed bool, problem *Error, wantCommitted bool, wantCode string) {
	t.Helper()
	if committed != wantCommitted {
		t.Fatalf("committed=%v; want %v", committed, wantCommitted)
	}
	if wantCode == "" {
		if problem != nil {
			t.Fatal(problem)
		}
		return
	}
	if problem == nil || *problem != (Error{Code: wantCode}) || problem.Error() != "managed catalog path: "+wantCode {
		t.Fatalf("error=%v; want only fixed code %s", problem, wantCode)
	}
}

func TestReplaceExistingBytesAndCleanup(t *testing.T) {
	dir, root := replacementFixture(t)
	for _, next := range [][]byte{[]byte("new\xff\x00\n"), nil, []byte("final")} {
		expected, present, problem := root.ReadFile("catalog/terms.yaml", MaxReadBytes)
		if problem != nil || !present {
			t.Fatal("cannot read synthetic expected bytes")
		}
		committed, problem := root.ReplaceFile("catalog/terms.yaml", expected, next)
		assertReplaceError(t, committed, problem, true, "")
		assertReplacementState(t, dir, string(next))
	}
	committed, problem := root.ReplaceFile("catalog/terms.yaml", []byte("stale"), []byte("not saved"))
	assertReplaceError(t, committed, problem, false, "FILE_CONFLICT")
	assertReplacementState(t, dir, "final")
}

func TestReplaceRefusesMissingUnsafeClosedAndOversized(t *testing.T) {
	dir, root := replacementFixture(t)
	for _, name := range []string{"missing/terms.yaml", "catalog/missing.yaml"} {
		committed, problem := root.ReplaceFile(name, nil, []byte("new"))
		assertReplaceError(t, committed, problem, false, "FILE_MISSING")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatal("replacement bootstrapped a missing parent")
	}
	for _, name := range []string{"../outside", "catalog/../terms.yaml", "catalog//terms.yaml", "catalog/terms.yaml:stream", "CON", "catalog/terms.yaml/", "/absolute"} {
		if committed, problem := root.ReplaceFile(name, nil, nil); committed || problem == nil {
			t.Fatal("replacement accepted an unsafe path")
		}
	}
	committed, problem := root.ReplaceFile("catalog", nil, nil)
	assertReplaceError(t, committed, problem, false, "PATH_NOT_REGULAR")
	large := make([]byte, MaxReadBytes+1)
	for _, args := range [][2][]byte{{large, nil}, {nil, large}} {
		committed, problem := root.ReplaceFile("catalog/terms.yaml", args[0], args[1])
		assertReplaceError(t, committed, problem, false, "WRITE_LIMIT_INVALID")
	}
	assertReplacementState(t, dir, "old\x00\xff")
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	committed, problem = root.ReplaceFile("catalog/terms.yaml", nil, nil)
	assertReplaceError(t, committed, problem, false, "ROOT_CLOSED")
	committed, problem = (*Root)(nil).ReplaceFile("catalog/terms.yaml", nil, nil)
	assertReplaceError(t, committed, problem, false, "ROOT_CLOSED")
}

func TestReplaceStageFaultsDoNotCommit(t *testing.T) {
	privateCause := errors.New("synthetic private cause that must not escape")
	for _, test := range []struct {
		name, code string
		ops        replaceOperations
	}{
		{"write", "FILE_WRITE", replaceOperations{write: func(*os.File, []byte) (int, error) { return 0, privateCause }}},
		{"short-write", "FILE_WRITE", replaceOperations{write: func(f *os.File, p []byte) (int, error) { return f.Write(p[:1]) }}},
		{"wrong-stage-bytes", "FILE_STAGE", replaceOperations{write: func(f *os.File, p []byte) (int, error) { return f.Write(bytes.Repeat([]byte{'X'}, len(p))) }}},
		{"sync", "FILE_SYNC", replaceOperations{sync: func(*os.File) error { return privateCause }}},
		{"close", "FILE_CLOSE", replaceOperations{close: func(f *os.File) error { _ = f.Close(); return privateCause }}},
		{"rename", "FILE_RENAME", replaceOperations{rename: func(*os.Root, string, string) error { return privateCause }}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir, root := replacementFixture(t)
			committed, problem := root.replaceFile("catalog/terms.yaml", []byte("old\x00\xff"), []byte("replacement"), test.ops)
			assertReplaceError(t, committed, problem, false, test.code)
			assertReplacementState(t, dir, "old\x00\xff")
		})
	}
}

func TestReplaceRechecksExpectedAfterStaging(t *testing.T) {
	dir, root := replacementFixture(t)
	ops := replaceOperations{close: func(file *os.File) error {
		if err := file.Close(); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "catalog", "terms.yaml"), []byte("external update"), 0600)
	}}
	committed, problem := root.replaceFile("catalog/terms.yaml", []byte("old\x00\xff"), []byte("must not overwrite"), ops)
	assertReplaceError(t, committed, problem, false, "FILE_CONFLICT")
	assertReplacementState(t, dir, "external update")
}

func TestReplaceRechecksMovedParentAndCleansRetainedStage(t *testing.T) {
	dir, root := replacementFixture(t)
	original, moved := filepath.Join(dir, "catalog"), filepath.Join(dir, "moved")
	ops := replaceOperations{close: func(file *os.File) error {
		if err := file.Close(); err != nil {
			return err
		}
		return os.Rename(original, moved)
	}}
	committed, problem := root.replaceFile("catalog/terms.yaml", []byte("old\x00\xff"), []byte("must not overwrite"), ops)
	assertReplaceError(t, committed, problem, false, "PATH_CHANGED")
	if err := os.Rename(moved, original); err != nil {
		t.Fatal("cannot restore synthetic moved parent after handles were released")
	}
	assertReplacementState(t, dir, "old\x00\xff")
}

func TestReplacePostCommitFailuresRemainCommitted(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "post-check", true: "rename-effect-observed"}[ambiguous], func(t *testing.T) {
			dir, root := replacementFixture(t)
			wantCode, wantBytes := "FILE_CONFLICT", "later external update"
			if ambiguous {
				wantCode, wantBytes = "FILE_RENAME", "replacement"
			}
			ops := replaceOperations{rename: func(parent *os.Root, source, target string) error {
				if err := parent.Rename(source, target); err != nil {
					return err
				}
				if ambiguous {
					return errors.New("synthetic error after a completed rename")
				}
				return os.WriteFile(filepath.Join(dir, "catalog", "terms.yaml"), []byte(wantBytes), 0600)
			}}
			committed, problem := root.replaceFile("catalog/terms.yaml", []byte("old\x00\xff"), []byte("replacement"), ops)
			assertReplaceError(t, committed, problem, true, wantCode)
			assertReplacementState(t, dir, wantBytes)
		})
	}
}

func TestReplaceSerializesOwnRootCalls(t *testing.T) {
	dir, root := replacementFixture(t)
	var wg sync.WaitGroup
	results := make(chan struct {
		committed bool
		problem   *Error
	}, 2)
	for _, next := range []string{"first", "second"} {
		wg.Go(func() {
			committed, problem := root.ReplaceFile("catalog/terms.yaml", []byte("old\x00\xff"), []byte(next))
			results <- struct {
				committed bool
				problem   *Error
			}{committed, problem}
		})
	}
	wg.Wait()
	close(results)
	commits, conflicts := 0, 0
	for result := range results {
		if result.committed && result.problem == nil {
			commits++
		} else if !result.committed && result.problem != nil && result.problem.Code == "FILE_CONFLICT" {
			conflicts++
		} else {
			t.Fatal("unexpected concurrent replacement outcome")
		}
	}
	if commits != 1 || conflicts != 1 {
		t.Fatal("own-root replacement calls were not serialized around comparison")
	}
	raw, _, problem := root.ReadFile("catalog/terms.yaml", MaxReadBytes)
	if problem != nil || !bytes.Equal(raw, []byte("first")) && !bytes.Equal(raw, []byte("second")) {
		t.Fatal("concurrent replacement saved unexpected bytes")
	}
	assertReplacementState(t, dir, string(raw))
}

func TestReplaceRejectsHardLinksAndPreservesAliases(t *testing.T) {
	dir, root := replacementFixture(t)
	alias := filepath.Join(dir, "alias")
	if err := os.Link(filepath.Join(dir, "catalog", "terms.yaml"), alias); err != nil {
		t.Fatal("cannot create owned synthetic hardlink")
	}
	committed, problem := root.ReplaceFile("catalog/terms.yaml", []byte("old\x00\xff"), []byte("not saved"))
	assertReplaceError(t, committed, problem, false, "PATH_HARDLINK")
	assertReplacementState(t, dir, "old\x00\xff")
	content, err := os.ReadFile(alias)
	if err != nil || string(content) != "old\x00\xff" {
		t.Fatal("replacement modified the synthetic hardlink alias")
	}
}

func TestReplaceCleanupDoesNotDeleteAnUnownedName(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "own-stage", "owned")
	put(t, dir, "other", "not owned")
	parent, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	identity, err := parent.Lstat("own-stage")
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Rename("other", "own-stage"); err != nil {
		t.Fatal(err)
	}
	code(t, removeOwnedStage(parent, "own-stage", identity), "FILE_CLEANUP")
	raw, err := parent.ReadFile("own-stage")
	if err != nil || strings.TrimSpace(string(raw)) != "not owned" {
		t.Fatal("cleanup deleted another entry that occupied the stage name")
	}
}
