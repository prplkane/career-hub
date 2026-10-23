package readonly

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices"

	"careerhub.local/backend/internal/intake/intakeextract"
	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
)

type PriorImport struct {
	SourceID       string `json:"source_id"`
	WorkflowStatus string `json:"workflow_status"`
	ProcessedAt    string `json:"processed_at"`
}
type IntakeUploadCheck struct {
	PriorImports     []PriorImport `json:"prior_imports"`
	SameNameConflict bool          `json:"same_name_conflict"`
}

func (w *Workspace) CheckIntakeUpload(ctx context.Context, encodedName string, content []byte) (*IntakeUploadCheck, error) {
	name, e := intakeextract.ValidateUpload(encodedName, content)
	if e != nil {
		return nil, fail("SOURCE_UPLOAD_CHECK_FAILED")
	}
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	return readOwnedAssessment(w, ctx, func(data, _ *runtimepaths.Root) (result *IntakeUploadCheck, problem error) {
		names, present, e := data.ListDirectoryFileNames("career_vault/raw")
		if e != nil {
			return nil, fail("SOURCE_UPLOAD_CHECK_UNAVAILABLE")
		}
		result = &IntakeUploadCheck{PriorImports: []PriorImport{}}
		for _, existing := range names {
			if textcompat.Fold(existing) == textcompat.Fold(name) {
				result.SameNameConflict = true
			}
		}
		defer func() {
			after, exists, e := data.ListDirectoryFileNames("career_vault/raw")
			if e != nil || exists != present || !slices.Equal(names, after) {
				result, problem = nil, fail("SOURCE_UPLOAD_CHECK_UNAVAILABLE")
			}
		}()
		hasRuns, e := data.Exists(resumeRunsPath)
		if e != nil {
			return nil, fail("SOURCE_UPLOAD_CHECK_UNAVAILABLE")
		}
		if !hasRuns {
			return result, nil
		}
		root, e := runtimepaths.Open(filepath.Join(w.start.DataRoot, filepath.FromSlash(resumeRunsPath)))
		if e != nil {
			return nil, fail("SOURCE_UPLOAD_CHECK_UNAVAILABLE")
		}
		defer func() {
			if root.Close() != nil {
				result, problem = nil, fail("SOURCE_UPLOAD_CHECK_UNAVAILABLE")
			}
		}()
		observed, e := root.ObserveRunDirectories()
		if e != nil {
			return nil, fail("SOURCE_UPLOAD_CHECK_UNAVAILABLE")
		}
		entries, problem := observeExtractionEntries(ctx, observed)
		if problem != nil {
			return nil, problem
		}
		var total int64
		read := func(id, leaf string, limit int64) (runtimepaths.ObservedFile, error) {
			if ctx.Err() != nil {
				return runtimepaths.ObservedFile{}, fail("APPLICATION_READ_CANCELED")
			}
			if remaining := maxResumeReportBytes - total; limit > remaining {
				limit = remaining
			}
			file, e := observed.ReadChildFile(id, leaf, limit)
			if e != nil {
				return runtimepaths.ObservedFile{}, fail("SOURCE_UPLOAD_CHECK_UNAVAILABLE")
			}
			total += int64(len(file.Content))
			return file, nil
		}
		for _, entry := range entries {
			if !validIntakeSourceID(entry.id) {
				continue
			}
			extraction, e := read(entry.id, "extracted.yaml", intakeread.MaxExtractionBytes)
			if e != nil {
				return nil, e
			}
			parsed, p := intakeread.ParseExtraction(extraction.Content)
			if p != nil || parsed.Source.ID != entry.id || parsed.Source.SHA256 != hash {
				continue
			}
			review, e := read(entry.id, "review.yaml", intakeread.MaxReviewBytes)
			if e != nil {
				return nil, e
			}
			approval, e := read(entry.id, "approval_result.yaml", intakeread.MaxApprovalBytes)
			if e != nil {
				return nil, e
			}
			row := extractionSummary(entry.id, extraction, review, approval)
			status, ok := row["workflow_status"].(string)
			if !ok {
				return nil, fail("SOURCE_UPLOAD_CHECK_UNAVAILABLE")
			}
			result.PriorImports = append(result.PriorImports, PriorImport{entry.id, status, extractionTimestamp(extraction.Modified)})
			if len(result.PriorImports) == 5 {
				break
			}
		}
		if e := observed.Recheck(); e != nil {
			return nil, fail("SOURCE_UPLOAD_CHECK_UNAVAILABLE")
		}
		return result, nil
	})
}
