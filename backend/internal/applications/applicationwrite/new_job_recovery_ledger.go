package applicationwrite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"careerhub.local/backend/internal/applications/applicationreview"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

type newJobLedgerFacts struct {
	company, title, sourceURL, fingerprint, status string
	resumeRecorded                                 int64
	allEvents, capturedEvents, appliedEvents       int64
	resumeSnapshots, initialResumeSnapshots        int64
	assessment                                     *applicationreview.VersionedAssessment
}

// This is an observation, never recovery authority. Exact event/snapshot
// counts belong to one fresh Job ID; any other history is a conflict, not an
// invitation to replay a phase or delete its pending intent.
func classifyNewJobLedger(intent newJobIntent, card cataloginput.Value, f newJobLedgerFacts) string {
	if f.company != newJobCardField(card, "company") || f.title != newJobCardField(card, "title") ||
		f.sourceURL != newJobCardField(card, "source_url") || f.fingerprint != intent.ContentFingerprint ||
		f.capturedEvents != 1 || f.assessment != nil &&
		(f.assessment.JobID != intent.JobID || f.assessment.JobFingerprint != intent.CardSHA256 || f.assessment.Artifact == "") {
		return "conflict"
	}
	switch f.status {
	case "captured":
		if f.resumeRecorded != 0 || f.allEvents != 1 || f.appliedEvents != 0 || f.resumeSnapshots != 0 {
			return "conflict"
		}
		if f.assessment == nil {
			return "captured"
		}
		return "assessed"
	case "applied":
		if intent.InitialStatus != "applied" || f.assessment == nil || f.resumeRecorded != 1 ||
			f.allEvents != 2 || f.appliedEvents != 1 || f.resumeSnapshots != 1 || f.initialResumeSnapshots != 1 {
			return "conflict"
		}
		return "applied"
	default:
		return "conflict"
	}
}

// observeNewJobLedger opens only the fixed guarded SQLite file after exact
// pending raw/card validation. A missing database/row is a phase observation;
// an unreadable/newer/older schema is an error, never a guessed absence.
func observeNewJobLedger(ctx context.Context, root *runtimepaths.Root, dataRoot string, intent newJobIntent,
	card cataloginput.Value) (state string, problem *Error) {
	pins, pe := root.GuardSQLiteFiles(databasePath)
	if pe != nil {
		return "", failed("ledger", "JOB_INTAKE_RECOVERY_LEDGER_UNAVAILABLE")
	}
	if pins == nil {
		if exists, e := root.Exists(databasePath); e != nil || exists {
			return "", failed("ledger", "JOB_INTAKE_RECOVERY_LEDGER_UNAVAILABLE")
		}
		return "database_missing", nil
	}
	defer func() {
		if pins.Close() != nil && problem == nil {
			state, problem = "", failed("cleanup", "JOB_INTAKE_RECOVERY_LEDGER_CLOSE_FAILED")
		}
	}()
	db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(dataRoot, filepath.FromSlash(databasePath)))
	if err != nil {
		return "", failed("ledger", "JOB_INTAKE_RECOVERY_LEDGER_UNAVAILABLE")
	}
	defer func() {
		if db.Close() != nil && problem == nil {
			state, problem = "", failed("cleanup", "JOB_INTAKE_RECOVERY_LEDGER_CLOSE_FAILED")
		}
	}()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", failed("ledger", "JOB_INTAKE_RECOVERY_LEDGER_UNAVAILABLE")
	}
	defer func() {
		if tx.Rollback() != nil && problem == nil {
			state, problem = "", failed("cleanup", "JOB_INTAKE_RECOVERY_LEDGER_ROLLBACK_FAILED")
		}
	}()
	if e := schemaCurrent(ctx, tx); e != nil {
		return "", e
	}
	var f newJobLedgerFacts
	err = tx.QueryRowContext(ctx, `SELECT company,title,source_url,content_fingerprint,current_status,applied_resume_recorded
		FROM job_applications WHERE job_id=?`, intent.JobID).Scan(&f.company, &f.title, &f.sourceURL,
		&f.fingerprint, &f.status, &f.resumeRecorded)
	if errors.Is(err, sql.ErrNoRows) {
		return "job_missing", nil
	}
	if err != nil {
		return "", failed("ledger", "JOB_INTAKE_RECOVERY_LEDGER_UNAVAILABLE")
	}
	var readErr *applicationreview.Error
	f.assessment, readErr = applicationreview.ReadVersionedAssessment(ctx, tx, intent.JobID)
	// A malformed assessment row is not the same as an absent row.
	if readErr != nil {
		return "", failed("ledger", "JOB_INTAKE_RECOVERY_LEDGER_UNAVAILABLE")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN status='captured' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='applied' THEN 1 ELSE 0 END),0)
		FROM application_status_events WHERE job_id=?`, intent.JobID).Scan(&f.allEvents, &f.capturedEvents, &f.appliedEvents); err != nil {
		return "", failed("ledger", "JOB_INTAKE_RECOVERY_LEDGER_UNAVAILABLE")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN action='initial_snapshot' THEN 1 ELSE 0 END),0)
		FROM application_applied_resume_events WHERE job_id=?`, intent.JobID).Scan(&f.resumeSnapshots, &f.initialResumeSnapshots); err != nil {
		return "", failed("ledger", "JOB_INTAKE_RECOVERY_LEDGER_UNAVAILABLE")
	}
	if pe := pins.Check(); pe != nil || ctx.Err() != nil {
		return "", failed("ledger", "JOB_INTAKE_RECOVERY_LEDGER_UNAVAILABLE")
	}
	return classifyNewJobLedger(intent, card, f), nil
}
