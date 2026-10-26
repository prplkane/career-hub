//go:build windows

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/agent/agentcontext"
	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

type preparedNativeCase struct {
	Purpose, Registry string
	Packet            json.RawMessage
}

func TestNativeSavedAgentPreparation(t *testing.T) {
	if base := os.Getenv("CAREERHUB_PREPARED_AGENT_HOME"); base != "" {
		runNativeSavedAgentPreparation(t, base, os.Getenv("CAREERHUB_PREPARED_AGENT_CASE"))
		return
	}
	for _, mode := range []string{"success", "vault-failure", "card-drift", "ledger-drift"} {
		t.Run(mode, func(t *testing.T) {
			base := newJobHostFixture(t)
			installFreshJobAssessmentFixture(t, base)
			// The assessment-only fixture omits certification proof_points;
			// full agent model context requires the complete source catalog.
			materialFixtureFile(t, filepath.Join(base, "data", "career_vault", "education.yaml"), []byte("schools: []\ncertifications: [{id: cert, name: SQL, credential: Test, completed: '2026', proof_points: []}]\n"))
			materialFixtureFile(t, filepath.Join(base, "data", "jobs", "normalized", "synthetic_prepare.yaml"), []byte(`id: synthetic_prepare
status: ready
company: Synthetic
title: Engineer
source_url: https://example.test/job
location: ''
remote_policy: ''
seniority: ''
required_skills: [Python]
required_platforms: []
required_capabilities: []
preferred_skills: []
experience_requirements: []
education_requirements: []
responsibilities: [Build services.]
visa_signals: {citizenship_required: null, clearance_required: null, sponsorship_mentioned: null, no_visa_or_opt: null}
raw_jd_path: jobs/incoming/synthetic_prepare.txt
`))
			materialFixtureFile(t, filepath.Join(base, "data", "jobs", "incoming", "synthetic_prepare.txt"), []byte("Engineer. Python required."))
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeSavedAgentPreparation$")
			command.Env = append(os.Environ(), "CAREERHUB_PREPARED_AGENT_HOME="+base, "CAREERHUB_PREPARED_AGENT_CASE="+mode)
			command.WaitDelay = time.Second
			raw, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("native prepare %s: %v %.2400s", mode, err, raw)
			}
			if mode != "success" {
				return
			}
			var cases []preparedNativeCase
			if json.NewDecoder(bytes.NewReader(raw)).Decode(&cases) != nil || len(cases) != 6 {
				t.Fatal("native packet framing")
			}
		})
	}
}

func runNativeSavedAgentPreparation(t *testing.T, base, mode string) {
	data := filepath.Join(base, "data")
	start := protocol.StartRecord{DataRoot: data, ResourceRoot: filepath.Join(base, "resources"), Channel: "dev"}
	w, err := readonly.OpenForProcess(start)
	if err != nil {
		t.Fatal("admission", err)
	}
	if _, err = schema.PrepareOwned(context.Background(), data); err != nil {
		t.Fatal("schema", err)
	}
	home, err := w.StartupHome()
	if err != nil {
		t.Fatal(err)
	}
	req := httpapi.AgentContextRequest{JobID: "synthetic_prepare", Purpose: "assessment", RegistryMode: "off"}
	if _, err := w.AgentContext(context.Background(), agentcontext.SavedPacketRequest{JobID: req.JobID, Purpose: req.Purpose, RegistryMode: req.RegistryMode, Operation: "context", Profile: "explicit", Channel: "dev"}); err != nil {
		var public *readonly.Error
		if errors.As(err, &public) {
			t.Fatalf("fixture context: %s", public.Code)
		}
		t.Fatal("fixture context unavailable")
	}
	wrong := start
	wrong.Channel = "stable"
	if out, e := agentPrepareSavedAction(w, wrong)(context.Background(), req); e == nil || out.Committed || out.CommitUnknown {
		t.Fatal("cross-start write")
	}
	if mode != "success" {
		profile := filepath.Join(data, "career_vault", "profile.yaml")
		original, err := os.ReadFile(profile)
		if err != nil {
			t.Fatal(err)
		}
		out, e := prepareSavedAgent(context.Background(), req, start.Channel, applicationwrite.New(data).PinnedToHome(home).PrepareSavedJob,
			func(ctx context.Context, r agentcontext.SavedPacketRequest) (json.RawMessage, error) {
				switch mode {
				case "vault-failure":
					materialFixtureFile(t, profile, []byte("name: Invalid\n"))
				case "card-drift":
					p := filepath.Join(data, "jobs", "normalized", "synthetic_prepare.yaml")
					b, err := os.ReadFile(p)
					if err != nil {
						t.Fatal(err)
					}
					materialFixtureFile(t, p, append(b, []byte("# drift\n")...))
				case "ledger-drift":
					db, err := sql.Open("sqlite", filepath.Join(data, "applications", "career_hub.sqlite3"))
					if err != nil {
						t.Fatal(err)
					}
					_, err = db.Exec("UPDATE job_applications SET current_status='applied' WHERE job_id='synthetic_prepare'")
					if db.Close() != nil || err != nil {
						t.Fatal("fixture update", err)
					}
				}
				return w.AgentPreparedContext(ctx, r)
			})
		if e == nil || !out.Committed || !out.Partial || len(out.Result) != 0 {
			t.Fatal("lost stage facts", out, e)
		}
		if mode != "vault-failure" && e.Code != "AGENT_PREPARE_CONTEXT_CHANGED" {
			t.Fatal("drift not distinguished", e)
		}
		materialFixtureFile(t, profile, original)
		again, e := agentPrepareSavedAction(w, start)(context.Background(), req)
		if e != nil || again.Committed || again.CommitUnknown || len(again.Result) == 0 {
			t.Fatal("retry rewrote/lost state", again, e)
		}
		return
	}
	first, e := agentPrepareSavedAction(w, start)(context.Background(), req)
	if e != nil || !first.Committed || first.CleanupFailed || first.Partial || !bytes.Contains(first.Result, []byte(`"result":"prepared"`)) {
		t.Fatal("first write/read", first, e)
	}
	cases := []preparedNativeCase{}
	for _, purpose := range []string{"assessment", "resume_delta", "application_text"} {
		for _, registry := range []string{"off", "exact-auto"} {
			req.Purpose, req.RegistryMode = purpose, registry
			out, e := agentPrepareSavedAction(w, start)(context.Background(), req)
			if e != nil || out.Committed || out.CommitUnknown || out.Partial || !bytes.Contains(out.Result, []byte(`"result":"no_change_needed"`)) {
				t.Fatal("repeat packet", out, e)
			}
			cases = append(cases, preparedNativeCase{purpose, registry, out.Result})
		}
	}
	if json.NewEncoder(os.Stdout).Encode(cases) != nil {
		t.Fatal("output")
	}
}
