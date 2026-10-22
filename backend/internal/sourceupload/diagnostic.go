// Package sourceupload defines content-minimized intake diagnostics. A snapshot
// describes publication facts; it never authorizes retry. For multi-file approval,
// Committed means at least one target was saved, not that all targets completed.
package sourceupload

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"

	"careerhub.local/backend/internal/emaildiagnostics"
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var ErrDiagnostic = errors.New("SOURCE_UPLOAD_DIAGNOSTIC_INVALID")

type Diagnostic struct {
	SchemaVersion   int    `json:"schema_version"`
	Operation       string `json:"operation"`
	OperationID     string `json:"operation_id"`
	RunID           string `json:"run_id"`
	State           string `json:"state"`
	ErrorCode       string `json:"error_code"`
	CommitAttempted bool   `json:"commit_attempted"`
	Committed       bool   `json:"committed"`
}

func ValidError(code string) bool {
	switch code {
	case "APPROVED_IMPORT_SOURCE_INVALID", "APPROVED_IMPORT_APPROVAL_INVALID", "APPROVED_IMPORT_NOT_APPROVED", "APPROVED_IMPORT_SOURCE_MISSING", "APPROVED_IMPORT_SOURCE_TAMPERED", "APPROVED_IMPORT_SOURCE_REMOVE_FAILED", "APPROVED_IMPORT_CANCELED", "APPROVED_IMPORT_CLEANUP_FAILED", "APPROVED_IMPORT_UNAVAILABLE", "APPROVED_IMPORT_DIAGNOSTIC_FAILED":
		return true
	case "SOURCE_UPLOAD_FAILED", "SOURCE_ALREADY_EXISTS", "SOURCE_REIMPORT_NOT_FOUND",
		"SOURCE_UPLOAD_UNAVAILABLE", "SOURCE_UPLOAD_CANCELED", "SOURCE_UPLOAD_WRITE_UNCERTAIN",
		"SOURCE_UPLOAD_COMMITTED_UNVERIFIED", "SOURCE_UPLOAD_DIAGNOSTIC_FAILED":
		return true
	case "SOURCE_EXTRACTION_INVALID", "SOURCE_EXTRACTION_UNAVAILABLE", "SOURCE_EXTRACTION_CANCELED",
		"SOURCE_EXTRACTION_LIMIT", "SOURCE_EXTRACTION_CHANGED", "SOURCE_EXTRACTION_WRITE_UNCERTAIN",
		"SOURCE_EXTRACTION_COMMITTED_UNVERIFIED", "SOURCE_EXTRACTION_DIAGNOSTIC_FAILED", "OUTPUT_GUARD_FAILED":
		return true
	case "REVIEW_UI_SAVE_FAILED", "REVIEW_UI_SAVE_UNAVAILABLE", "REVIEW_UI_SAVE_CANCELED",
		"REVIEW_UI_SAVE_DIAGNOSTIC_FAILED", "REVIEW_REVISION_CONFLICT", "REVIEW_INTEGRITY_FAILED",
		"REVIEW_UI_SAVE_APPLIED_READ_ONLY", "REVIEW_UI_SAVE_WRITE_UNCERTAIN", "REVIEW_UI_SAVE_COMMITTED_UNVERIFIED":
		return true
	case "APPROVAL_UNAVAILABLE", "APPROVAL_CANCELED", "APPROVAL_INVALID", "APPROVAL_DIAGNOSTIC_FAILED",
		"APPROVAL_NOT_READY", "APPROVAL_ALREADY_APPLIED", "APPROVAL_SOURCE_CHANGED", "APPROVAL_TARGET_INVALID",
		"APPROVAL_RECOVERY_REQUIRED", "APPROVAL_WRITE_UNCERTAIN", "APPROVAL_COMMITTED_UNVERIFIED",
		"APPROVAL_JOURNAL_INVALID", "VAULT_REVISION_CONFLICT", "SKILL_REGISTRY_REVISION_CONFLICT",
		"INVALID_MERGE_TARGET", "INVALID_FIELD_RESOLUTION", "MISSING_APPROVAL_FIELD", "INVALID_COMPETENCY_REFERENCE":
		return true
	}
	return false
}

func Encode(d Diagnostic) ([]byte, error) {
	if d.SchemaVersion != 2 || (d.Operation != "source_upload" && d.Operation != "source_extract" && d.Operation != "review_save" && d.Operation != "approval_save" && d.Operation != "approval_recover" && d.Operation != "source_remove") || !idPattern.MatchString(d.OperationID) || !emaildiagnostics.ValidRunID(d.RunID) || d.Committed && !d.CommitAttempted {
		return nil, ErrDiagnostic
	}
	switch d.State {
	case "started":
		if d.ErrorCode != "" || d.CommitAttempted || d.Committed {
			return nil, ErrDiagnostic
		}
	case "success":
		if d.ErrorCode != "" || !d.Committed && !((d.Operation == "approval_recover" || d.Operation == "source_remove") && !d.CommitAttempted) {
			return nil, ErrDiagnostic
		}
	case "failed":
		if !ValidError(d.ErrorCode) || d.CommitAttempted && !d.Committed {
			return nil, ErrDiagnostic
		}
	case "unknown":
		if !ValidError(d.ErrorCode) || !d.CommitAttempted || d.Committed {
			return nil, ErrDiagnostic
		}
	default:
		return nil, ErrDiagnostic
	}
	return json.Marshal(d)
}

func Decode(raw []byte) (Diagnostic, error) {
	var d Diagnostic
	if len(raw) > 4096 || json.Unmarshal(raw, &d) != nil {
		return Diagnostic{}, ErrDiagnostic
	}
	canonical, e := Encode(d)
	if e != nil || !bytes.Equal(raw, canonical) {
		return Diagnostic{}, ErrDiagnostic
	}
	return d, nil
}
