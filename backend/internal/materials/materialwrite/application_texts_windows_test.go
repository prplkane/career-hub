//go:build windows

package materialwrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/applications/applicationtexts"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type textRequest struct {
	Root, Job, Mode string
	Draft           applicationtexts.Draft
}
type textResult struct {
	Outcome *Outcome
	Error   *Error
}

func TestApplicationTextProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_TEXT_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var request textRequest
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		t.Fatal("fixture request")
	}
	root, e := runtimepaths.Open(request.Root)
	if e != nil {
		t.Fatal(e.Code)
	}
	defer root.Close()
	if request.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e.Code)
		}
	}
	s := New(request.Root)
	s.checkpoint = func(stage string) *Error {
		if request.Mode == "fail:"+stage {
			return failed(stage, "TEST_FAILURE")
		}
		if request.Mode == "change-source" && stage == "after_read" {
			path := filepath.Join(request.Root, "jobs", "normalized", request.Job+".yaml")
			raw, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(path, append(raw, []byte("\n# independent fixture edit\n")...), 0600); e != nil {
				t.Fatal(e)
			}
		}
		return nil
	}
	if request.Mode == "late-create-error" {
		original := s.create
		s.create = func(root *runtimepaths.Root, path string, raw []byte) (bool, *runtimepaths.Error) {
			committed, e := original(root, path, raw)
			if e != nil {
				return committed, e
			}
			return committed, &runtimepaths.Error{Code: "FILE_CLEANUP"}
		}
	}
	if request.Mode == "close-failure" {
		s.closeRoot = func(root *runtimepaths.Root) error { _ = root.Close(); return os.ErrInvalid }
	}
	out, problem := s.SaveApplicationText(context.Background(), request.Job, request.Draft)
	if json.NewEncoder(os.Stdout).Encode(textResult{out, problem}) != nil {
		t.Fatal("fixture response")
	}
	os.Exit(0)
}
