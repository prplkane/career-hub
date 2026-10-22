// Package promotiondiagnostics records the whole Discovery-to-job composition.
// These bounded snapshots are diagnostics, never recovery or retry authority.
package promotiondiagnostics

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"careerhub.local/backend/internal/emaildiagnostics"
)

var ErrInvalid = errors.New("DISCOVERY_PROMOTION_DIAGNOSTIC_INVALID")
var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type Facts struct {
	Committed        bool `json:"committed"`
	CommitUnknown    bool `json:"commit_unknown"`
	Partial          bool `json:"partial"`
	RecoveryRequired bool `json:"recovery_required"`
	CleanupFailed    bool `json:"cleanup_failed"`
}

type Record struct {
	SchemaVersion int    `json:"schema_version"`
	Operation     string `json:"operation"`
	OperationID   string `json:"operation_id"`
	RunID         string `json:"run_id"`
	ChildRunID    string `json:"child_run_id"`
	Action        string `json:"action"`
	Sequence      int    `json:"sequence"`
	Stage         string `json:"stage"`
	State         string `json:"state"`
	ErrorCode     string `json:"error_code"`
	Facts
}

func New(action string) (Record, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Record{}, ErrInvalid
	}
	text := hex.EncodeToString(id[:])
	r := Record{SchemaVersion: 3, Operation: "discovery_promotion", OperationID: text,
		RunID:  time.Now().UTC().Format("20060102T150405.000000Z") + "_" + text[:8],
		Action: action, Sequence: 1, Stage: "prepare", State: "running"}
	_, err := Encode(r)
	return r, err
}

// Only closed adapter codes may enter a record, not error messages or JD text.
func SafeError(code string) string {
	switch code {
	case "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED", "DISCOVERY_PROMOTION_FAILED",
		"DISCOVERY_UPDATE_INVALID", "DISCOVERY_REVISION_CONFLICT", "DISCOVERY_VERIFICATION_NOT_READY",
		"DISCOVERY_NOT_FOUND", "DISCOVERY_WRITE_FAILED", "LOCAL_OPERATION_CANCELED",
		"LOCAL_RESPONSE_INVALID", "WORKSPACE_UNAVAILABLE", "JOB_CARD_NOT_FOUND", "JOB_OPERATION_FAILED",
		"JOB_INTAKE_SOURCE_INVALID", "JOB_INTAKE_RETRY_CONFLICT", "JOB_INTAKE_DUPLICATE_AMBIGUOUS",
		"JOB_INTAKE_RECOVERY_REQUIRED", "JOB_INTAKE_DIAGNOSTIC_FAILED", "JOB_INTAKE_RESULT_INVALID",
		"JOB_INTAKE_REQUEST_INVALID", "JOB_INTAKE_STORAGE_UNAVAILABLE", "JOB_INTAKE_UNAVAILABLE":
		return code
	default:
		return "DISCOVERY_PROMOTION_FAILED"
	}
}

func Encode(r Record) ([]byte, error) {
	if r.SchemaVersion != 3 || r.Operation != "discovery_promotion" || !idPattern.MatchString(r.OperationID) ||
		!emaildiagnostics.ValidRunID(r.RunID) || r.RunID[len(r.RunID)-8:] != r.OperationID[:8] ||
		(r.ChildRunID != "" && (!emaildiagnostics.ValidRunID(r.ChildRunID) || r.ChildRunID == r.RunID)) ||
		!slices.Contains([]string{"save_job", "prepare_application", "record_as_applied"}, r.Action) ||
		!slices.Contains([]string{"prepare", "intake", "assessment", "link", "complete"}, r.Stage) || r.Sequence < 1 || r.Sequence > 4 {
		return nil, ErrInvalid
	}
	if r.Sequence == 1 && (r.Stage != "prepare" || r.State != "running" || r.ChildRunID != "" || r.Facts != (Facts{})) {
		return nil, ErrInvalid
	}
	switch r.State {
	case "running":
		if r.ErrorCode != "" || r.Stage == "complete" {
			return nil, ErrInvalid
		}
	case "success":
		if r.ErrorCode != "" || r.Stage != "complete" || r.Sequence < 2 || r.Partial || r.CommitUnknown || r.RecoveryRequired || r.CleanupFailed {
			return nil, ErrInvalid
		}
	case "failed":
		if r.Sequence < 2 || r.Stage == "complete" || r.ErrorCode == "" || SafeError(r.ErrorCode) != r.ErrorCode || (r.Committed || r.CommitUnknown || r.RecoveryRequired) && !r.Partial {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	return json.Marshal(r)
}

func Decode(raw []byte) (Record, error) {
	var r Record
	if len(raw) > 4096 || json.Unmarshal(raw, &r) != nil {
		return Record{}, ErrInvalid
	}
	canonical, err := Encode(r)
	if err != nil || !bytes.Equal(raw, canonical) {
		return Record{}, ErrInvalid
	}
	return r, nil
}

func ValidTransition(before, after Record) bool {
	if _, err := Encode(before); err != nil {
		return false
	}
	if _, err := Encode(after); err != nil {
		return false
	}
	if before.State != "running" || before.OperationID != after.OperationID || before.RunID != after.RunID ||
		before.Action != after.Action || after.Sequence != before.Sequence+1 ||
		(before.ChildRunID != "" && before.ChildRunID != after.ChildRunID) ||
		before.Committed && !after.Committed || before.CommitUnknown && !after.CommitUnknown ||
		before.Partial && !after.Partial || before.RecoveryRequired && !after.RecoveryRequired || before.CleanupFailed && !after.CleanupFailed {
		return false
	}
	if after.State == "failed" {
		return before.Stage == after.Stage
	}
	if after.State == "success" {
		return before.Stage == "link"
	}
	return before.Stage == "prepare" && (after.Stage == "intake" || after.Stage == "assessment") ||
		(before.Stage == "intake" || before.Stage == "assessment") && after.Stage == "link"
}
