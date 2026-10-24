package applicationwrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/intake/resumeplan"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type planNativeRequest struct {
	Data, Mode string
	Request    resumeplan.Request
}

func TestResumePlanProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_PLAN_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r planNativeRequest
	if json.NewDecoder(os.Stdin).Decode(&r) != nil {
		t.Fatal("request")
	}
	root, e := runtimepaths.Open(r.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if r.Mode != "unowned" {
		if e = root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	nativeSetup(t, nativeRequest{Data: r.Data, Mode: r.Mode})
	s := New(r.Data)
	s.now = func() string { return "2026-09-10T12:00:00Z" }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	installFaults(t, s, nativeRequest{Data: r.Data, Mode: r.Mode}, ctx, cancel)
	prior := s.checkpoint
	s.checkpoint = func(stage string) *Error {
		if e := prior(stage); e != nil {
			return e
		}
		if r.Mode == "crash-"+stage {
			os.Exit(77)
		}
		if stage == "applied" {
			if r.Mode == "change-vault" {
				fixtureFile(t, filepath.Join(r.Data, "career_vault/education.yaml"), []byte("changed"))
			}
			if r.Mode == "change-hints" {
				fixtureFile(t, filepath.Join(r.Data, "jobs/normalized/plan_job.yaml"), []byte("changed"))
			}
		}
		return nil
	}
	out, p := s.ResumePlan(ctx, r.Request)
	if json.NewEncoder(os.Stdout).Encode(nativeOutput{out, p, sessionTables(t, r.Data)}) != nil {
		t.Fatal("output")
	}
}
