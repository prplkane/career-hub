package applicationwrite

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strconv"
)

const newJobRecoveryPlanVersion = 1

const (
	newJobRecoveryAdvance        = "advance"
	newJobRecoveryManualReview   = "manual_review"
	newJobRecoverySchemaRequired = "schema_required"
)

// NewJobRecoveryPlan is a content-minimized, read-only description of the
// exact pending intake state. PlanID binds the private sealed intent witness
// and every reported state; it is a revision token, not write authority.
type NewJobRecoveryPlan struct {
	Version       int    `json:"version"`
	PlanID        string `json:"plan_id"`
	OperationID   string `json:"operation_id"`
	JobID         string `json:"job_id"`
	InitialStatus string `json:"initial_status"`
	FileState     string `json:"file_state"`
	LedgerState   string `json:"ledger_state"`
	ReceiptState  string `json:"receipt_state"`
	Disposition   string `json:"disposition"`
	NextStep      string `json:"next_step"`
	ReasonCode    string `json:"reason_code"`
	CanAdvance    bool   `json:"can_advance"`
}

func (NewJobRecoveryPlan) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted new job recovery plan]")
}

// PlanIncompleteNewJobRecovery observes but never advances the pending intake.
// A later explicit writer must re-observe and require this exact PlanID before
// invoking one step; this read-only method does not grant that permission.
func (s *Service) PlanIncompleteNewJobRecovery(ctx context.Context) (*NewJobRecoveryPlan, *Error) {
	observed, problem := s.ObserveNewJobRecovery(ctx)
	if problem != nil {
		return nil, problem
	}
	return planIncompleteNewJobRecovery(observed)
}

func planIncompleteNewJobRecovery(observed *NewJobRecoveryObservation) (*NewJobRecoveryPlan, *Error) {
	if !validNewJobRecoveryPlanObservation(observed) {
		return nil, failed("observation", "JOB_INTAKE_RECOVERY_STATE_INVALID")
	}
	plan := &NewJobRecoveryPlan{
		Version:       newJobRecoveryPlanVersion,
		OperationID:   observed.OperationID,
		JobID:         observed.JobID,
		InitialStatus: observed.InitialStatus,
		FileState:     observed.FileState,
		LedgerState:   observed.LedgerState,
		ReceiptState:  observed.ReceiptState,
	}

	switch observed.FileState {
	case string(newJobFilesNone), string(newJobFilesRawOnly):
		plan.setManualReview("JOB_INTAKE_RECOVERY_FILES_INCOMPLETE")
	case string(newJobFilesConflict):
		plan.setManualReview("JOB_INTAKE_RECOVERY_STATE_CONFLICT")
	case string(newJobFilesComplete):
		classifyCompleteNewJobRecoveryPlan(plan, observed)
	}
	plan.PlanID = newJobRecoveryPlanID(observed, plan)
	return plan, nil
}

func classifyCompleteNewJobRecoveryPlan(plan *NewJobRecoveryPlan, observed *NewJobRecoveryObservation) {
	if observed.ReceiptState == "exact" {
		if newJobRecoveryTerminalState(observed) {
			plan.setAdvance("terminal")
			return
		}
		plan.setManualReview("JOB_INTAKE_RECOVERY_STATE_CONFLICT")
		return
	}
	if observed.ReceiptState != "missing" {
		plan.setManualReview("JOB_INTAKE_RECOVERY_STATE_CONFLICT")
		return
	}

	switch observed.LedgerState {
	case "database_missing":
		plan.Disposition = newJobRecoverySchemaRequired
		plan.ReasonCode = "SCHEMA_PREPARATION_REQUIRED"
	case "job_missing":
		plan.setAdvance("ledger")
	case "captured":
		plan.setAdvance("assessment")
	case "assessed":
		if observed.InitialStatus == "applied" {
			plan.setAdvance("applied")
		} else {
			plan.setAdvance("terminal")
		}
	case "applied":
		if observed.InitialStatus == "applied" {
			plan.setAdvance("terminal")
		} else {
			plan.setManualReview("JOB_INTAKE_RECOVERY_STATE_CONFLICT")
		}
	default:
		plan.setManualReview("JOB_INTAKE_RECOVERY_STATE_CONFLICT")
	}
}

func (p *NewJobRecoveryPlan) setAdvance(step string) {
	p.Disposition = newJobRecoveryAdvance
	p.NextStep = step
	p.ReasonCode = "JOB_INTAKE_RECOVERY_STEP_AVAILABLE"
	p.CanAdvance = true
}

func (p *NewJobRecoveryPlan) setManualReview(reason string) {
	p.Disposition = newJobRecoveryManualReview
	p.ReasonCode = reason
}

func validNewJobRecoveryPlanObservation(observed *NewJobRecoveryObservation) bool {
	if observed == nil || !intakeID(observed.OperationID) || !validNewJobID(observed.JobID) ||
		(observed.InitialStatus != "captured" && observed.InitialStatus != "applied") ||
		observed.SourceKeySHA256 == ([32]byte{}) || observed.intentSHA256 == ([32]byte{}) {
		return false
	}
	switch observed.FileState {
	case string(newJobFilesNone), string(newJobFilesRawOnly), string(newJobFilesConflict):
		return observed.LedgerState == "not_checked" && observed.ReceiptState == "not_checked"
	case string(newJobFilesComplete):
	default:
		return false
	}
	switch observed.LedgerState {
	case "conflict":
		return observed.ReceiptState == "not_checked" || observed.ReceiptState == "missing" || observed.ReceiptState == "exact"
	case "database_missing", "job_missing", "captured", "assessed", "applied":
		return observed.ReceiptState == "missing" || observed.ReceiptState == "exact"
	default:
		return false
	}
}

func newJobRecoveryPlanID(observed *NewJobRecoveryObservation, plan *NewJobRecoveryPlan) string {
	digest := sha256.New()
	for _, field := range []string{
		"career-hub/new-job-recovery-plan/v1",
		strconv.Itoa(plan.Version), plan.OperationID, plan.JobID, plan.InitialStatus,
		plan.FileState, plan.LedgerState, plan.ReceiptState, plan.Disposition,
		plan.NextStep, plan.ReasonCode, strconv.FormatBool(plan.CanAdvance),
	} {
		writeNewJobRecoveryPlanField(digest, []byte(field))
	}
	writeNewJobRecoveryPlanField(digest, observed.SourceKeySHA256[:])
	writeNewJobRecoveryPlanField(digest, observed.intentSHA256[:])
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func writeNewJobRecoveryPlanField(digest hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write(value)
}

func validNewJobRecoveryPlanID(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, r := range value[len("sha256:"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
