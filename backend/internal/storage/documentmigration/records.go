package documentmigration

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

type RecoveryManifest struct {
	Plan                 Plan
	DocumentsRootPresent bool
}

type CompletionRecord struct {
	PlanID        string
	MigrationID   string
	DocumentCount int
}

func (manifest RecoveryManifest) CanonicalBytes() ([]byte, *Error) {
	if !validPlan(manifest.Plan) {
		return nil, fail(CodeRecoveryFailed)
	}
	plan := manifest.Plan
	result := []byte(`{"ciphertext_file_count":`)
	result = strconv.AppendInt(result, int64(plan.CiphertextFileCount), 10)
	result = append(result, `,"document_count":`...)
	result = strconv.AppendInt(result, int64(plan.DocumentCount), 10)
	result = append(result, `,"documents_root_present":`...)
	result = strconv.AppendBool(result, manifest.DocumentsRootPresent)
	result = append(result, `,"plan_id":`...)
	result = appendASCII(result, plan.PlanID)
	result = append(result, `,"source_format_version":1,"target_format_version":2,"total_ciphertext_bytes":`...)
	result = strconv.AppendInt(result, int64(plan.TotalCiphertextBytes), 10)
	result = append(result, `,"version":1}`...)
	return append(result, '\n'), nil
}

func ParseRecoveryManifest(content []byte) (RecoveryManifest, *Error) {
	if len(content) == 0 || len(content) > MaxJournalBytes {
		return RecoveryManifest{}, fail(CodeRecoveryFailed)
	}
	var decoded struct {
		CiphertextFileCount  int    `json:"ciphertext_file_count"`
		DocumentCount        int    `json:"document_count"`
		DocumentsRootPresent bool   `json:"documents_root_present"`
		PlanID               string `json:"plan_id"`
		SourceFormatVersion  int    `json:"source_format_version"`
		TargetFormatVersion  int    `json:"target_format_version"`
		TotalCiphertextBytes int    `json:"total_ciphertext_bytes"`
		Version              int    `json:"version"`
	}
	if err := json.Unmarshal(content, &decoded); err != nil || decoded.Version != JournalVersion {
		return RecoveryManifest{}, fail(CodeRecoveryFailed)
	}
	manifest := RecoveryManifest{
		Plan: Plan{
			PlanID: decoded.PlanID, SourceFormatVersion: decoded.SourceFormatVersion,
			TargetFormatVersion: decoded.TargetFormatVersion,
			DocumentCount:       decoded.DocumentCount, CiphertextFileCount: decoded.CiphertextFileCount,
			TotalCiphertextBytes: decoded.TotalCiphertextBytes,
		},
		DocumentsRootPresent: decoded.DocumentsRootPresent,
	}
	canonical, problem := manifest.CanonicalBytes()
	if problem != nil || !bytes.Equal(content, canonical) {
		return RecoveryManifest{}, fail(CodeRecoveryFailed)
	}
	return manifest, nil
}

func (completed CompletionRecord) CanonicalBytes() ([]byte, *Error) {
	if !validPlanID(completed.PlanID) || !lowerHexID(completed.MigrationID, "", 32) || completed.DocumentCount < 0 {
		return nil, fail(CodeRecoveryFailed)
	}
	result := []byte(`{"document_count":`)
	result = strconv.AppendInt(result, int64(completed.DocumentCount), 10)
	result = append(result, `,"migration_id":`...)
	result = appendASCII(result, completed.MigrationID)
	result = append(result, `,"plan_id":`...)
	result = appendASCII(result, completed.PlanID)
	result = append(result, `,"state":"completed","version":1}`...)
	return append(result, '\n'), nil
}

func ParseCompletionRecord(content []byte) (CompletionRecord, *Error) {
	if len(content) == 0 || len(content) > MaxJournalBytes {
		return CompletionRecord{}, fail(CodeRecoveryFailed)
	}
	var decoded struct {
		DocumentCount int    `json:"document_count"`
		MigrationID   string `json:"migration_id"`
		PlanID        string `json:"plan_id"`
		State         string `json:"state"`
		Version       int    `json:"version"`
	}
	if err := json.Unmarshal(content, &decoded); err != nil || decoded.Version != JournalVersion || decoded.State != "completed" {
		return CompletionRecord{}, fail(CodeRecoveryFailed)
	}
	completed := CompletionRecord{decoded.PlanID, decoded.MigrationID, decoded.DocumentCount}
	canonical, problem := completed.CanonicalBytes()
	if problem != nil || !bytes.Equal(content, canonical) {
		return CompletionRecord{}, fail(CodeRecoveryFailed)
	}
	return completed, nil
}

func validPlan(plan Plan) bool {
	if !validPlanID(plan.PlanID) || plan.SourceFormatVersion != SourceFormatVersion ||
		plan.TargetFormatVersion != TargetFormatVersion || plan.DocumentCount < 0 ||
		plan.CiphertextFileCount < 0 || plan.TotalCiphertextBytes < 0 {
		return false
	}
	return plan.DocumentCount == 0 && plan.CiphertextFileCount == 0 && plan.TotalCiphertextBytes == 0 ||
		plan.CiphertextFileCount == plan.DocumentCount+1 && plan.TotalCiphertextBytes >= plan.CiphertextFileCount
}

func validPlanID(value string) bool {
	return strings.HasPrefix(value, "sha256:") && lowerHexID(value, "sha256:", 64)
}
