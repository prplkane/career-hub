package applicationwrite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

const newJobTerminalRecoveryHomeEnv = "CAREERHUB_TEST_NEW_JOB_TERMINAL_RECOVERY_HOME"
const newJobTerminalRecoveryCaseEnv = "CAREERHUB_TEST_NEW_JOB_TERMINAL_RECOVERY_CASE"

func TestNewJobTerminalRecoveryReopen(t *testing.T) {
	home := os.Getenv(newJobTerminalRecoveryHomeEnv)
	if home == "" {
		t.Skip("fresh-process terminal helper only")
	}
	root, openErr := runtimepaths.Open(home)
	if openErr != nil {
		t.Fatal(openErr.Code)
	}
	if claimErr := root.ClaimForProcess(); claimErr != nil {
		t.Fatal(claimErr.Code)
	}
	defer root.Close()
	info, inspectErr := datahome.InspectOwnedForNewJobRecovery(root, nil)
	if inspectErr != nil {
		t.Fatal(inspectErr.Code)
	}
	owner := New(home).PinnedToHome(*info)
	scenario := os.Getenv(newJobTerminalRecoveryCaseEnv)
	if scenario == "terminal_receipt_sql_drift" {
		db, dbErr := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, "applications", "career_hub.sqlite3"))
		if dbErr != nil {
			t.Fatal(dbErr)
		}
		if _, err := db.Exec("UPDATE job_applications SET title='Foreign title' WHERE job_id='job_one'"); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	before, observeErr := owner.ObserveNewJobRecovery(context.Background())
	wantLedger := "assessed"
	if scenario == "terminal_receipt_sql_drift" {
		wantLedger = "conflict"
	}
	if observeErr != nil || before == nil || before.LedgerState != wantLedger || before.ReceiptState != "exact" {
		t.Fatal("terminal receipt did not survive restart", before, observeErr)
	}
	plan, planErr := owner.PlanIncompleteNewJobRecovery(context.Background())
	if planErr != nil || plan == nil {
		t.Fatal("terminal recovery plan unavailable", plan, planErr)
	}
	step, recoverErr := owner.RecoverIncompleteNewJobStep(context.Background(), plan.PlanID, "", assessmentcontext.Month{})
	if scenario == "terminal_receipt_sql_drift" {
		if recoverErr == nil || recoverErr.Code != "JOB_INTAKE_RECOVERY_STATE_CONFLICT" || step == nil ||
			step.Step != "" || step.Terminal != nil {
			t.Fatal("SQL drift authorized terminal cleanup", step, recoverErr)
		}
		if _, err := os.Stat(filepath.Join(home, datahome.NewJobRecoveryPath)); err != nil {
			t.Fatal("SQL drift lost pending journal", err)
		}
		return
	}
	recovered := step.Terminal
	if recoverErr != nil || recovered == nil || !recovered.Recovered || !recovered.ReceiptObserved ||
		step.Step != "terminal" || !recovered.RemovalReported || !recovered.RemovalObserved || recovered.RecoveryRequired {
		t.Fatal("exact terminal receipt did not settle pending journal", step, recoverErr)
	}
	if _, err := os.Stat(filepath.Join(home, datahome.NewJobRecoveryPath)); !os.IsNotExist(err) {
		t.Fatal("terminal recovery retained pending journal", err)
	}
	if _, err := datahome.Inspect(home, nil); err != nil {
		t.Fatal("ordinary Data Home admission remained blocked", err.Code)
	}
}
