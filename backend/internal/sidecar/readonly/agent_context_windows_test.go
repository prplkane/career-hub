//go:build windows

package readonly

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/agent/agentcontext"
	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/intake/intakeextract"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/operationgate"
)

func agentContextFixture(t *testing.T, start protocol.StartRecord) {
	for path, text := range map[string]string{
		"jobs/normalized/synthetic.yaml": `id: synthetic
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
raw_jd_path: jobs/incoming/synthetic.txt
`,
		"jobs/incoming/synthetic.txt":         "Engineer. Python required.",
		"career_vault/profile.yaml":           "name: Synthetic\ntarget_location: US\nwork_authorization: synthetic\nmajor: Computer Science\ncontact: {email: PRIVATE@example.test}\n",
		"career_vault/education.yaml":         "schools: [{id: school, institution: Synthetic College, degree: BS}]\ncertifications: [{id: cert, name: Synthetic, credential: Test, completed: '2026', proof_points: []}]\n",
		"career_vault/visa_profile.yaml":      "current_status: synthetic\nwork_authorization: synthetic\nrequires_future_sponsorship: false\nstem_opt_eligible: false\n",
		"career_vault/experience_blocks.yaml": "- {id: exp, company: Synthetic, role: Engineer, dates: '2025', category: technical, technologies: [Python], proof_points: ['Built Python services.']}\n",
		"career_vault/project_blocks.yaml":    "- {id: project, name: Synthetic, category: technical, summary: Test, technologies: [SQL], proof_points: [], related_experience: [exp]}\n",
		"career_vault/skill_blocks.yaml":      "- {id: skill, skill_family: Technical, related_experience: [exp], related_projects: [project], related_certifications: [cert], keywords: []}\n",
		"career_vault/proof_blocks.yaml":      "- {id: proof, claim: Synthetic, evidence_type: experience, source_block: exp}\n",
		"career_vault/style_rules.yaml":       "standards_metadata: {reviewed_on: '2026-09-01', review_due: '2027-09-01', market: US, authority_order: [false]}\nresume_voice: {}\nbullet_rules: {}\ndocument_rules: {layout: single_column, standard_sections: [false]}\nats_rules: {}\nselection_rules: {allowed_decisions: [full, compact, excluded]}\nquality_gates: {}\nforbidden: []\n",
	} {
		writeFixture(t, start.DataRoot, path, text)
	}
	writeApplicationSQL(t, start.DataRoot,
		`PRAGMA user_version=28`,
		`CREATE TABLE job_applications(job_id TEXT PRIMARY KEY,company TEXT,title TEXT,source_url TEXT,content_fingerprint TEXT,decision TEXT,current_status TEXT,notes TEXT,created_at TEXT,updated_at TEXT,applied_resume_id TEXT,applied_resume_recorded INTEGER,revision INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE application_resume_attachments(job_id TEXT,resume_id TEXT,relative_path TEXT,file_name TEXT,label TEXT,sha256 TEXT,size_bytes INTEGER,attached_at TEXT)`,
		`CREATE TABLE application_resume_plans(job_id TEXT,plan_json TEXT,status TEXT,revision INTEGER,created_at TEXT,updated_at TEXT)`,
		`CREATE TABLE application_applied_resume_events(event_id INTEGER PRIMARY KEY,job_id TEXT,resume_id TEXT)`,
		`CREATE TABLE resume_library(resume_id TEXT PRIMARY KEY,display_name TEXT,relative_path TEXT,file_name TEXT,sha256 TEXT,size_bytes INTEGER,added_at TEXT,archived_at TEXT)`,
		`INSERT INTO job_applications VALUES('synthetic','Synthetic','Engineer','https://example.test/job','fp','apply','preparing','','2026-01-01','2026-01-01',NULL,0,1)`,
		`INSERT INTO resume_library VALUES('r1','Alpha','private/path','resume.txt','`+managementSHA+`',12,'2026-01-01',NULL)`)
	snapshot, ce := catalogstore.Load(start.ResourceRoot, start.DataRoot)
	if ce != nil {
		t.Fatal(ce)
	}
	report := managementReport(t, "migration_intake_extraction_cases.json")
	report["registry_revision"], report["extractor_version"] = snapshot.CatalogRevision(), intakeextract.ExtractorVersion
	writeManagementReport(t, start, "src_fictional_1234567890", "extracted.yaml", report)
}

