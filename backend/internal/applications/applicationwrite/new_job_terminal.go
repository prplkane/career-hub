package applicationwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sync/atomic"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type newJobTerminalCapability struct {
	operationID, jobID, expectedState string
	intentSHA256                      [32]byte
	consumed                          atomic.Bool
}

// The terminal file and journal deletion are separate native operations.
// Observed bytes never turn an error return into an attributed success.
type NewJobTerminalOutcome struct {
	OperationID, JobID                           string
	Recovered                                    bool
	ReceiptAttempted, ReceiptReported            bool
	ReceiptObserved                              bool
	RemovalAttempted, RemovalReported            bool
	RemovalObserved                              bool
	Unconfirmed, RecoveryRequired, CleanupFailed bool
}

func (NewJobTerminalOutcome) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted new job terminal outcome]")
}

type newJobTerminalOps struct {
	create func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
	remove func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
}

// FinalizeNewJobAfterSuccess is an unconnected, host-only last phase. It
// accepts only a same-call successful assessment/Applied transaction or the
// explicit incomplete-recovery owner after it observes that exact terminal SQL
// state. The terminal receipt precedes exact journal removal; neither a public
// observation nor public outcome fields authorize this method.
func (s *Service) FinalizeNewJobAfterSuccess(ctx context.Context, phase *Outcome) (*NewJobTerminalOutcome, *Error) {
	return s.finalizeNewJobAfterSuccess(ctx, phase, newJobTerminalOps{})
}

func (s *Service) finalizeNewJobAfterSuccess(ctx context.Context, phase *Outcome, ops newJobTerminalOps) (out *NewJobTerminalOutcome, problem *Error) {
	out = &NewJobTerminalOutcome{RecoveryRequired: true}
	if s == nil || s.expectedHome == nil || s.closeRoot == nil || s.checkpoint == nil || phase == nil ||
		phase.newJobTerminalCapability == nil {
		return out, failed("input", "JOB_INTAKE_TERMINAL_REQUEST_INVALID")
	}
	if ctx == nil || ctx.Err() != nil {
		return out, canceled()
	}
	capability := phase.newJobTerminalCapability
	if !capability.consumed.CompareAndSwap(false, true) {
		return out, failed("input", "JOB_INTAKE_TERMINAL_ALREADY_ATTEMPTED")
	}
	out.OperationID, out.JobID = capability.operationID, capability.jobID
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return out, canceled()
	}
	defer release()
	root, openErr := runtimepaths.Open(s.root)
	if openErr != nil {
		return out, failed("path", "JOB_INTAKE_TERMINAL_ROOT_UNAVAILABLE")
	}
	defer func() {
		if s.closeRoot(root) != nil {
			out.CleanupFailed = true
			if problem == nil {
				problem = failed("cleanup", "JOB_INTAKE_TERMINAL_CLOSE_FAILED")
			}
		}
	}()
	owned, e := s.observeNewJobRecoveryOwned(ctx, root)
	if e != nil {
		return out, e
	}
	if !matchingTerminalPhase(owned, capability) {
		return out, failed("phase", "JOB_INTAKE_TERMINAL_PHASE_CHANGED")
	}
	receiptPath, receipt, e := newJobTerminalReceiptFile(owned.intent)
	if e != nil {
		return out, e
	}
	if _, pe := root.EnsureDirectory("jobs/intake_receipts"); pe != nil {
		return out, failed("receipt", "JOB_INTAKE_RECEIPT_DIRECTORY_UNAVAILABLE")
	}
	if e := newJobCreateAndObserve(root, receiptPath, receipt, "receipt", ops.create,
		&out.ReceiptAttempted, &out.ReceiptReported, &out.ReceiptObserved, &out.Unconfirmed); e != nil {
		return out, failed("receipt", "JOB_INTAKE_RECEIPT_UNCONFIRMED")
	}
	if e := s.checkpoint("new_job_terminal_receipt"); e != nil {
		return out, e
	}
	// This second read is intentionally after receipt publication. A changed
	// journal, card or SQL phase must not be unlocked by the earlier snapshot.
	current, e := s.observeNewJobRecoveryOwned(ctx, root)
	if e != nil || !matchingTerminalPhase(current, capability) ||
		!bytes.Equal(current.sealed, owned.sealed) || !bytes.Equal(current.raw, owned.raw) ||
		!bytes.Equal(current.card, owned.card) {
		return out, failed("phase", "JOB_INTAKE_TERMINAL_PHASE_CHANGED")
	}
	readback, present, pe := root.ReadSingleLinkFile(receiptPath, newJobTerminalReceiptLimit)
	if pe != nil || !present || !bytes.Equal(readback, receipt) {
		return out, failed("receipt", "JOB_INTAKE_RECEIPT_CHANGED")
	}
	if e := removeNewJobTerminalIntent(root, owned.sealed, ops.remove, out); e != nil {
		return out, e
	}
	return out, nil
}

