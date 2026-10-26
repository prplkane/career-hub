package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"

	"careerhub.local/backend/internal/intake/intakeextract"
	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
)

// ExtractIntakeSource publishes only a report for one existing raw source.
// It never creates an application, approves evidence or rewrites a raw file.
func (s *Service) ExtractIntakeSource(ctx context.Context, name string, overwrite bool) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || s.expectedHome == nil || s.resourceRoot == "" || s.now == nil || s.newIntakeID == nil || s.closeRoot == nil || s.checkpoint == nil {
		return out, failed("admission", "SOURCE_EXTRACTION_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, intakeOperationTimeout)
	defer cancel()
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return out, failed("context", "SOURCE_EXTRACTION_CANCELED")
	}
	defer release()
	root, pe := runtimepaths.Open(s.root)
	if pe != nil {
		return out, failed("path", "SOURCE_EXTRACTION_UNAVAILABLE")
	}
	var resources *runtimepaths.Root
	defer func() {
		if resources != nil && s.closeRoot(resources) != nil {
			out.CleanupFailed = true
		}
		if s.closeRoot(root) != nil {
			out.CleanupFailed = true
		}
		if out.CleanupFailed {
			out.CleanupCode = "SOURCE_EXTRACTION_CLEANUP_FAILED"
			if problem == nil {
				problem = failed("cleanup", out.CleanupCode)
			}
		}
		if problem != nil {
			out.Result = nil
		}
	}()
	home, readyErr := s.ready(root, nil)
	if readyErr != nil {
		return out, failed("admission", "SOURCE_EXTRACTION_UNAVAILABLE")
	}
	finish, problem := s.beginIntakeOperation(root, out, "source_extract")
	if problem != nil {
		return out, problem
	}
	defer func() { problem = finish(problem) }()
	if !intakeFileName(name) || path.Base(name) != name || textcompat.Strip(name) != name {
		return out, failed("validation", "SOURCE_EXTRACTION_INVALID")
	}
	sourcePath := "career_vault/raw/" + name
	content, present, pe := root.ReadSingleLinkFile(sourcePath, intakeextract.MaxUploadBytes)
	if pe != nil {
		return out, failed("source", "SOURCE_EXTRACTION_UNAVAILABLE")
	}
	if !present {
		return out, failed("source", "SOURCE_EXTRACTION_INVALID")
	}
	// The source selector is literal JSON, not an encoded upload header.
	if _, e := intakeextract.ValidateUpload(url.PathEscape(name), content); e != nil {
		return out, failed("validation", "SOURCE_EXTRACTION_INVALID")
	}
	resources, pe = runtimepaths.Open(s.resourceRoot)
	if pe != nil {
		return out, failed("catalog", "SOURCE_EXTRACTION_UNAVAILABLE")
	}
	catalog, problem := readIntakeCatalog(ctx, resources, root)
	if problem != nil {
		return out, failed("catalog", "SOURCE_EXTRACTION_UNAVAILABLE")
	}
	stem := intakeSlug(textcompat.Fold(strings.TrimSuffix(name, path.Ext(name))))
	if stem == "" {
		stem = "source"
	}
	hash := digestBytes(content)
	id := "src_" + stem + "_" + hash[:10]
	reportPath, reviewPath, approvalPath := intakeReviewPaths(id)
	previous, exists, pe := root.ReadSingleLinkFile(reportPath, intakeread.MaxExtractionBytes)
	if pe != nil {
		return out, failed("report", "SOURCE_EXTRACTION_UNAVAILABLE")
	}
	if exists && !overwrite {
		return out, failed("guard", "OUTPUT_GUARD_FAILED")
	}
	review, reviewExists, pe := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
	if pe != nil {
		return out, failed("review", "SOURCE_EXTRACTION_UNAVAILABLE")
	}
	approved, pe := root.Exists(approvalPath)
	if pe != nil {
		return out, failed("review", "SOURCE_EXTRACTION_UNAVAILABLE")
	}
	// Applied evidence is immutable: use explicit process-again for a new run.
	if approved {
		return out, failed("guard", "OUTPUT_GUARD_FAILED")
	}
	if exists {
		old, e := intakeread.ParseExtraction(previous)
		if e != nil || old.Source.ID != id || old.Source.Path != sourcePath || old.Source.SHA256 != hash {
			return out, failed("guard", "OUTPUT_GUARD_FAILED")
		}
	}
	ref := intakeextract.Source{ID: id, Path: sourcePath, FileType: strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")), SHA256: hash}
	report, err := intakeextract.Extract(ctx, content, ref, catalog.revision, catalog.terms)
	if err != nil {
		if ctx.Err() != nil {
			return out, failed("context", "SOURCE_EXTRACTION_CANCELED")
		}
		if errors.Is(err, intakeextract.ErrLimit) {
			return out, failed("extract", "SOURCE_EXTRACTION_LIMIT")
		}
		return out, failed("extract", "SOURCE_EXTRACTION_INVALID")
	}
	reportBytes, err := json.Marshal(report)
	if err != nil || len(reportBytes) > intakeread.MaxExtractionBytes {
		return out, failed("report", "SOURCE_EXTRACTION_LIMIT")
	}
	parsed, parseError := intakeread.ParseExtraction(reportBytes)
	if parseError != nil {
		return out, failed("report_validation", "SOURCE_EXTRACTION_INVALID")
	}
	if parsed.Source.ID != id || parsed.Source.Path != sourcePath || parsed.Source.SHA256 != hash || parsed.RegistryRevision != catalog.revision {
		return out, failed("report", "SOURCE_EXTRACTION_INVALID")
	}
	unchanged := func() bool {
		if ctx.Err() != nil {
			return false
		}
		if _, e := s.ready(root, home); e != nil {
			return false
		}
		current, present, e := root.ReadSingleLinkFile(sourcePath, intakeextract.MaxUploadBytes)
		if e != nil || !present || !bytes.Equal(current, content) {
			return false
		}
		currentReview, present, e := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
		if e != nil || present != reviewExists || !bytes.Equal(currentReview, review) {
			return false
		}
		if approved, e := root.Exists(approvalPath); e != nil || approved {
			return false
		}
		currentCatalog, e2 := readIntakeCatalog(ctx, resources, root)
		if e2 != nil || len(currentCatalog.witnesses) != len(catalog.witnesses) || currentCatalog.revision != catalog.revision {
			return false
		}
		for i, w := range catalog.witnesses {
			if currentCatalog.witnesses[i] != w {
				return false
			}
		}
		return true
	}
	if e := s.checkpoint("source_extraction_prepared"); e != nil {
		return out, failed("verification", "SOURCE_EXTRACTION_CHANGED")
	}
	if !unchanged() {
		return out, failed("verification", "SOURCE_EXTRACTION_CHANGED")
	}
	if _, pe := root.EnsureDirectory(path.Dir(reportPath)); pe != nil {
		return out, failed("path", "SOURCE_EXTRACTION_UNAVAILABLE")
	}
	out.CommitAttempted = true
	var committed bool
	if exists {
		committed, pe = s.intakeMutations.replaceFile(root, reportPath, previous, reportBytes)
	} else {
		committed, pe = s.intakeMutations.createFile(root, reportPath, reportBytes)
	}
	out.Committed = committed
	if pe != nil || !committed {
		if !committed && pe != nil && (pe.Code == "FILE_EXISTS" || pe.Code == "FILE_CONFLICT" || pe.Code == "FILE_MISSING") {
			out.CommitAttempted = false
			return out, failed("guard", "OUTPUT_GUARD_FAILED")
		}
		return out, failed("write", "SOURCE_EXTRACTION_WRITE_UNCERTAIN")
	}
	if e := s.checkpoint("source_extraction_committed"); e != nil {
		return out, failed("verification", "SOURCE_EXTRACTION_COMMITTED_UNVERIFIED")
	}
	if !intakeFileEquals(root, reportPath, reportBytes) || !unchanged() {
		return out, failed("verification", "SOURCE_EXTRACTION_COMMITTED_UNVERIFIED")
	}
	out.Result, _ = json.Marshal(struct {
		OutputPath      string `json:"output_path"`
		SourceID        string `json:"source_id"`
		TechnologyCount int    `json:"technology_count"`
		BulletCount     int    `json:"bullet_count"`
		RunID           string `json:"run_id"`
	}{reportPath, id, len(report.TechnologyIDs), len(report.BulletCandidates), out.RunID})
	return out, nil
}
