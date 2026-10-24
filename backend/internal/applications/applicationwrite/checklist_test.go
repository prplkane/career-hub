package applicationwrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/applications/applicationchecklist"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestChecklistNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_CHECKLIST_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r sessionRequest
	d := json.NewDecoder(os.Stdin)
	d.UseNumber()
	if d.Decode(&r) != nil {
		t.Fatal("request")
	}
	root, e := runtimepaths.Open(r.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if r.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	nativeSetup(t, nativeRequest{Data: r.Data, Mode: r.Mode})
	out := []reviewOutput{}
	for _, step := range r.Steps {
		s := New(r.Data)
		s.now = func() string { return step.Now }
		ctx, cancel := context.WithCancel(context.Background())
		installFaults(t, s, nativeRequest{Data: r.Data, Mode: r.Mode}, ctx, cancel)
		prior := s.checkpoint
		s.checkpoint = func(stage string) *Error {
			if p := prior(stage); p != nil {
				return p
			}
			if r.Mode == "crash-"+stage {
				os.Exit(77)
			}
			if r.Mode == "late-cancel" && stage == "committed" {
				cancel()
			}
			for _, target := range []string{"raw", "card"} {
				if r.Mode != "change-"+target+"-"+stage {
					continue
				}
				path := "jobs/incoming/job-one.txt"
				if target == "card" {
					path = "jobs/normalized/job-one.yaml"
				}
				fixtureFile(t, filepath.Join(r.Data, filepath.FromSlash(path)), []byte("changed fixture source"))
			}
			return nil
		}
		result, p := s.Checklist(ctx, applicationchecklist.Request{Operation: step.Op, Fields: step.Input})
		cancel()
		tables := map[string]table{}
		if r.Mode != "missing-database" && !r.OmitTables {
			tables = sessionTables(t, r.Data)
		}
		out = append(out, reviewOutput{result, p, tables})
	}
	if json.NewEncoder(os.Stdout).Encode(out) != nil {
		t.Fatal("output")
	}
}