// RecoverNewJobTerminal settles only a retained, exact terminal receipt and
// the still-matching completed SQL phase. It cannot create a missing receipt,
// replay SQL, finish an earlier incomplete phase, or treat an absent journal
// as a success. A future startup owner must invoke it explicitly.
func (s *Service) RecoverNewJobTerminal(ctx context.Context) (out *NewJobTerminalOutcome, problem *Error) {
	out = &NewJobTerminalOutcome{RecoveryRequired: true, Recovered: true}
	if s == nil || s.expectedHome == nil || s.closeRoot == nil || ctx == nil {
		return out, failed("configuration", "JOB_INTAKE_TERMINAL_RECOVERY_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return out, canceled()
	}
	defer release()
	root, openErr := runtimepaths.Open(s.root)
	if openErr != nil {
		return out, failed("path", "JOB_INTAKE_TERMINAL_ROOT_UNAVAILABLE")
	}
	defer func() {
		if s.closeRoot(root) != nil {
			out.CleanupFailed = true
			if problem == nil {
				problem = failed("cleanup", "JOB_INTAKE_TERMINAL_CLOSE_FAILED")
			}
		}
	}()
	owned, e := s.observeNewJobRecoveryOwned(ctx, root)
	if e != nil {
		return out, e
	}
	out.OperationID, out.JobID = owned.intent.OperationID, owned.intent.JobID
	if owned.observation.ReceiptState != "exact" || owned.observation.FileState != string(newJobFilesComplete) ||
		(owned.intent.InitialStatus == "captured" && owned.observation.LedgerState != "assessed") ||
		(owned.intent.InitialStatus == "applied" && owned.observation.LedgerState != "applied") {
		return out, failed("phase", "JOB_INTAKE_TERMINAL_RECOVERY_NOT_READY")
	}
	receiptPath, expected, receiptErr := newJobTerminalReceiptFile(owned.intent)
	if receiptErr != nil {
		return out, receiptErr
	}
	currentReceipt, present, readErr := root.ReadSingleLinkFile(receiptPath, newJobTerminalReceiptLimit)
	if readErr != nil || !present || !bytes.Equal(currentReceipt, expected) ||
		verifyNewJobPublicationState(root, owned.intent, owned.sealed, owned.raw, owned.card) != nil {
		return out, failed("receipt", "JOB_INTAKE_TERMINAL_RECEIPT_CHANGED")
	}
	out.ReceiptObserved = true
	if e := removeNewJobTerminalIntent(root, owned.sealed, nil, out); e != nil {
		return out, e
	}
	return out, nil
}

func removeNewJobTerminalIntent(root *runtimepaths.Root, sealed []byte,
	remove func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error), out *NewJobTerminalOutcome) *Error {
	if remove == nil {
		remove = (*runtimepaths.Root).RemoveFile
	}
	out.RemovalAttempted = true
	removed, removalErr := remove(root, datahome.NewJobRecoveryPath, sealed)
	out.RemovalReported = removed
	_, stillPresent, checkErr := root.ReadSingleLinkFile(datahome.NewJobRecoveryPath, newJobIntentLimit)
	out.RemovalObserved = checkErr == nil && !stillPresent
	if removalErr != nil || !removed || !out.RemovalObserved {
		out.Unconfirmed = true
		return failed("cleanup", "JOB_INTAKE_INTENT_REMOVAL_UNCONFIRMED")
	}
	out.RecoveryRequired = false
	return nil
}

func matchingTerminalPhase(owned *ownedNewJobRecovery, capability *newJobTerminalCapability) bool {
	if owned == nil || capability == nil || owned.observation == nil ||
		owned.observation.FileState != string(newJobFilesComplete) ||
		owned.observation.LedgerState != capability.expectedState ||
		owned.intent.OperationID != capability.operationID || owned.intent.JobID != capability.jobID ||
		sha256.Sum256(owned.sealed) != capability.intentSHA256 {
		return false
	}
	return (capability.expectedState == "assessed" && owned.intent.InitialStatus == "captured") ||
		(capability.expectedState == "applied" && owned.intent.InitialStatus == "applied")
}
