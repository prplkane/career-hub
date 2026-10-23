package applicationwrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applications/applicationcontacts"
	"careerhub.local/backend/internal/applications/applicationsessions"
	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/intake/resumelibrary"
	"careerhub.local/backend/internal/skills/catalogtransaction"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

const databasePath = "applications/career_hub.sqlite3"
const operationTimeout = 20 * time.Second

// Outcome preserves publication facts even if verification/cleanup later fails.
// CommitAttempted && !Committed is UNKNOWN, not proof of rollback. Never retry
// it blindly. A successful no-op has neither flag and still returns a result.
type Outcome struct {
	Result           json.RawMessage
	RunID            string
	ReviewValidation *intakeread.ReviewValidation
	CommitAttempted  bool
	Committed        bool
	CleanupFailed    bool
	CleanupCode      string
	RecoveryRequired bool
	ApprovalWrite    catalogtransaction.Report
	// Recovery observes an earlier SQL outcome; it never replays that SQL or
	// claims that the recovery call itself committed a database mutation.
	RecoveredDatabaseState string
	// Intake recovery publishes files, not a SQLite mutation. These separate
	// facts remain available even when later owner cleanup returns an error.
	IntakeState           string
	IntakeSourceID        string
	IntakeReportPublished bool
	// This in-memory capability is minted only after a confirmed fresh-job
	// Captured commit. Public outcome fields are never assessment authority.
	newJobLedgerReceipt *newJobLedgerReceipt
	// A second one-use capability exists only after a positively observed
	// assessment of a new JD whose sealed intake intent requests Applied.
	newJobAssessmentReceipt *newJobAssessmentReceipt
	// Only a confirmed terminal SQL phase can mint this private, one-use
	// capability to publish a terminal receipt and settle the pending journal.
	newJobTerminalCapability *newJobTerminalCapability
}

// Service borrows an already claimed home. It never bootstraps, migrates, starts
// legacy writers or exposes arbitrary SQL. Its function seams are private and
// fixed at construction; callers must not mutate a service while using it.
type Service struct {
	root            string
	resourceRoot    string
	expectedHome    *datahome.Info
	now             func() string
	checkpoint      func(string) *Error
	commit          func(*sql.Tx) error
	rollback        func(*sql.Tx) error
	closeDB         func(*sql.DB) error
	closeRoot       func(*runtimepaths.Root) error
	newDiscoveryID  func() (string, error)
	newIntakeID     func() (string, error)
	intakeMutations intakeFileMutations
	reviewMutations reviewFileMutations
	approvalBegin   func(context.Context, *runtimepaths.Root, string, []catalogtransaction.Change, []catalogtransaction.Guard, catalogtransaction.Validate) (catalogtransaction.Report, error)
	sessionClock    func() time.Time
	sessionEntropy  applicationsessions.Entropy
	contactEntropy  applicationcontacts.Entropy
	resumeEntropy   resumelibrary.Entropy
}

func New(dataRoot string) *Service {
	return &Service{root: dataRoot, now: func() string {
		return time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}, checkpoint: func(string) *Error { return nil },
		commit: (*sql.Tx).Commit, rollback: (*sql.Tx).Rollback,
		closeDB: (*sql.DB).Close, closeRoot: (*runtimepaths.Root).Close,
		newIntakeID:  secureIntakeID,
		sessionClock: time.Now, sessionEntropy: applicationsessions.SecureEntropy{},
		contactEntropy: applicationcontacts.SecureEntropy{}, resumeEntropy: resumelibrary.SecureEntropy{}}
}

// PinnedToHome returns an immutable service copy whose every operation must
// re-admit the same complete Data Home identity. New remains unpinned for
// existing callers; a long-running host can bind its state and import writes
// to the Home from its initial guarded read.
func (s *Service) PinnedToHome(expected datahome.Info) *Service {
	if s == nil {
		return nil
	}
	bound := *s
	bound.expectedHome = &expected
	return &bound
}

// PinnedToResources binds the explicit startup resource root for operations
// that must recheck a digest catalog before publishing into the Data Home.
// It does not infer a root from cwd or an environment variable.
func (s *Service) PinnedToResources(resourceRoot string) *Service {
	if s == nil {
		return nil
	}
	bound := *s
	bound.resourceRoot = resourceRoot
	return &bound
}
func failed(stage, code string) *Error         { return &Error{Stage: stage, Code: code} }
func canceled() *Error                         { return failed("context", "APPLICATION_LEDGER_CANCELED") }
func emptyFailure(e *Error) (*Outcome, *Error) { return &Outcome{}, e }

type mutation func(context.Context, *sql.Tx) (*applications.MutationResult, *Error)
type preparation func(*sourceReads) (mutation, *Error)

