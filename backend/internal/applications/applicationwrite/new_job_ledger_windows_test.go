//go:build windows

package applicationwrite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

const newJobLedgerHomeEnv = "CAREERHUB_TEST_NEW_JOB_LEDGER_HOME"
const newJobLedgerCaseEnv = "CAREERHUB_TEST_NEW_JOB_LEDGER_CASE"

func TestNewJobLedgerPendingIntentOwned(t *testing.T) {
	if home := os.Getenv(newJobLedgerHomeEnv); home != "" {
		runNewJobLedgerCase(t, home, os.Getenv(newJobLedgerCaseEnv))
		return
	}
	for _, scenario := range []string{"success", "precommit_drift", "existing_id", "commit_uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			intent, _, _ := frozenNewJobFixture(t)
			marker, err := datahome.MarkerBytes(intent.Home.Channel, intent.Home.DataHomeID)
			if err != nil {
				t.Fatal(err.Code)
			}
			if err := os.WriteFile(filepath.Join(home, datahome.MarkerPath), marker, 0600); err != nil {
				t.Fatal(err)
			}
			for _, directory := range []string{"jobs/incoming", "jobs/normalized"} {
				if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(directory)), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNewJobLedgerPendingIntentOwned$")
			child.Env = append(os.Environ(), newJobLedgerHomeEnv+"="+home, newJobLedgerCaseEnv+"="+scenario)
			child.WaitDelay = time.Second
			if output, err := child.CombinedOutput(); err != nil {
				t.Fatalf("fictional pending-intent ledger %s: %v %.1800s", scenario, err, output)
			}
		})
	}
}

func runNewJobLedgerCase(t *testing.T, home, scenario string) {
	t.Helper()
	intent, raw, card := frozenNewJobFixture(t)
	root, openErr := runtimepaths.Open(home)
	if openErr != nil {
		t.Fatal(openErr.Code)
	}
	if claimErr := root.ClaimForProcess(); claimErr != nil {
		t.Fatal(claimErr.Code)
	}
	defer root.Close()
	ctx := context.Background()
	prepared, prepErr := schema.PrepareOwned(ctx, home)
	if prepErr != nil || prepared == nil || !prepared.Committed {
		t.Fatal("fictional SQLite preparation failed", prepErr)
	}
	owner := New(home).PinnedToHome(intent.Home)
	owner.newIntakeID = func() (string, error) { return intent.OperationID, nil }
	if scenario == "existing_id" {
		prior, problem := owner.RegisterJob(ctx, map[string]any{"id": intent.JobID, "company": "Existing employer",
			"title": "Existing role", "source_url": "", "content_fingerprint": intent.ContentFingerprint})
		if problem != nil || prior == nil || !prior.Committed {
			t.Fatal("prior SQL identity fixture failed", problem)
		}
	}
	phase, phaseErr := owner.BeginNewJobFilePhase(ctx, func(context.Context) (*NewJobFileChoice, error) {
		return &NewJobFileChoice{Prepared: &NewJobFileDraft{
			Destination: jobcard.IntakeDestination{ID: intent.JobID, RawPath: intent.RawPath, CardPath: intent.CardPath},
			RawText:     string(raw), CardBytes: card, RawFileSHA256: sha256.Sum256(raw), CardFileSHA256: sha256.Sum256(card),
			ContentFingerprint: intent.ContentFingerprint, VerifyReadSet: func(context.Context) error { return nil },
		}}, nil
	})
	if phaseErr != nil || phase == nil || !phase.Complete || phase.IntentSHA256 == ([32]byte{}) {
		t.Fatal("fictional file phase failed", phase, phaseErr)
	}
	if scenario == "precommit_drift" {
		owner.checkpoint = func(stage string) *Error {
			if stage == "before_commit" {
				changed, err := root.ReplaceFile(intent.RawPath, raw, []byte("changed fictional JD"))
				if err != nil || !changed {
					t.Fatal("synthetic precommit drift failed", err)
				}
			}
			return nil
		}
	}
	if scenario == "commit_uncertain" {
		owner.commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("synthetic error after commit")
		}
	}
	result, problem := owner.RegisterNewJobAfterFiles(ctx, phase)
	if result == nil || !result.RecoveryRequired {
		t.Fatal("pending file intent was hidden", result, problem)
	}
	db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows, events int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_applications WHERE job_id=?", intent.JobID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM application_status_events WHERE job_id=?", intent.JobID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	switch scenario {
	case "success":
		if problem != nil || !result.Committed || !result.CommitAttempted || rows != 1 || events != 1 ||
			!strings.Contains(string(result.Result), intent.JobID) {
			t.Fatal("captured ledger registration was not committed exactly once", result, problem, rows, events)
		}
	case "precommit_drift":
		if problem == nil || problem.Code != "JOB_INTAKE_SOURCE_CHANGED" || result.Committed || result.CommitAttempted || rows != 0 || events != 0 {
			t.Fatal("changed source was registered", result, problem, rows, events)
		}
	case "existing_id":
		if problem == nil || problem.Code != "JOB_INTAKE_LEDGER_ID_CONFLICT" || result.Committed || result.CommitAttempted || rows != 1 || events != 1 {
			t.Fatal("existing SQL identity was overwritten", result, problem, rows, events)
		}
	case "commit_uncertain":
		if problem == nil || problem.Code != "APPLICATION_LEDGER_COMMIT_UNCONFIRMED" || !result.CommitAttempted ||
			result.Committed || rows != 1 || events != 1 {
			t.Fatal("unknown SQLite commit was reported as safe retry", result, problem, rows, events)
		}
	default:
		t.Fatal("unknown test case")
	}
	repeated, repeatErr := owner.RegisterNewJobAfterFiles(ctx, phase)
	if repeatErr == nil || repeatErr.Code != "JOB_INTAKE_LEDGER_ALREADY_ATTEMPTED" || repeated == nil ||
		!repeated.RecoveryRequired || repeated.CommitAttempted {
		t.Fatal("same file-phase receipt allowed a blind SQL retry", repeated, repeatErr)
	}
	if _, err := os.Stat(filepath.Join(home, datahome.NewJobRecoveryPath)); err != nil {
		t.Fatal("pending intent was removed", err)
	}
}
