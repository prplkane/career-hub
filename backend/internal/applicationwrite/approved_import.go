package applicationwrite

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
)

type approvedImportSource struct {
	path, hash string
	content    []byte
	present    bool
}

// This closed reader pins approval and extraction independently from immutable
// Resume Version publication. Raw bytes use its existing 10 MiB resume reader.
// The shared owner rechecks both witnesses before the SQL/file commit boundary.
func approvedImportInput(reads *sourceReads, id string, allowMissing bool) (approvedImportSource, *Error) {
	var source approvedImportSource
	if !intakeReviewSourceID(id) {
		return source, failed("input", "APPROVED_IMPORT_SOURCE_INVALID")
	}
	extractionPath, _, approvalPath := intakeReviewPaths(id)
	witness := &sourceReads{ctx: reads.ctx, root: reads.root}
	reads.approvedImport = witness
	raw, present, pe := witness.ReadFile(approvalPath, intakeread.MaxApprovalBytes)
	if pe != nil {
		return source, failed("approval", "APPROVED_IMPORT_APPROVAL_INVALID")
	}
	if !present {
		return source, failed("approval", "APPROVED_IMPORT_NOT_APPROVED")
	}
	approval, parseErr := intakeread.ParseApproval(raw)
	if parseErr != nil || approval.SourceID != id || approval.Status != "applied" {
		return source, failed("approval", "APPROVED_IMPORT_APPROVAL_INVALID")
	}
	raw, present, pe = witness.ReadFile(extractionPath, intakeread.MaxExtractionBytes)
	if pe != nil || !present {
		return source, failed("source", "APPROVED_IMPORT_SOURCE_INVALID")
	}
	extraction, extractionErr := intakeread.ParseExtraction(raw)
	if extractionErr != nil || extraction.Source.ID != id {
		return source, failed("source", "APPROVED_IMPORT_SOURCE_INVALID")
	}
	source.path, source.hash = extraction.Source.Path, extraction.Source.SHA256
	name := path.Base(source.path)
	suffix := strings.ToLower(path.Ext(name))
	if path.Dir(source.path) != "career_vault/raw" || source.path != "career_vault/raw/"+name || !intakeFileName(name) ||
		(suffix != ".txt" && suffix != ".docx") || strings.TrimPrefix(suffix, ".") != extraction.Source.FileType {
		return source, failed("source", "APPROVED_IMPORT_SOURCE_INVALID")
	}
	source.content, source.present, pe = reads.resumeReads.ReadFile(source.path, resumelibrary.MaxResumeBytes)
	if pe != nil || !source.present && !allowMissing {
		return source, failed("source", "APPROVED_IMPORT_SOURCE_MISSING")
	}
	if source.present && digestBytes(source.content) != source.hash {
		return source, failed("source", "APPROVED_IMPORT_SOURCE_TAMPERED")
	}
	return source, nil
}

// KeepApprovedImport resolves the exact source under the same owner/gate as the
// existing approved Resume operation, retaining its idempotent source mapping.
func (s *Service) KeepApprovedImport(ctx context.Context, id string) (*Outcome, *Error) {
	if s == nil || s.expectedHome == nil || !intakeReviewSourceID(id) {
		return &Outcome{}, failed("input", "APPROVED_IMPORT_SOURCE_INVALID")
	}
	result, problem := s.resumePrepared(ctx, func(reads *sourceReads) (resumelibrary.Request, *Error) {
		source, problem := approvedImportInput(reads, id, false)
		if problem != nil {
			return resumelibrary.Request{}, problem
		}
		return resumelibrary.Request{Operation: "approved", Fields: map[string]any{"file_name": path.Base(source.path), "source_id": id, "source_sha256": source.hash}, Content: source.content}, nil
	})
	if result == nil {
		return nil, problem
	}
	return result.Outcome, problem
}

// RemoveApprovedImport removes only the exact verified original. Missing raw
// bytes are an idempotent no-op; review, approval, Vault and saved resumes stay.
func (s *Service) RemoveApprovedImport(ctx context.Context, id string) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || s.expectedHome == nil || s.closeRoot == nil || s.now == nil || s.newIntakeID == nil || s.checkpoint == nil || !intakeReviewSourceID(id) {
		return out, failed("input", "APPROVED_IMPORT_SOURCE_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, intakeOperationTimeout)
	defer cancel()
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return out, failed("context", "APPROVED_IMPORT_CANCELED")
	}
	defer release()
	root, pe := runtimepaths.Open(s.root)
	if pe != nil {
		return out, failed("path", "APPROVED_IMPORT_SOURCE_REMOVE_FAILED")
	}
	defer func() {
		if s.closeRoot(root) != nil {
			out.CleanupFailed, out.CleanupCode = true, "APPROVED_IMPORT_CLEANUP_FAILED"
			if problem == nil {
				problem = failed("cleanup", out.CleanupCode)
			}
		}
		if problem != nil {
			out.Result = nil
		}
	}()
	home, e := s.ready(root, nil)
	if e != nil {
		return out, failed("admission", "APPROVED_IMPORT_SOURCE_REMOVE_FAILED")
	}
	finish, e := s.beginIntakeOperation(root, out, "source_remove")
	if e != nil {
		return out, e
	}
	defer func() { problem = finish(problem) }()
	reads := &sourceReads{ctx: ctx, root: root}
	reads.resumeReads = &resumeReads{owner: reads}
	source, e := approvedImportInput(reads, id, true)
	if e != nil {
		return out, e
	}
	if e := s.checkpoint("approved_source_removal_prepared"); e != nil {
		return out, failed("source", "APPROVED_IMPORT_SOURCE_TAMPERED")
	}
	if _, e := s.ready(root, home); e != nil {
		return out, failed("admission", "APPROVED_IMPORT_SOURCE_REMOVE_FAILED")
	}
	if e := reads.unchanged(); e != nil {
		return out, failed("source", "APPROVED_IMPORT_SOURCE_TAMPERED")
	}
	if source.present {
		out.CommitAttempted = true
		out.Committed, pe = s.intakeMutations.removeFile(root, source.path, source.content)
		if pe != nil || !out.Committed {
			if !out.Committed && pe != nil && (pe.Code == "FILE_CONFLICT" || pe.Code == "FILE_MISSING") {
				out.CommitAttempted = false
			}
			return out, failed("delete", "APPROVED_IMPORT_SOURCE_REMOVE_FAILED")
		}
	}
	if e := s.checkpoint("approved_source_removal_completed"); e != nil {
		return out, failed("verification", "APPROVED_IMPORT_SOURCE_REMOVE_FAILED")
	}
	// The raw witness deliberately changed; all immutable metadata must not.
	reads.resumeReads = nil
	if e := reads.unchanged(); e != nil {
		return out, failed("verification", "APPROVED_IMPORT_SOURCE_REMOVE_FAILED")
	}
	if present, pe := root.Exists(source.path); pe != nil || present {
		return out, failed("verification", "APPROVED_IMPORT_SOURCE_REMOVE_FAILED")
	}
	if _, e := s.ready(root, home); e != nil {
		return out, failed("verification", "APPROVED_IMPORT_SOURCE_REMOVE_FAILED")
	}
	out.Result, _ = json.Marshal(map[string]any{"source_id": id, "removed": out.Committed, "already_removed": !out.Committed})
	return out, nil
}
