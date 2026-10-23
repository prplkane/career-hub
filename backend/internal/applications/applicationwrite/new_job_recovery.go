package applicationwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"

	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// NewJobRecoveryObservation reports bounded observed file state, not an
// attributed native call, completed ledger registration or permission to retry.
type NewJobRecoveryObservation struct {
	OperationID, JobID, FileState, InitialStatus string
	LedgerState, ReceiptState                    string
	SourceKeySHA256                              [32]byte
	intentSHA256                                 [32]byte
}

type ownedNewJobRecovery struct {
	observation       *NewJobRecoveryObservation
	intent            newJobIntent
	sealed, raw, card []byte
}

func (NewJobRecoveryObservation) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted new job recovery observation]")
}

// ObserveNewJobRecovery is host-only and read-only. The explicit recovery
// owner and read-only planner may use it while an intent blocks ordinary Data
// Home reads; no startup hook, HTTP route or automatic replay calls it.
func (s *Service) ObserveNewJobRecovery(ctx context.Context) (result *NewJobRecoveryObservation, problem *Error) {
	if s == nil || ctx == nil || s.expectedHome == nil || s.closeRoot == nil || s.root == "" {
		return nil, failed("configuration", "JOB_INTAKE_RECOVERY_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return nil, canceled()
	}
	defer release()
	root, pathErr := runtimepaths.Open(s.root)
	if pathErr != nil {
		return nil, failed("path", "JOB_INTAKE_RECOVERY_UNAVAILABLE")
	}
	defer func() {
		if s.closeRoot(root) != nil {
			result, problem = nil, failed("cleanup", "JOB_INTAKE_RECOVERY_CLOSE_FAILED")
		}
	}()
	owned, problem := s.observeNewJobRecoveryOwned(ctx, root)
	if problem != nil {
		return nil, problem
	}
	return owned.observation, nil
}

// The caller holds the operation gate and an owned Root. Returning exact
// witnesses as well as public phase facts lets the terminal owner recheck
// the same state without treating a read-only observation as a write receipt.
func (s *Service) observeNewJobRecoveryOwned(ctx context.Context, root *runtimepaths.Root) (*ownedNewJobRecovery, *Error) {
	home, readyErr := s.readyMode(root, nil, "new_job")
	if readyErr != nil {
		return nil, readyErr
	}
	sealed, present, readErr := root.ReadSingleLinkFile(datahome.NewJobRecoveryPath, newJobIntentLimit)
	if readErr != nil {
		return nil, failed("intent", "JOB_INTAKE_RECOVERY_INTENT_UNAVAILABLE")
	}
	if !present {
		return nil, failed("intent", "JOB_INTAKE_RECOVERY_INTENT_MISSING")
	}
	intent, decodeErr := decodeNewJobIntent(sealed, *home)
	if decodeErr != nil {
		return nil, decodeErr
	}
	raw, problem := readNewJobRecoveryFile(root, intent.RawPath, jobdigest.MaxSourceBytes)
	if problem != nil {
		return nil, problem
	}
	card, problem := readNewJobRecoveryFile(root, intent.CardPath, jobcard.MaxInputBytes)
	if problem != nil {
		return nil, problem
	}
	state := classifyNewJobFiles(intent, raw, card)
	if ctx.Err() != nil {
		return nil, canceled()
	}
	if _, readyErr := s.readyMode(root, home, "new_job"); readyErr != nil {
		return nil, readyErr
	}
	currentIntent, stillPresent, readErr := root.ReadSingleLinkFile(datahome.NewJobRecoveryPath, newJobIntentLimit)
	if readErr != nil || !stillPresent || !bytes.Equal(sealed, currentIntent) {
		return nil, failed("intent", "JOB_INTAKE_RECOVERY_CHANGED")
	}
	currentRaw, problem := readNewJobRecoveryFile(root, intent.RawPath, jobdigest.MaxSourceBytes)
	if problem != nil {
		return nil, problem
	}
	currentCard, problem := readNewJobRecoveryFile(root, intent.CardPath, jobcard.MaxInputBytes)
	if problem != nil {
		return nil, problem
	}
	if raw.Present != currentRaw.Present || card.Present != currentCard.Present ||
		!bytes.Equal(raw.Bytes, currentRaw.Bytes) || !bytes.Equal(card.Bytes, currentCard.Bytes) || ctx.Err() != nil {
		return nil, failed("source", "JOB_INTAKE_RECOVERY_CHANGED")
	}
	ledgerState, receiptState := "not_checked", "not_checked"
	if state == newJobFilesComplete {
		if validateFrozenNewJob(ctx, intent, raw.Bytes, card.Bytes) != nil {
			ledgerState = "conflict"
		} else if parsed, parseErr := cataloginput.ParseYAML(card.Bytes); parseErr != nil {
			ledgerState = "conflict"
		} else {
			ledgerState, problem = observeNewJobLedger(ctx, root, s.root, intent, parsed)
			if problem != nil {
				return nil, problem
			}
			receiptPath, expected, receiptErr := newJobTerminalReceiptFile(intent)
			if receiptErr != nil {
				return nil, receiptErr
			}
			receipt, present, readErr := root.ReadSingleLinkFile(receiptPath, newJobTerminalReceiptLimit)
			if readErr != nil {
				return nil, failed("receipt", "JOB_INTAKE_RECOVERY_RECEIPT_UNAVAILABLE")
			}
			receiptState = "missing"
			if present {
				receiptState = "conflict"
				if bytes.Equal(receipt, expected) {
					receiptState = "exact"
				}
			}
			if verifyNewJobPublicationState(root, intent, sealed, raw.Bytes, card.Bytes) != nil {
				return nil, failed("source", "JOB_INTAKE_RECOVERY_CHANGED")
			}
		}
	}
	return &ownedNewJobRecovery{observation: &NewJobRecoveryObservation{OperationID: intent.OperationID,
		JobID: intent.JobID, FileState: string(state), InitialStatus: intent.InitialStatus,
		LedgerState: ledgerState, ReceiptState: receiptState,
		SourceKeySHA256: sha256.Sum256([]byte(intent.ContentFingerprint)), intentSHA256: sha256.Sum256(sealed)},
		intent: intent, sealed: sealed, raw: raw.Bytes, card: card.Bytes}, nil
}

func readNewJobRecoveryFile(root *runtimepaths.Root, path string, limit int) (newJobObservedFile, *Error) {
	raw, present, err := root.ReadSingleLinkFile(path, int64(limit))
	if err != nil {
		return newJobObservedFile{}, failed("source", "JOB_INTAKE_RECOVERY_SOURCE_UNAVAILABLE")
	}
	return newJobObservedFile{Present: present, Bytes: raw}, nil
}
