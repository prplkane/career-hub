//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailcandidates"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

type emailOutcomeOwnedResult struct {
	Outcome        *Outcome `json:"outcome"`
	Problem        *Error   `json:"problem"`
	CurrentStatus  string   `json:"current_status"`
	ResumeRecorded int      `json:"resume_recorded"`
	StatusEvents   int      `json:"status_events"`
	ResumeEvents   int      `json:"resume_events"`
	OutcomeRows    int      `json:"outcome_rows"`
	StatusNote     string   `json:"status_note"`
}

func TestEmailOutcomeOwnedHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_EMAIL_OUTCOME_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	ctx := context.Background()
	home := os.Getenv("CAREERHUB_EMAIL_OUTCOME_HOME")
	mode := os.Getenv("CAREERHUB_EMAIL_OUTCOME_MODE")
	if _, err := datahome.BootstrapForProcess(ctx, home, "dev"); err != nil {
		t.Fatalf("synthetic bootstrap: %s", err.Code)
	}
	if _, err := schema.PrepareOwned(ctx, home); err != nil {
		t.Fatalf("synthetic schema: %v", err)
	}
	dbPath := filepath.Join(home, "applications", "career_hub.sqlite3")
	db, err := sqlitestore.OpenReadWriteExisting(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO job_applications
		(job_id,company,title,source_url,content_fingerprint,current_status,created_at,updated_at)
		VALUES('synthetic-handshake-99','Synthetic AI','Engineer',
		'https://app.joinhandshake.com/job-search/99',?,'captured','t0','t0')`,
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO application_status_events
		(job_id,status,occurred_at,recorded_at,note)
		VALUES('synthetic-handshake-99','captured','t0','t0','')`); err != nil {
		t.Fatal(err)
	}
	if mode == "constraint" {
		if _, err := db.Exec(`CREATE TRIGGER synthetic_email_abort BEFORE INSERT ON job_email_application_outcomes
			BEGIN SELECT RAISE(ABORT,'private test cause'); END`); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := New(home)
	s.now = func() string { return "2026-08-20T12:00:00Z" }
	if mode == "ambiguous-commit" {
		s.commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("private uncertain commit")
		}
	}
	input := emailcandidates.ApplicationOutcome{
		SourceProvider: "handshake", PostingIdentity: "handshake:99",
		SourceURL: "https://app.joinhandshake.com/job-search/99",
		Company:   "Synthetic AI", ApplicationStatus: "applied",
		EvidenceCode: "handshake_application_sent",
	}
	if mode == "unmatched" || strings.HasPrefix(mode, "link-") {
		input.PostingIdentity, input.SourceURL = "", ""
	}
	out, problem := s.RecordEmailApplicationOutcome(ctx, "sha256:"+
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		input, "2026-08-18T18:45:00-04:00")
	if strings.HasPrefix(mode, "link-") {
		if problem != nil || out == nil || !out.Committed {
			t.Fatalf("synthetic link setup: %+v %+v", out, problem)
		}
		var stored struct {
			ID       string `json:"outcome_id"`
			Revision int64  `json:"revision"`
		}
		if err := json.Unmarshal(out.Result, &stored); err != nil || stored.ID == "" || stored.Revision != 1 {
			t.Fatalf("synthetic link setup result: %+v %v", stored, err)
		}
		if mode == "link-constraint" {
			setup, err := sqlitestore.OpenReadWriteExisting(ctx, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			_, err = setup.Exec(`CREATE TRIGGER synthetic_link_abort BEFORE UPDATE ON job_email_application_outcomes
				BEGIN SELECT RAISE(ABORT,'private test cause'); END`)
			if err != nil {
				t.Fatal(err)
			}
			if err := setup.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "link-ambiguous-commit" {
			s.commit = func(tx *sql.Tx) error {
				if err := tx.Commit(); err != nil {
					return err
				}
				return errors.New("private uncertain link commit")
			}
		}
		if mode == "link-stale" {
			stored.Revision++
		}
		out, problem = s.LinkEmailApplicationOutcome(ctx, stored.ID, "synthetic-handshake-99", stored.Revision)
	}
	read, err := sqlitestore.OpenReadOnly(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	state := emailOutcomeOwnedResult{Outcome: out, Problem: problem}
	if err := read.QueryRow(`SELECT current_status,applied_resume_recorded FROM job_applications
		WHERE job_id='synthetic-handshake-99'`).Scan(&state.CurrentStatus, &state.ResumeRecorded); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		destination *int
		sql         string
	}{
		{&state.StatusEvents, `SELECT COUNT(*) FROM application_status_events`},
		{&state.ResumeEvents, `SELECT COUNT(*) FROM application_applied_resume_events`},
		{&state.OutcomeRows, `SELECT COUNT(*) FROM job_email_application_outcomes`},
	} {
		if err := read.QueryRow(query.sql).Scan(query.destination); err != nil {
			t.Fatal(err)
		}
	}
	_ = read.QueryRow(`SELECT note FROM application_status_events WHERE status='applied' ORDER BY event_id DESC LIMIT 1`).Scan(&state.StatusNote)
	if err := json.NewEncoder(os.Stdout).Encode(state); err != nil {
		t.Fatal(err)
	}
}

func ownedEmailOutcome(t *testing.T, mode string) emailOutcomeOwnedResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmailOutcomeOwnedHelper$")
	command.Env = append(os.Environ(), "CAREERHUB_EMAIL_OUTCOME_HELPER=1",
		"CAREERHUB_EMAIL_OUTCOME_HOME="+filepath.Join(t.TempDir(), "home"),
		"CAREERHUB_EMAIL_OUTCOME_MODE="+mode)
	command.WaitDelay = time.Second
	var output, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostic
	if err := command.Run(); err != nil {
		t.Fatalf("native fixture: %v stdout=%.900s stderr=%.900s", err, output.String(), diagnostic.String())
	}
	var result emailOutcomeOwnedResult
	if err := json.NewDecoder(bytes.NewReader(output.Bytes())).Decode(&result); err != nil {
		t.Fatalf("native framing: %v %.700s", err, output.String())
	}
	return result
}

