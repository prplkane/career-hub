package vaultread

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func nativeRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNativeMissingVaultDoesNotCreateParents(t *testing.T) {
	root := nativeRoot(t)
	state, err := openVault(context.Background(), root)
	if state != nil || err == nil || err.Code != "VAULT_FILE_MISSING" || err.Source != "profile" {
		t.Fatalf("missing vault result: %v", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil || len(entries) != 0 {
		t.Fatal("read created a missing Vault directory")
	}
}

func TestNativeLoaderPreservesExactBytesAndFileMetadata(t *testing.T) {
	root := nativeRoot(t)
	f := shapeFixture()
	f.files[schemas[profileIndex].path] = []byte("name: '  Ω Synthetic  '\r\ncustom: false\r\n")
	before := map[string]os.FileInfo{}
	for name, raw := range f.files {
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = info
	}
	state, err := openVault(context.Background(), root)
	if err != nil || state == nil {
		t.Fatalf("synthetic load failed: %v", err)
	}
	for index, spec := range schemas {
		if state.revisions[index] != fmt.Sprintf("sha256:%x", sha256.Sum256(f.files[spec.path])) {
			t.Fatal("same-read file revision changed")
		}
	}
	entries, readErr := os.ReadDir(filepath.Join(root, "career_vault"))
	if readErr != nil || len(entries) != vaultFileCount {
		t.Fatal("file membership changed")
	}
	for name, raw := range f.files {
		target := filepath.Join(root, filepath.FromSlash(name))
		after, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(target)
		if err != nil || !reflect.DeepEqual(got, raw) || !os.SameFile(before[name], after) ||
			before[name].Mode() != after.Mode() || !before[name].ModTime().Equal(after.ModTime()) {
			t.Fatal("read changed a Vault file")
		}
	}
}
