package applicationwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sync/atomic"

	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// NewJobFileChoice is the result of one fresh owned inspection performed while
// BeginNewJobFilePhase holds the process operation gate. Exactly one branch is
// required; a duplicate is an observation, not a new application or write.
type NewJobFileChoice struct {
	Duplicate *NewJobFileDuplicate
	Prepared  *NewJobFileDraft
}

func (NewJobFileChoice) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted new job file choice]")
}

type NewJobFileDuplicate struct {
	JobID, Reason, CardPath, RawPath, InitialStatus string
	CardFileSHA256, RawFileSHA256                   [32]byte
}

// NewJobFileDraft carries only the already-prepared file phase. The host must
// bind VerifyReadSet to the same owned inspection, not to a new or user-supplied
// read set. This type is not an HTTP input and does not authorize Applied.
type NewJobFileDraft struct {
	Destination                   jobcard.IntakeDestination
	RawText                       string
	CardBytes                     []byte
	RawFileSHA256, CardFileSHA256 [32]byte
	ContentFingerprint            string
	InitialStatus                 string
	VerifyReadSet                 func(context.Context) error
}

func (NewJobFileDraft) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted new job file draft]")
}

// NewJobFilePhaseOutcome reports only the file phase. Even when Complete is
// true, the pending intent remains and no SQLite/assessment/status work has
// been performed. Attempted/reported/observed are intentionally distinct.
type NewJobFilePhaseOutcome struct {
	NamespaceMayHaveChanged bool
	Duplicate               *NewJobFileDuplicate
	OperationID, JobID      string
	IntentSHA256            [32]byte
	// Observed/planned source identity only, never authority to resume a phase.
	CardFileSHA256, RawFileSHA256                          [32]byte
	IntentAttempted, IntentReported, IntentObserved        bool
	RawAttempted, RawReported, RawObserved                 bool
	CardAttempted, CardReported, CardObserved              bool
	Complete, Unconfirmed, RecoveryRequired, CleanupFailed bool
	receipt                                                *newJobPhaseReceipt
	recoveryReceipt                                        *newJobPhaseReceipt
	duplicateReceipt                                       *newJobDuplicatePhaseReceipt
}

type newJobPhaseReceipt struct {
	operationID, jobID string
	intentSHA256       [32]byte
	consumed           atomic.Bool
}

type newJobDuplicatePhaseReceipt struct {
	choice   NewJobFileDuplicate
	consumed atomic.Bool
}

func (NewJobFilePhaseOutcome) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted new job file outcome]")
}