func (s *Service) ready(root *runtimepaths.Root, expected *datahome.Info) (*datahome.Info, *Error) {
	return s.readyMode(root, expected, "")
}
func (s *Service) readyMode(root *runtimepaths.Root, expected *datahome.Info, recovery string) (*datahome.Info, *Error) {
	if root.RequireProcessOwnership() != nil {
		return nil, failed("admission", "DATA_HOME_OWNERSHIP_REQUIRED")
	}
	var info *datahome.Info
	var e *datahome.Error
	switch recovery {
	case "discovery":
		info, e = datahome.InspectOwnedForDiscoveryRecovery(root, nil)
	case "resume":
		info, e = datahome.InspectOwnedForResumeRecovery(root, nil)
	case "intake":
		info, e = datahome.InspectOwnedForIntakeRecovery(root, nil)
	case "new_job":
		info, e = datahome.InspectOwnedForNewJobRecovery(root, nil)
	case "approval":
		info, e = datahome.InspectOwnedForApprovalRecovery(root, nil)
	case "":
		info, e = datahome.Inspect(s.root, nil)
	default:
		return nil, failed("configuration", "APPLICATION_LEDGER_UNAVAILABLE")
	}
	if e != nil {
		return nil, failed("admission", e.Code)
	}
	if s.expectedHome != nil && *info != *s.expectedHome {
		return nil, failed("admission", "APPLICATION_LEDGER_HOME_CHANGED")
	}
	if expected != nil && *info != *expected {
		return nil, failed("admission", "APPLICATION_LEDGER_HOME_CHANGED")
	}
	return info, nil
}
func schemaCurrent(ctx context.Context, tx *sql.Tx) *Error {
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		if ctx.Err() != nil {
			return canceled()
		}
		return failed("schema", "DATABASE_QUERY_FAILED")
	}
	if version < applications.SchemaVersion {
		return failed("schema", "SCHEMA_PREPARATION_REQUIRED")
	}
	if version > applications.SchemaVersion {
		return failed("schema", "SCHEMA_TOO_NEW")
	}
	return nil
}

// run retains all ownership and path pins until synchronous SQLite cleanup is
// finished. Deadlines bound cooperative work, not uninterruptible OS calls.
// There is no abandoned goroutine and no asynchronous database/sql rollback.
func (s *Service) run(ctx context.Context, id string, prepare preparation) (out *Outcome, problem *Error) {
	return s.runMode(ctx, prepare, "")
}