func TestNativeAgentContextOwner(t *testing.T) {
	for _, mode := range []string{"assessment", "application_text", "resume_delta", "file_drift", "late_file_drift", "digest_drift", "catalog_drift", "cancel", "rollback_failure", "close_failure", "digest_close_failure", "schema", "intent", "channel", "writer_reservation", "source", "source_late_file_drift", "source_rollback_failure", "source_close_failure", "source_cancel", "source_channel", "source_schema", "prepared_assessment", "prepared_application_text", "prepared_resume_delta", "prepared_late_file_drift", "prepared_close_failure", "prepared_rollback_failure", "prepared_channel", "prepared_card_mismatch", "prepared_ledger_mismatch", "prepared_missing_binding"} {
		t.Run(mode, func(t *testing.T) {
			base, start := fixture(t)
			agentContextFixture(t, start)
			if strings.TrimPrefix(mode, "source_") == "schema" {
				writeApplicationSQL(t, start.DataRoot, "PRAGMA user_version=29")
			}
			before := tree(t, base)
			raw, _ := json.Marshal(struct {
				Start protocol.StartRecord
				Mode  string
			}{start, mode})
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAgentContextProcessHelper$")
			cmd.Env = append(os.Environ(), "CAREERHUB_AGENT_CONTEXT_HELPER=1")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.Stdin = bytes.NewReader(raw)
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("native %s: %s", mode, out)
			}
			if !strings.HasSuffix(mode, "_drift") && !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("read changed synthetic Home")
			}
		})
	}
}

func TestAgentContextProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_AGENT_CONTEXT_HELPER") != "1" {
		return
	}
	var req struct {
		Start protocol.StartRecord
		Mode  string
	}
	if json.NewDecoder(io.LimitReader(os.Stdin, 16<<10)).Decode(&req) != nil {
		t.Fatal("request")
	}
	sourceOnly := req.Mode == "source" || strings.HasPrefix(req.Mode, "source_")
	prepared := strings.HasPrefix(req.Mode, "prepared_")
	if prepared {
		req.Mode = strings.TrimPrefix(req.Mode, "prepared_")
	}
	if sourceOnly {
		req.Mode = strings.TrimPrefix(req.Mode, "source_")
		if req.Mode == "source" {
			req.Mode = "assessment"
		}
	}
	w, err := OpenForProcess(req.Start)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := agentcontext.SavedPacketRequest{JobID: "synthetic", Purpose: "resume_delta", Operation: "context", RegistryMode: "off", Profile: "sandbox", Channel: req.Start.Channel}
	if req.Mode == "assessment" || req.Mode == "application_text" {
		request.Purpose = req.Mode
	}
	if req.Mode == "intent" {
		request.Operation = "analyze"
	}
	if req.Mode == "channel" {
		request.Channel = "stable"
	}
	if prepared {
		db, err := sql.Open("sqlite", filepath.Join(req.Start.DataRoot, filepath.FromSlash(applicationDatabasePath)))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		summaries, problem := applications.SummariesInTransaction(ctx, tx, []string{request.JobID})
		if problem != nil {
			t.Fatal(problem)
		}
		raw, err := json.Marshal(summaries[request.JobID])
		if err != nil {
			t.Fatal(err)
		}
		if tx.Rollback() != nil || db.Close() != nil {
			t.Fatal("binding fixture cleanup")
		}
		applicationHash := sha256.Sum256(raw)
		card, err := os.ReadFile(filepath.Join(req.Start.DataRoot, "jobs", "normalized", "synthetic.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		cardHash := sha256.Sum256(card)
		request.Operation = "prepare"
		request.WriteReceipt = json.RawMessage(`{"result":"no_change_needed","changed":false,"job_created":false,"job_reused":true,"status_before":"preparing","status_after":"preparing","applied_recorded":false}`)
		request.Preparation = &agentcontext.PreparationBinding{CardSHA256: hex.EncodeToString(cardHash[:]), ApplicationSHA256: hex.EncodeToString(applicationHash[:])}
		switch req.Mode {
		case "card_mismatch":
			request.Preparation.CardSHA256 = strings.Repeat("a", 64)
		case "ledger_mismatch":
			request.Preparation.ApplicationSHA256 = strings.Repeat("a", 64)
		case "missing_binding":
			request.Preparation = nil
		}
	}
	faults := &agentContextFaults{checkpoint: func(stage string) {
		if stage == "after_read" {
			switch req.Mode {
			case "file_drift":
				writeFixture(t, req.Start.DataRoot, "career_vault/profile.yaml", "name: changed\n")
			case "digest_drift":
				writeFixture(t, req.Start.DataRoot, resumeRunsPath+"/src_fictional_1234567890/extracted.yaml", "invalid: [")
			case "catalog_drift":
				writeFixture(t, req.Start.DataRoot, catalogstore.CustomGroupsPath, "[]\n")
			case "cancel":
				cancel()
			}
		}
		if stage == "after_database_close" && req.Mode == "late_file_drift" {
			writeFixture(t, req.Start.DataRoot, "career_vault/profile.yaml", "name: late\n")
		}
		if stage == "before_read" && req.Mode == "writer_reservation" {
			db, e := sql.Open("sqlite", filepath.Join(req.Start.DataRoot, filepath.FromSlash(applicationDatabasePath)))
			if e != nil {
				t.Fatal(e)
			}
			db.SetMaxOpenConns(1)
			if _, e = db.Exec("PRAGMA busy_timeout=20"); e != nil {
				t.Fatal(e)
			}
			_, e = db.Exec("UPDATE resume_library SET display_name='illegal writer'")
			if db.Close() != nil || e == nil {
				t.Fatal("reader did not retain write reservation")
			}
		}
	}}
	if req.Mode == "rollback_failure" {
		faults.rollback = func(tx *sql.Tx) error {
			if tx.Rollback() != nil {
				t.Fatal("real rollback")
			}
			return errors.New("PRIVATE cleanup")
		}
	}
	if req.Mode == "close_failure" {
		faults.closeDB = func(db *sql.DB) error {
			if db.Close() != nil {
				t.Fatal("real close")
			}
			return errors.New("PRIVATE cleanup")
		}
	}
	if req.Mode == "digest_close_failure" {
		faults.closeRuns = func(o *agentDigestObservation) error {
			if !o.closed {
				t.Fatal("not closed")
			}
			return errors.New("PRIVATE cleanup")
		}
	}
	var packet json.RawMessage
	if prepared {
		packet, err = w.agentPreparedContext(ctx, request, faults)
	} else if sourceOnly {
		packet, err = w.agentAnalyzeSource(ctx, agentcontext.SourcePacketRequest{Source: agentcontext.SourceRequest{Text: "Software Engineer\nPython services"}, RegistryMode: "off", Profile: "sandbox", Channel: request.Channel}, faults)
	} else {
		packet, err = w.agentContext(ctx, request, faults)
	}
	if err != nil {
		var public *Error
		if errors.As(err, &public) {
			t.Log("public failure", public.Code)
		}
	}
	success := req.Mode == "assessment" || req.Mode == "application_text" || req.Mode == "resume_delta" || req.Mode == "writer_reservation"
	if success {
		identityOK := bytes.Contains(packet, []byte(`"job_id":"synthetic"`))
		if sourceOnly {
			identityOK = bytes.Contains(packet, []byte(`"persisted":false`)) && bytes.Contains(packet, []byte(`"application":null`))
		}
		if err != nil || !json.Valid(packet) || !identityOK || bytes.Contains(packet, []byte("PRIVATE@example.test")) || bytes.Contains(packet, []byte(managementSHA)) {
			t.Fatal("invalid packet", err)
		}
		if request.Purpose == "resume_delta" && !bytes.Contains(packet, []byte(`"digest_status":"in_progress"`)) {
			t.Fatal("missing native digest enrichment")
		}
		if prepared && !bytes.Contains(packet, []byte(`"operation":"prepare"`)) {
			t.Fatal("lost prepare intent")
		}
	} else {
		if err == nil || packet != nil {
			t.Fatal("failure exposed partial packet")
		}
		want := "AGENT_CONTEXT_SOURCE_CHANGED"
		switch req.Mode {
		case "card_mismatch", "ledger_mismatch":
			want = "AGENT_CONTEXT_PREPARATION_CHANGED"
		case "missing_binding":
			want = "AGENT_CONTEXT_PREPARATION_BINDING_REQUIRED"
		case "rollback_failure", "close_failure", "digest_close_failure":
			want = "AGENT_CONTEXT_CLEANUP_FAILED"
		case "schema":
			want = "AGENT_CONTEXT_SCHEMA_UNSUPPORTED"
		case "intent":
			want = "AGENT_CONTEXT_READ_INTENT_REQUIRED"
		case "channel":
			want = "AGENT_CONTEXT_CHANNEL_MISMATCH"
		case "cancel":
			want = "ASSESSMENT_INPUT_CANCELED"
		}
		requireCode(t, err, want)
	}
	// The owner must release the process-wide gate on every success/failure.
	gateCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	release, e := operationgate.Acquire(gateCtx)
	if e != nil {
		t.Fatal("gate leaked")
	}
	release()
}