// BeginNewJobFilePhase is the host-only file phase of native intake.
// It acquires one gate before the fresh owned duplicate/namespace inspection
// and retains it through exact file publication and root cleanup. The caller
// must supply the actual owned inspector and bind its live read-set verifier.
// This is not a complete job intake; the pending intent blocks ordinary work
// until later ledger/assessment composition and reviewed recovery policy.
func (s *Service) BeginNewJobFilePhase(ctx context.Context, inspect func(context.Context) (*NewJobFileChoice, error)) (out *NewJobFilePhaseOutcome, problem *Error) {
	out = &NewJobFilePhaseOutcome{}
	if s == nil || ctx == nil || inspect == nil || s.root == "" || s.expectedHome == nil ||
		s.closeRoot == nil || s.newIntakeID == nil || s.checkpoint == nil {
		return out, failed("configuration", "JOB_INTAKE_REQUEST_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, intakeOperationTimeout)
	defer cancel()
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return out, canceled()
	}
	defer release()
	root, openErr := runtimepaths.Open(s.root)
	if openErr != nil {
		return out, failed("path", "JOB_INTAKE_ROOT_UNAVAILABLE")
	}
	defer func() {
		if s.closeRoot(root) != nil {
			out.CleanupFailed = true
			if problem == nil {
				problem = failed("cleanup", "JOB_INTAKE_ROOT_CLOSE_FAILED")
			}
		}
	}()
	home, readyErr := s.ready(root, nil)
	if readyErr != nil {
		return out, readyErr
	}
	choice, inspectErr := inspect(ctx)
	if ctx.Err() != nil {
		return out, canceled()
	}
	if inspectErr != nil {
		return out, failed("inspection", "JOB_INTAKE_INSPECTION_FAILED")
	}
	if choice == nil || (choice.Prepared == nil) == (choice.Duplicate == nil) {
		return out, failed("inspection", "JOB_INTAKE_CHOICE_INVALID")
	}
	if _, readyErr = s.ready(root, home); readyErr != nil {
		return out, readyErr
	}
	if choice.Duplicate != nil {
		if !jobcard.ValidID(choice.Duplicate.JobID) ||
			(choice.Duplicate.Reason != "retry_key" && choice.Duplicate.Reason != "posting" && choice.Duplicate.Reason != "content") ||
			(choice.Duplicate.CardPath != "jobs/normalized/"+choice.Duplicate.JobID+".yaml" &&
				choice.Duplicate.CardPath != "jobs/normalized/"+choice.Duplicate.JobID+".yml") ||
			(choice.Duplicate.RawPath != "jobs/incoming/"+choice.Duplicate.JobID+".md" &&
				choice.Duplicate.RawPath != "jobs/incoming/"+choice.Duplicate.JobID+".txt") ||
			choice.Duplicate.CardFileSHA256 == ([32]byte{}) || choice.Duplicate.RawFileSHA256 == ([32]byte{}) {
			return out, failed("inspection", "JOB_INTAKE_CHOICE_INVALID")
		}
		copy := *choice.Duplicate
		if copy.InitialStatus == "" {
			copy.InitialStatus = "captured"
		}
		if copy.InitialStatus != "captured" && copy.InitialStatus != "applied" {
			return out, failed("input", "JOB_INTAKE_INITIAL_STATUS_INVALID")
		}
		loaded, cardErr := jobcard.Read(ctx, root, copy.CardPath)
		if cardErr != nil || loaded == nil || loaded.Report == nil || !loaded.Report.SchemaValid() || !loaded.RawPresent ||
			loaded.CardFileSHA256 != copy.CardFileSHA256 || loaded.RawFileSHA256 != copy.RawFileSHA256 ||
			newJobCardField(loaded.Card, "id") != copy.JobID || newJobCardField(loaded.Card, "raw_jd_path") != copy.RawPath {
			return out, failed("inspection", "JOB_INTAKE_DUPLICATE_CHANGED")
		}
		out.Duplicate = &copy
		out.JobID = copy.JobID
		out.CardFileSHA256, out.RawFileSHA256 = copy.CardFileSHA256, copy.RawFileSHA256
		out.duplicateReceipt = &newJobDuplicatePhaseReceipt{choice: copy}
		return out, nil
	}
	draft := choice.Prepared
	initialStatus := draft.InitialStatus
	if initialStatus == "" {
		initialStatus = "captured"
	}
	if initialStatus != "captured" && initialStatus != "applied" {
		return out, failed("input", "JOB_INTAKE_INITIAL_STATUS_INVALID")
	}
	if draft.VerifyReadSet == nil || !validNewJobID(draft.Destination.ID) ||
		draft.Destination.CardPath != "jobs/normalized/"+draft.Destination.ID+".yaml" ||
		(draft.Destination.RawPath != "jobs/incoming/"+draft.Destination.ID+".md" &&
			draft.Destination.RawPath != "jobs/incoming/"+draft.Destination.ID+".txt") {
		return out, failed("inspection", "JOB_INTAKE_CHOICE_INVALID")
	}
	raw, card := []byte(draft.RawText), bytes.Clone(draft.CardBytes)
	if len(raw) == 0 || len(card) == 0 || sha256.Sum256(raw) != draft.RawFileSHA256 ||
		sha256.Sum256(card) != draft.CardFileSHA256 {
		return out, failed("inspection", "JOB_INTAKE_PLAN_CHANGED")
	}
	operationID, idErr := s.newIntakeID()
	if idErr != nil || !intakeID(operationID) {
		return out, failed("identity", "JOB_INTAKE_ENTROPY_UNAVAILABLE")
	}
	intent := newJobIntent{Version: 1, Home: *home, OperationID: operationID,
		InitialStatus: initialStatus,
		JobID:         draft.Destination.ID, RawPath: draft.Destination.RawPath, CardPath: draft.Destination.CardPath,
		RawSize: len(raw), RawSHA256: digestBytes(raw), CardSize: len(card), CardSHA256: digestBytes(card),
		ContentFingerprint: draft.ContentFingerprint}
	if !validNewJobIntent(intent) {
		return out, failed("inspection", "JOB_INTAKE_CHOICE_INVALID")
	}
	sealed, sealErr := encodeNewJobIntent(intent)
	if sealErr != nil {
		return out, sealErr
	}
	checks := newJobPublicationChecks{witnesses: func(ctx context.Context) *Error {
		if err := draft.VerifyReadSet(ctx); err != nil {
			if ctx.Err() != nil {
				return canceled()
			}
			return failed("witness", "JOB_INTAKE_INPUT_CHANGED")
		}
		return nil
	}, checkpoint: func(stage string) *Error { return s.checkpoint("new_job_" + stage) }}
	facts, publishErr := publishFrozenNewJob(ctx, root, intent, raw, card, checks)
	out.NamespaceMayHaveChanged = facts.NamespaceMayHaveChanged
	out.OperationID, out.JobID, out.IntentSHA256 = operationID, intent.JobID, sha256.Sum256(sealed)
	out.CardFileSHA256, out.RawFileSHA256 = draft.CardFileSHA256, draft.RawFileSHA256
	out.IntentAttempted, out.IntentReported, out.IntentObserved = facts.IntentAttempted, facts.IntentReported, facts.IntentObserved
	out.RawAttempted, out.RawReported, out.RawObserved = facts.RawAttempted, facts.RawReported, facts.RawObserved
	out.CardAttempted, out.CardReported, out.CardObserved = facts.CardAttempted, facts.CardReported, facts.CardObserved
	out.Unconfirmed, out.RecoveryRequired = facts.Unconfirmed, facts.recoveryRequired()
	out.Complete = publishErr == nil && facts.IntentReported && facts.RawReported && facts.CardReported &&
		facts.IntentObserved && facts.RawObserved && facts.CardObserved && !facts.Unconfirmed
	if out.Complete {
		out.receipt = &newJobPhaseReceipt{operationID: operationID, jobID: intent.JobID, intentSHA256: out.IntentSHA256}
	}
	return out, publishErr
}
