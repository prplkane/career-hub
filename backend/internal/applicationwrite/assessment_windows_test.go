//go:build windows

package applicationwrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applicationreview"
	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/savedassessment"
)

type assessmentStep struct {
	Operation, Now string
	Fields         map[string]any
	Month          assessmentcontext.Month
}
type assessmentRequest struct {
	Data, Resources, Mode string
	Steps                 []assessmentStep
	OmitTables            bool
}

func TestAssessmentNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_ASSESSMENT_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r assessmentRequest
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
			targets := map[string]string{"card": "jobs/normalized/job-one.yaml", "raw": "jobs/incoming/job-one.txt",
				"vault": "career_vault/experience_blocks.yaml", "profile": "career_vault/profile.yaml", "policy": "career_vault/hard_blocker_policy.yaml",
				"custom": "career_vault/taxonomy/technologies.yaml", "groups": "career_vault/taxonomy/skill_groups.yaml",
				"core": "src/data/job_technologies.yaml", "coregroups": "src/data/skill_groups.yaml", "concepts": "src/data/requirement_concepts.yaml"}
			for key, path := range targets {
				if r.Mode != "change-"+key+"-"+stage {
					continue
				}
				base := r.Data
				if strings.HasPrefix(path, "src/") {
					base = r.Resources
				}
				fixtureFile(t, filepath.Join(base, filepath.FromSlash(path)), []byte("changed fixture source"))
			}
			return nil
		}
		if step.Operation == "reopen" {
			// Native SQLite recovery/read happens before any Python snapshot.
			r.OmitTables = true
			result, p := s.Review(ctx, applicationreview.Request{Operation: "get_assessment", Fields: map[string]any{"job_id": "job-one"}})
			cancel()
			out = append(out, reviewOutput{Outcome: result, Error: p})
			continue
		}
		result, p := s.AssessSaved(ctx, r.Resources, savedassessment.Request{Operation: step.Operation, Fields: step.Fields, Month: step.Month})
		cancel()
		tables := map[string]table{}
		if r.Mode != "missing-database" && !r.OmitTables {
			tables = sessionTables(t, r.Data)
		}
		out = append(out, reviewOutput{Outcome: result, Error: p, Tables: tables})
	}
	if json.NewEncoder(os.Stdout).Encode(out) != nil {
		t.Fatal("output")
	}
}
