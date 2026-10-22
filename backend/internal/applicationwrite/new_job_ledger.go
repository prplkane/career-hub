package applicationwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"sync/atomic"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobdigest"
	"careerhub.local/backend/internal/runtimepaths"
)

type newJobLedgerWitness struct {
	intent            newJobIntent
	sealed, raw, card []byte
}

type newJobLedgerReceipt struct {
	operationID, jobID string
	intentSHA256       [32]byte
	consumed           atomic.Bool
}

// readNewJobLedgerWitness is the shared, exact pending-file boundary for
// registration and the later advisory assessment. It never trusts a caller's
// public outcome fields or treats a recovery observation as a write receipt.
func readNewJobLedgerWitness(reads *sourceReads, operationID, jobID string, intentSHA256 [32]byte) (newJobIntent, cataloginput.Value, *Error) {
	sealed, present, readErr := reads.root.ReadSingleLinkFile(datahome.NewJobRecoveryPath, newJobIntentLimit)
	if readErr != nil || !present || sha256.Sum256(sealed) != intentSHA256 {
		return newJobIntent{}, cataloginput.Value{}, failed("intent", "JOB_INTAKE_INTENT_CHANGED")
	}
	intent, decodeErr := decodeNewJobIntent(sealed, reads.home)
	if decodeErr != nil || intent.OperationID != operationID || intent.JobID != jobID {
		return newJobIntent{}, cataloginput.Value{}, failed("intent", "JOB_INTAKE_INTENT_CHANGED")
	}
	raw, rawPresent, rawErr := reads.root.ReadSingleLinkFile(intent.RawPath, jobdigest.MaxSourceBytes)
	card, cardPresent, cardErr := reads.root.ReadSingleLinkFile(intent.CardPath, jobcard.MaxInputBytes)
	if rawErr != nil || cardErr != nil ||
		classifyNewJobFiles(intent, newJobObservedFile{rawPresent, raw}, newJobObservedFile{cardPresent, card}) != newJobFilesComplete {
		return newJobIntent{}, cataloginput.Value{}, failed("source", "JOB_INTAKE_SOURCE_CHANGED")
	}
	if e := validateFrozenNewJob(reads.ctx, intent, raw, card); e != nil {
		return newJobIntent{}, cataloginput.Value{}, e
	}
	parsed, parseErr := cataloginput.ParseYAML(card)
	if parseErr != nil {
		return newJobIntent{}, cataloginput.Value{}, failed("source", "JOB_INTAKE_CARD_INVALID")
	}
	reads.newJobWitness = &newJobLedgerWitness{intent: intent, sealed: bytes.Clone(sealed), raw: bytes.Clone(raw), card: bytes.Clone(card)}
	return intent, parsed, nil
}

func (w *newJobLedgerWitness) unchanged(root *runtimepaths.Root) *Error {
	if w == nil {
		return failed("witness", "JOB_INTAKE_LEDGER_WITNESS_REQUIRED")
	}
	return verifyNewJobPublicationState(root, w.intent, w.sealed, w.raw, w.card)
}

func completeNewJobFileReceipt(phase *NewJobFilePhaseOutcome) bool {
	return phase != nil && phase.Duplicate == nil && phase.Complete && !phase.Unconfirmed &&
		phase.RecoveryRequired && !phase.CleanupFailed && intakeID(phase.OperationID) && validNewJobID(phase.JobID) &&
		phase.receipt != nil && phase.receipt.operationID == phase.OperationID && phase.receipt.jobID == phase.JobID &&
		phase.receipt.intentSHA256 == phase.IntentSHA256 &&
		phase.IntentSHA256 != ([32]byte{}) && phase.IntentAttempted && phase.IntentReported && phase.IntentObserved &&
		phase.RawAttempted && phase.RawReported && phase.RawObserved &&
		phase.CardAttempted && phase.CardReported && phase.CardObserved
}

