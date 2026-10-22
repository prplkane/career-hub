//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/agentcontext"
	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func prepareSourceFixtureRequest() agentPrepareRequest {
	return agentPrepareRequest{AgentContextRequest: httpapi.AgentContextRequest{Purpose: "assessment", RegistryMode: "off", Source: &agentcontext.SourceRequest{Text: reanalysisCoordinatorSource, Company: "Fictional Labs", Title: "Backend Engineer", SourceURL: "https://example.test/jobs/prepare"}}, SourceName: "synthetic_prepare.txt"}
}

func TestNativeSourceAgentPreparation(t *testing.T) {
	if base := os.Getenv("CAREERHUB_SOURCE_AGENT_HOME"); base != "" {
		runNativeSourceAgentPreparation(t, base, os.Getenv("CAREERHUB_SOURCE_AGENT_CASE"))
		return
	}
	for _, mode := range []string{"success", "unregistered-duplicate", "posting-duplicate", "applied-duplicate", "invalid-purpose", "context-failure", "intake-failure"} {
		t.Run(mode, func(t *testing.T) {
			base := newJobHostFixture(t)
			installFreshJobAssessmentFixture(t, base)
			if mode != "context-failure" {
				materialFixtureFile(t, filepath.Join(base, "data", "career_vault", "education.yaml"), []byte("schools: []\ncertifications: [{id: cert, name: SQL, credential: Test, completed: '2026', proof_points: []}]\n"))
			}
			if mode == "intake-failure" {
				materialFixtureFile(t, filepath.Join(base, "data", "career_vault", "profile.yaml"), []byte("name: Invalid\n"))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeSourceAgentPreparation$")
			command.Env = append(os.Environ(), "CAREERHUB_SOURCE_AGENT_HOME="+base, "CAREERHUB_SOURCE_AGENT_CASE="+mode)
			command.WaitDelay = time.Second
			raw, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("source prepare %s: %v %.2400s", mode, err, raw)
			}
			if mode == "success" {
				var cases []preparedNativeCase
				if json.NewDecoder(bytes.NewReader(raw)).Decode(&cases) != nil || len(cases) != 6 {
					t.Fatal("source packet framing")
				}
			}
		})
	}
}

