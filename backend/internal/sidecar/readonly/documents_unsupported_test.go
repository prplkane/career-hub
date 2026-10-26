//go:build !windows

package readonly

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/storage/documents/keys"
)

func TestDocumentStorageUnsupportedBeforeKeysOrFiles(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	value, e := readOwnedDocuments(context.Background(), root, *profileInfo(), func() (keys.Backend, error) { t.Fatal("unsupported storage accessed credentials"); return nil, nil })
	if value != nil {
		t.Fatal("unsupported storage returned metadata")
	}
	requireCode(t, e, "DOCUMENT_STORAGE_UNSUPPORTED")
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 0 {
		t.Fatal("unsupported read created storage")
	}
}
