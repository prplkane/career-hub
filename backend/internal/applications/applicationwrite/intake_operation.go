package applicationwrite

import (
	"path"
	"time"

	"careerhub.local/backend/internal/intake/sourceupload"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// beginIntakeOperation borrows the admitted root under the caller's gate.
// Its finalizer records publication facts, never retries the product write.
func (s *Service) beginIntakeOperation(root *runtimepaths.Root, out *Outcome, operation string) (func(*Error) *Error, *Error) {
	prefix := "SOURCE_UPLOAD"
	if operation == "source_extract" {
		prefix = "SOURCE_EXTRACTION"
	}
	if operation == "review_save" {
		prefix = "REVIEW_UI_SAVE"
	}
	if operation == "approval_save" || operation == "approval_recover" {
		prefix = "APPROVAL"
	}
	if operation == "source_remove" {
		prefix = "APPROVED_IMPORT"
	}
	id, e := s.newIntakeID()
	stamp, timeErr := time.Parse(time.RFC3339Nano, s.now())
	if e != nil || !intakeID(id) || timeErr != nil {
		return nil, failed("identity", prefix+"_UNAVAILABLE")
	}
	runID := stamp.UTC().Format("20060102T150405.000000Z") + "_" + id[:8]
	diagnostic := sourceupload.Diagnostic{SchemaVersion: 2, Operation: operation, OperationID: id, RunID: runID, State: "started"}
	started, encodeErr := sourceupload.Encode(diagnostic)
	if encodeErr != nil {
		return nil, failed("diagnostic", prefix+"_DIAGNOSTIC_FAILED")
	}
	diagnosticPath := "career_vault/logs/native-intake/" + id + ".json"
	if _, pe := root.EnsureDirectory(path.Dir(diagnosticPath)); pe != nil {
		return nil, failed("diagnostic", prefix+"_DIAGNOSTIC_FAILED")
	}
	created, pe := s.intakeMutations.createFile(root, diagnosticPath, started)
	if pe != nil || !created || !intakeFileEquals(root, diagnosticPath, started) {
		return nil, failed("diagnostic", prefix+"_DIAGNOSTIC_FAILED")
	}
	out.RunID = runID
	return func(problem *Error) *Error {
		diagnostic.CommitAttempted, diagnostic.Committed = out.CommitAttempted, out.Committed
		diagnostic.State = "success"
		if problem != nil {
			diagnostic.State = "failed"
			diagnostic.ErrorCode = problem.Code
			if !sourceupload.ValidError(diagnostic.ErrorCode) {
				diagnostic.ErrorCode = prefix + "_UNAVAILABLE"
			}
			if out.CommitAttempted && !out.Committed {
				diagnostic.State = "unknown"
			}
		}
		terminal, e := sourceupload.Encode(diagnostic)
		if e == nil {
			changed, pe := s.intakeMutations.replaceFile(root, diagnosticPath, started, terminal)
			if pe == nil && changed && intakeFileEquals(root, diagnosticPath, terminal) {
				return problem
			}
		}
		return failed("diagnostic", prefix+"_DIAGNOSTIC_FAILED")
	}, nil
}