func runNativeSourceAgentPreparation(t *testing.T, base, mode string) {
	data := filepath.Join(base, "data")
	start := protocol.StartRecord{DataRoot: data, ResourceRoot: filepath.Join(base, "resources"), Channel: "dev"}
	w, err := readonly.OpenForProcess(start)
	if err != nil {
		t.Fatal("admit", err)
	}
	if _, err = schema.PrepareOwned(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	req := prepareSourceFixtureRequest()
	if mode == "invalid-purpose" {
		req.Purpose = "submit"
	}
	if mode == "unregistered-duplicate" {
		inspected, err := w.InspectNewJob(context.Background(), readonly.NewJobRequest{RawText: req.Source.Text, SourceName: req.SourceName, SourceURL: req.Source.SourceURL}, req.Source.Company, req.Source.Title)
		if err != nil || inspected == nil || inspected.Prepared == nil {
			t.Fatal("legacy fixture", err)
		}
		p := inspected.Prepared
		materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(p.Destination.CardPath)), p.CardBytes)
		materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(p.Destination.RawPath)), []byte(req.Source.Text))
	}
	action := agentPrepareAction(w, start)
	out, problem := action(context.Background(), req)
	if mode == "invalid-purpose" {
		if problem == nil || out.Committed || out.PreparationStages != nil {
			t.Fatal("invalid intent executed", out, problem)
		}
		entries, err := os.ReadDir(filepath.Join(data, "jobs", "normalized"))
		if err != nil || len(entries) != 0 {
			t.Fatal("invalid intent published")
		}
		return
	}
	if mode == "intake-failure" {
		if problem == nil || !out.Committed || !out.Partial || !out.RecoveryRequired || out.PreparationStages == nil || out.PreparationStages.IntakeComplete || out.PreparationStages.PreparationComplete || len(out.Result) != 0 {
			t.Fatal("lost intake failure facts", out, problem)
		}
		if _, err := os.Stat(filepath.Join(data, datahome.NewJobRecoveryPath)); err != nil {
			t.Fatal("lost recovery intent", err)
		}
		return
	}
	if mode == "context-failure" {
		if problem == nil || !out.Committed || !out.Partial || out.RecoveryRequired || out.PreparationStages == nil || !out.PreparationStages.IntakeComplete || !out.PreparationStages.PreparationComplete || out.PreparationStages.ContextComplete || len(out.Result) != 0 {
			t.Fatal("lost context failure facts", out, problem)
		}
		return
	}
	if problem != nil || !out.Committed || out.RecoveryRequired || out.CommitUnknown || out.PreparationStages == nil || !out.PreparationStages.IntakeComplete || !out.PreparationStages.PreparationComplete || !out.PreparationStages.ContextComplete {
		t.Fatal("source preparation", out, problem)
	}
	var packet struct {
		WriteReceipt struct {
			Result          string  `json:"result"`
			Changed         bool    `json:"changed"`
			JobCreated      bool    `json:"job_created"`
			JobReused       bool    `json:"job_reused"`
			StatusBefore    *string `json:"status_before"`
			StatusAfter     string  `json:"status_after"`
			AppliedRecorded bool    `json:"applied_recorded"`
		} `json:"write_receipt"`
	}
	if json.Unmarshal(out.Result, &packet) != nil || packet.WriteReceipt.Result != "prepared" || !packet.WriteReceipt.Changed || packet.WriteReceipt.StatusBefore != nil || packet.WriteReceipt.StatusAfter != "preparing" || packet.WriteReceipt.AppliedRecorded || packet.WriteReceipt.JobCreated != (mode != "unregistered-duplicate") || packet.WriteReceipt.JobReused != (mode == "unregistered-duplicate") {
		t.Fatal("false source receipt", string(out.Result))
	}
	before, err := os.ReadFile(filepath.Join(data, "jobs", "incoming", "synthetic_prepare.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if mode == "posting-duplicate" {
		req.Source.Text += "\nChanged listing for the same URL."
		req.SourceName = "different.txt"
	}
	expectedStatus := "preparing"
	if mode == "applied-duplicate" {
		written, err := applicationwrite.New(data).SetStatus(context.Background(), "synthetic_prepare", "applied", nil, "")
		if err != nil || !written.Committed {
			t.Fatal("fixture submission", err)
		}
		expectedStatus = "applied"
	}
	cases := []preparedNativeCase{}
	for _, purpose := range []string{"assessment", "resume_delta", "application_text"} {
		for _, registry := range []string{"off", "exact-auto"} {
			req.Purpose, req.RegistryMode = purpose, registry
			repeated, e := action(context.Background(), req)
			if e != nil || repeated.Partial || repeated.RecoveryRequired || json.Unmarshal(repeated.Result, &packet) != nil || packet.WriteReceipt.Result != "no_change_needed" || packet.WriteReceipt.JobCreated || !packet.WriteReceipt.JobReused || packet.WriteReceipt.StatusBefore == nil || *packet.WriteReceipt.StatusBefore != expectedStatus || packet.WriteReceipt.StatusAfter != expectedStatus || packet.WriteReceipt.AppliedRecorded {
				t.Fatal("duplicate preparation", repeated, e)
			}
			cases = append(cases, preparedNativeCase{purpose, registry, repeated.Result})
		}
	}
	after, err := os.ReadFile(filepath.Join(data, "jobs", "incoming", "synthetic_prepare.txt"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("duplicate replaced raw JD")
	}
	entries, err := os.ReadDir(filepath.Join(data, "jobs", "normalized"))
	if err != nil || len(entries) != 1 {
		t.Fatal("duplicate added cards")
	}
	if mode == "success" {
		if json.NewEncoder(os.Stdout).Encode(cases) != nil {
			t.Fatal("output")
		}
	}
}
