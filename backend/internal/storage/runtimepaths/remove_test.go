package runtimepaths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveExactFileAndPostDeleteFailures(t *testing.T) {
	for _, mode := range []string{"success", "before", "after", "new-leaf"} {
		t.Run(mode, func(t *testing.T) {
			dir, root := replacementFixture(t)
			remove := func(parent *os.Root, name string) error {
				if mode == "before" {
					return errors.New("SYNTHETIC_PRIVATE_ERROR")
				}
				if err := parent.Remove(name); err != nil {
					return err
				}
				if mode == "new-leaf" {
					return parent.WriteFile(name, []byte("external replacement"), 0600)
				}
				if mode == "after" {
					return errors.New("SYNTHETIC_PRIVATE_ERROR")
				}
				return nil
			}
			removed, e := root.removeFile("catalog/terms.yaml", []byte("old\x00\xff"), remove)
			if removed != (mode != "before") || (e != nil) != (mode != "success") {
				t.Fatalf("outcome %v %v", removed, e)
			}
			if e != nil && e.Code != "FILE_REMOVE" && e.Code != "PATH_CHANGED" {
				t.Fatal(e)
			}
			if raw, err := os.ReadFile(filepath.Join(dir, "catalog/unrelated.yaml")); err != nil || string(raw) != "untouched" {
				t.Fatal("unrelated file changed")
			}
			if mode == "new-leaf" {
				if raw, err := os.ReadFile(filepath.Join(dir, "catalog/terms.yaml")); err != nil || string(raw) != "external replacement" {
					t.Fatal("new leaf was deleted")
				}
			}
		})
	}
}

func TestRemoveRejectsStaleUnsafeMissingAndClosed(t *testing.T) {
	dir, root := replacementFixture(t)
	for _, path := range []string{"../outside", "catalog", "catalog/absent.yaml"} {
		if removed, e := root.RemoveFile(path, nil); removed || e == nil {
			t.Fatal("unsafe remove accepted")
		}
	}
	if removed, e := root.RemoveFile("catalog/terms.yaml", []byte("stale")); removed || e == nil || e.Code != "FILE_CONFLICT" {
		t.Fatal("stale bytes removed")
	}
	assertReplacementState(t, dir, "old\x00\xff")
	if removed, e := root.RemoveFile("catalog/terms.yaml", []byte("old\x00\xff")); !removed || e != nil {
		t.Fatal(e)
	}
	if removed, e := root.RemoveFile("catalog/terms.yaml", []byte("old\x00\xff")); removed || e == nil || e.Code != "FILE_MISSING" {
		t.Fatal("missing file claimed removed")
	}
	root.Close()
	for _, r := range []*Root{root, nil} {
		if removed, e := r.RemoveFile("catalog/terms.yaml", nil); removed || e == nil || e.Code != "ROOT_CLOSED" {
			t.Fatal("closed root admitted")
		}
	}
}