func (s *Service) runMode(ctx context.Context, prepare preparation, recovery string) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || prepare == nil || s.now == nil || s.checkpoint == nil ||
		s.commit == nil || s.rollback == nil || s.closeDB == nil || s.closeRoot == nil {
		return out, failed("configuration", "APPLICATION_LEDGER_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return out, canceled()
	}
	defer release()
	root, pe := runtimepaths.Open(s.root)
	if pe != nil {
		return out, failed("path", pe.Code)
	}
	var db *sql.DB
	var conn *sql.Conn
	var tx *sql.Tx
	var files *runtimepaths.SQLiteFiles
	var home *datahome.Info
	var reads *sourceReads
	finished := false
	cleanup := func(code string) {
		out.CleanupFailed = true
		if out.CleanupCode == "" {
			out.CleanupCode = code
		}
		if problem == nil {
			problem = failed("cleanup", code)
		}
	}
	defer func() {
		if reads != nil {
			p := reads.pendingPublication()
			if recovered := reads.pendingRecovery(); recovered != nil {
				p = recovered
				out.RecoveredDatabaseState = p.facts().state
			}
			if p != nil {
				facts := p.facts()
				out.RecoveryRequired = facts.recorded && !facts.removed
				if facts.cleanupCode != "" {
					cleanup(facts.cleanupCode)
				}
			}
		}
		if tx != nil && !finished {
			e := s.rollback(tx)
			if e != nil && !(out.CommitAttempted && errors.Is(e, sql.ErrTxDone)) {
				cleanup("APPLICATION_LEDGER_ROLLBACK_FAILED")
			}
		}
		if conn != nil {
			if e := conn.Close(); e != nil && !errors.Is(e, sql.ErrConnDone) {
				cleanup("APPLICATION_LEDGER_CONNECTION_CLOSE_FAILED")
			}
		}
		if db != nil && s.closeDB(db) != nil {
			cleanup("APPLICATION_LEDGER_DATABASE_CLOSE_FAILED")
		}
		if reads != nil && reads.newJobWitness != nil && problem == nil {
			problem = reads.newJobWitness.unchanged(root)
		}
		if home != nil && problem == nil {
			// Successful explicit recovery has settled its intent already. A new
			// sentinel must not be hidden by the initial recovery admission mode.
			if recovery == "new_job" {
				// New JD registration deliberately leaves its exact journal pending
				// for assessment/status composition; its own witness is checked at
				// precommit, rather than treating the journal as a foreign writer.
				_, problem = s.readyMode(root, home, "new_job")
			} else {
				_, problem = s.ready(root, home)
			}
		}
		if files != nil && files.Close() != nil {
			cleanup("APPLICATION_LEDGER_FILES_CLOSE_FAILED")
		}
		if reads != nil && reads.assessmentReads != nil && s.closeRoot(reads.assessmentReads.resources.root) != nil {
			cleanup("SAVED_ASSESSMENT_RESOURCES_CLOSE_FAILED")
		}
		if reads != nil && reads.intakeResources != nil && s.closeRoot(reads.intakeResources) != nil {
			cleanup("INTAKE_RESOURCES_CLOSE_FAILED")
		}
		if s.closeRoot(root) != nil {
			cleanup("APPLICATION_LEDGER_ROOT_CLOSE_FAILED")
		}
		if problem != nil {
			out.Result = nil
		}
	}()
	home, problem = s.readyMode(root, nil, recovery)
	if problem != nil {
		return out, problem
	}
	reads = &sourceReads{ctx: ctx, root: root, home: *home}
	apply, problem := prepare(reads)
	if problem != nil {
		return out, problem
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	files, pe = root.GuardSQLiteFiles(databasePath)
	if pe != nil {
		return out, failed("path", pe.Code)
	}
	if files == nil {
		return out, failed("schema", "SCHEMA_PREPARATION_REQUIRED")
	}
	reads.pins = files
	db, err = sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(s.root, filepath.FromSlash(databasePath)))
	if err != nil {
		if ctx.Err() != nil {
			return out, canceled()
		}
		return out, failed("open", "DATABASE_OPEN_FAILED")
	}
	conn, err = db.Conn(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return out, canceled()
		}
		return out, failed("open", "DATABASE_OPEN_FAILED")
	}
	tx, err = conn.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		if ctx.Err() != nil {
			return out, canceled()
		}
		return out, failed("transaction", "DATABASE_TRANSACTION_FAILED")
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	if problem = schemaCurrent(ctx, tx); problem != nil {
		return out, problem
	}
	if problem = s.checkpoint("prepared"); problem != nil {
		return out, problem
	}
	result, ae := apply(ctx, tx)
	if ae != nil {
		if ctx.Err() != nil {
			return out, canceled()
		}
		return out, ae
	}
	if problem = s.checkpoint("applied"); problem != nil {
		return out, problem
	}
	if result == nil || len(result.Result) == 0 || len(result.Result) > applications.MaxResultBytes || !json.Valid(result.Result) {
		return out, failed("result", "APPLICATION_LEDGER_RESPONSE_UNSUPPORTED")
	}
	if problem = s.checkpoint("before_commit"); problem != nil {
		return out, problem
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	if problem = reads.unchanged(); problem != nil {
		return out, problem
	}
	if recovery == "new_job" {
		_, problem = s.readyMode(root, home, "new_job")
	} else {
		_, problem = s.ready(root, home)
	}
	if problem != nil {
		return out, problem
	}
	if pe = files.Check(); pe != nil {
		return out, failed("path", pe.Code)
	}
	if problem = schemaCurrent(ctx, tx); problem != nil {
		return out, problem
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	if result.Changed {
		publication := reads.pendingPublication()
		if publication != nil {
			problem = publication.record(ctx, root, tx, *home)
			out.RecoveryRequired = publication.facts().recorded
			if problem != nil {
				return out, problem
			}
			if problem = s.checkpoint(publication.intentStage()); problem != nil {
				return out, problem
			}
			if problem = publication.checkReady(ctx, root, tx, files, *home); problem != nil {
				return out, problem
			}
		}
		out.CommitAttempted = true
		if s.commit(tx) != nil {
			return out, failed("commit", "APPLICATION_LEDGER_COMMIT_UNCONFIRMED")
		}
		out.Committed, finished = true, true
		if problem = s.checkpoint("committed"); problem != nil {
			return out, problem
		}
		if publication != nil {
			settle, cancelSettle := context.WithTimeout(context.WithoutCancel(ctx), operationTimeout)
			defer cancelSettle()
			problem = publication.finishCommitted(settle, root, conn, files, *home, s.checkpoint, s.rollback)
			out.RecoveryRequired = !publication.facts().removed
			if problem != nil {
				return out, problem
			}
		}
	} else {
		if reads.pendingPublication() != nil {
			return out, failed("result", "DISCOVERY_PUBLICATION_NOOP_INVALID")
		}
		err = s.rollback(tx)
		finished = true
		if err != nil {
			cleanup("APPLICATION_LEDGER_ROLLBACK_FAILED")
			return out, problem
		}
	}
	// Cancellation after a successful commit cannot undo it. Complete bounded
	// verification/cleanup and preserve facts; do not turn it into a retry hint.
	out.Result = result.Result
	return out, nil
}
