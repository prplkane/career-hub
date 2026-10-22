//go:build windows

package applicationwrite

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
)

type resumeState struct {
	Tables map[string]table
	Files  map[string]map[string]any
}

func resumeRecipe(t *testing.T, v any) []byte {
	t.Helper()
	recipe, ok := v.(map[string]any)
	if !ok {
		t.Fatal("recipe")
	}
	if s, ok := recipe["text"].(string); ok {
		return []byte(s)
	}
	if s, ok := recipe["hex"].(string); ok {
		b, e := hex.DecodeString(s)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	if n, ok := recipe["repeat"].(json.Number); ok {
		i, e := n.Int64()
		if e != nil || i < 0 || i > resumelibrary.MaxResumeBytes+1 {
			t.Fatal("recipe bound")
		}
		return bytes.Repeat([]byte("x"), int(i))
	}
	t.Fatal("unreviewed recipe")
	return nil
}
func resumeInput(t *testing.T, step sessionStep) resumelibrary.Request {
	f := map[string]any{}
	for k, v := range step.Input {
		if k != "content" {
			f[k] = v
		}
	}
	r := resumelibrary.Request{Operation: step.Op, Fields: f}
	if recipe, ok := step.Input["content"]; ok {
		r.Content = resumeRecipe(t, recipe)
	}
	if step.Op == "approved" {
		if _, ok := f["source_sha256"]; !ok {
			f["source_sha256"] = digestBytes(r.Content)
		}
	}
	return r
}

type resumeFixtureReader struct{ *runtimepaths.Root }

func (r resumeFixtureReader) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	return r.ReadSingleLinkFile(path, limit)
}

func resumeByteState(b []byte) map[string]any {
	return map[string]any{"sha256": digestBytes(b), "size_bytes": json.Number(fmt.Sprint(len(b)))}
}
func resumeFileState(t *testing.T, data string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	root := filepath.Join(data, "applications", "resumes")
	if _, e := os.Stat(root); os.IsNotExist(e) {
		return out
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("unexpected fixture link")
		}
		rel, e := filepath.Rel(data, path)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), "resume_") {
				out[rel] = map[string]any{"kind": "directory"}
			}
			return nil
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		out[rel] = resumeByteState(raw)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