// RegisterNewJobAfterFiles is a host-only, unconnected second phase. It accepts
// either a completed same-call file receipt or a private recovery receipt
// minted after exact complete files plus an observed missing job. Public
// recovery observations and ambiguous native effects are never sufficient. It
// does not finalize the pending intent, compute assessment or set Applied. A
// failed or unknown SQLite commit keeps file-complete and database facts
// distinct; the caller must never retry blind.
func (s *Service) RegisterNewJobAfterFiles(ctx context.Context, phase *NewJobFilePhaseOutcome) (*Outcome, *Error) {
	receipt := newJobRegistrationReceipt(phase)
	if s == nil || s.expectedHome == nil || receipt == nil {
		return emptyFailure(failed("input", "JOB_INTAKE_LEDGER_REQUEST_INVALID"))
	}
	if ctx == nil || ctx.Err() != nil {
		return &Outcome{RecoveryRequired: true}, canceled()
	}
	if !receipt.consumed.CompareAndSwap(false, true) {
		return &Outcome{RecoveryRequired: true}, failed("input", "JOB_INTAKE_LEDGER_ALREADY_ATTEMPTED")
	}
	out, problem := s.runMode(ctx, func(reads *sourceReads) (mutation, *Error) {
		intent, parsed, witnessErr := readNewJobLedgerWitness(reads, receipt.operationID, receipt.jobID, receipt.intentSHA256)
		if witnessErr != nil {
			return nil, witnessErr
		}
		fingerprint, fingerprintErr := reads.fingerprint(intent.RawPath)
		if fingerprintErr != nil || fingerprint != intent.ContentFingerprint {
			return nil, failed("source", "JOB_INTAKE_SOURCE_CHANGED")
		}
		jobs, snapshotErr := snapshots([]map[string]any{{
			"id": intent.JobID, "company": newJobCardField(parsed, "company"),
			"title": newJobCardField(parsed, "title"), "source_url": newJobCardField(parsed, "source_url"),
			"content_fingerprint": intent.ContentFingerprint,
		}}, reads)
		if snapshotErr != nil || len(jobs) != 1 {
			return nil, failed("source", "JOB_INTAKE_CARD_INVALID")
		}
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			var existing string
			err := tx.QueryRowContext(ctx, "SELECT job_id FROM job_applications WHERE job_id=?", intent.JobID).Scan(&existing)
			if err == nil {
				return nil, failed("ledger", "JOB_INTAKE_LEDGER_ID_CONFLICT")
			}
			if !errors.Is(err, sql.ErrNoRows) {
				if ctx.Err() != nil {
					return nil, canceled()
				}
				return nil, failed("ledger", "JOB_INTAKE_LEDGER_UNAVAILABLE")
			}
			result, registerErr := applications.RegisterInTransaction(ctx, tx, jobs, s.now())
			if registerErr != nil {
				return nil, applicationProblem(registerErr, intent.JobID)
			}
			var records []json.RawMessage
			if result == nil || !result.Changed || json.Unmarshal(result.Result, &records) != nil || len(records) != 1 {
				return nil, failed("result", "JOB_INTAKE_LEDGER_RESULT_INVALID")
			}
			result.Result = records[0]
			return result, nil
		}, nil
	}, "new_job")
	if out != nil {
		// A successful database commit does not settle the file intent or
		// certify the later assessment/status phases.
		out.RecoveryRequired = true
		if problem == nil && out.Committed && !out.CleanupFailed {
			out.newJobLedgerReceipt = &newJobLedgerReceipt{operationID: receipt.operationID, jobID: receipt.jobID,
				intentSHA256: receipt.intentSHA256}
		}
	}
	return out, problem
}

func newJobRegistrationReceipt(phase *NewJobFilePhaseOutcome) *newJobPhaseReceipt {
	if completeNewJobFileReceipt(phase) {
		return phase.receipt
	}
	if phase == nil || phase.Duplicate != nil || phase.recoveryReceipt == nil ||
		phase.OperationID != phase.recoveryReceipt.operationID || phase.JobID != phase.recoveryReceipt.jobID ||
		phase.IntentSHA256 != phase.recoveryReceipt.intentSHA256 || phase.IntentSHA256 == ([32]byte{}) {
		return nil
	}
	return phase.recoveryReceipt
}