func TestEmailOutcomeNativeOwner(t *testing.T) {
	for _, example := range []struct {
		mode, resolution, status, code            string
		committed, attempted                      bool
		resume, events, resumeEvents, outcomeRows int
	}{
		{"matched", "application_updated", "applied", "", true, true, 1, 2, 1, 1},
		{"unmatched", "unmatched", "captured", "", true, true, 0, 1, 0, 1},
		{"constraint", "", "captured", "EMAIL_OUTCOME_WRITE_FAILED", false, false, 0, 1, 0, 0},
		{"ambiguous-commit", "", "applied", "APPLICATION_LEDGER_COMMIT_UNCONFIRMED", false, true, 1, 2, 1, 1},
	} {
		t.Run(example.mode, func(t *testing.T) {
			got := ownedEmailOutcome(t, example.mode)
			if got.Outcome == nil || got.Outcome.Committed != example.committed ||
				got.Outcome.CommitAttempted != example.attempted || got.CurrentStatus != example.status ||
				got.ResumeRecorded != example.resume || got.StatusEvents != example.events ||
				got.ResumeEvents != example.resumeEvents || got.OutcomeRows != example.outcomeRows {
				t.Fatalf("owned outcome facts: %+v", got)
			}
			if example.code != "" {
				if got.Problem == nil || got.Problem.Code != example.code || hasResult(got.Outcome) {
					t.Fatalf("owned error: %+v", got)
				}
				return
			}
			if got.Problem != nil || !hasResult(got.Outcome) {
				t.Fatalf("owned result: %+v", got)
			}
			var result struct {
				Resolution         string `json:"resolution"`
				ApplicationChanged bool   `json:"application_changed"`
			}
			if err := json.Unmarshal(got.Outcome.Result, &result); err != nil || result.Resolution != example.resolution ||
				(result.ApplicationChanged != (example.mode == "matched")) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestEmailOutcomeNativeLinkOwner(t *testing.T) {
	for _, tc := range []struct {
		mode, code, status   string
		attempted, committed bool
		events, resume       int
	}{
		{"link-success", "", "applied", true, true, 2, 1},
		{"link-stale", "EMAIL_OUTCOME_LINK_REVISION_CONFLICT", "captured", false, false, 1, 0},
		{"link-constraint", "EMAIL_OUTCOME_WRITE_FAILED", "captured", false, false, 1, 0},
		{"link-ambiguous-commit", "APPLICATION_LEDGER_COMMIT_UNCONFIRMED", "applied", true, false, 2, 1},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			got := ownedEmailOutcome(t, tc.mode)
			if got.Outcome == nil || got.Outcome.CommitAttempted != tc.attempted ||
				got.Outcome.Committed != tc.committed || got.CurrentStatus != tc.status ||
				got.StatusEvents != tc.events || got.ResumeRecorded != tc.resume || got.OutcomeRows != 1 {
				t.Fatalf("native link facts: %+v", got)
			}
			if tc.code != "" {
				if got.Problem == nil || got.Problem.Code != tc.code || hasResult(got.Outcome) {
					t.Fatalf("native link error: %+v", got)
				}
				return
			}
			if got.Problem != nil || !hasResult(got.Outcome) ||
				got.StatusNote != "Handshake submission received by email and linked by the user." {
				t.Fatalf("native link result/note: %+v", got)
			}
			var result struct {
				Update struct {
					Resolution string `json:"resolution"`
					Revision   int64  `json:"revision"`
				} `json:"update"`
				Application struct {
					CurrentStatus string `json:"current_status"`
				} `json:"application"`
			}
			if err := json.Unmarshal(got.Outcome.Result, &result); err != nil ||
				result.Update.Resolution != "application_updated" || result.Update.Revision != 2 ||
				result.Application.CurrentStatus != "applied" {
				t.Fatalf("native link payload: %+v %v", result, err)
			}
		})
	}
}
