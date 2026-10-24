package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications/contactcandidate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type candidatePreviewProcessRequest struct {
	Data    string
	Mode    string
	Request contactcandidate.SourceRequest
	Path    string
}

type candidatePreviewProcessOutput struct {
	Outcome *Outcome
	Error   *Error
}

func TestContactCandidatePreviewProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_CONTACT_CANDIDATE_PREVIEW_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var request candidatePreviewProcessRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 64<<10)).Decode(&request) != nil {
		t.Fatal("invalid request")
	}
	root, p := runtimepaths.Open(request.Data)
	if p != nil {
		t.Fatal(p)
	}
	if p = root.ClaimForProcess(); p != nil {
		t.Fatal(p)
	}
	defer root.Close()
	service := New(request.Data)
	service.now = func() string { return "2026-09-20T12:05:00Z" }
	var original []byte
	if strings.HasPrefix(request.Mode, "drift") {
		absolute := filepath.Join(request.Data, filepath.FromSlash(request.Path))
		original, _ = os.ReadFile(absolute)
		changed := false
		service.checkpoint = func(stage string) *Error {
			if stage == "before_commit" && !changed {
				changed = true
				if err := os.WriteFile(absolute, append(bytes.Clone(original), []byte("changed")...), 0600); err != nil {
					t.Fatal(err)
				}
			}
			return nil
		}
	}
	outcome, problem := service.PreviewContactCandidate(context.Background(), request.Request)
	if original != nil {
		if err := os.WriteFile(filepath.Join(request.Data, filepath.FromSlash(request.Path)), original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if json.NewEncoder(os.Stdout).Encode(candidatePreviewProcessOutput{outcome, problem}) != nil {
		t.Fatal("encode")
	}
}
