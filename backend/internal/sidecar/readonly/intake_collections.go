package readonly

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/intake/intakeview"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type IntakeExtractionPage struct {
	Items      []map[string]any `json:"items"`
	Total      int              `json:"total"`
	NextCursor *string          `json:"next_cursor"`
}

type extractionEntry struct {
	id       string
	metadata runtimepaths.ObservedMetadata
}

func observeExtractionEntries(ctx context.Context, observed *runtimepaths.ObservedRuns) ([]extractionEntry, error) {
	entries := []extractionEntry{}
	for _, id := range observed.Names() {
		if ctx.Err() != nil {
			return nil, fail("APPLICATION_READ_CANCELED")
		}
		metadata, e := observed.ChildMetadata(id, "extracted.yaml")
		if e != nil {
			return nil, fail("INTAKE_RUNS_UNAVAILABLE")
		}
		if metadata.Present {
			entries = append(entries, extractionEntry{id, metadata})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].metadata.Modified.After(entries[j].metadata.Modified) })
	return entries, nil
}

func (w *Workspace) IntakeExtractions(ctx context.Context, offset int64, limit int) (*IntakeExtractionPage, error) {
	if offset < 0 || limit < 1 || limit > 100 {
		return nil, fail("INTAKE_QUERY_INVALID")
	}
	return readOwnedAssessment(w, ctx, func(data, _ *runtimepaths.Root) (result *IntakeExtractionPage, failure error) {
		result = &IntakeExtractionPage{Items: []map[string]any{}}
		present, e := data.Exists(resumeRunsPath)
		if e != nil {
			return nil, fail("INTAKE_RUNS_UNAVAILABLE")
		}
		if !present {
			return result, nil
		}
		root, e := runtimepaths.Open(filepath.Join(w.start.DataRoot, filepath.FromSlash(resumeRunsPath)))
		if e != nil {
			return nil, fail("INTAKE_RUNS_UNAVAILABLE")
		}
		defer func() {
			if root.Close() != nil {
				result, failure = nil, fail("INTAKE_RUNS_CLEANUP_FAILED")
			}
		}()
		observed, e := root.ObserveRunDirectories()
		if e != nil {
			return nil, fail("INTAKE_RUNS_UNAVAILABLE")
		}
		entries, problem := observeExtractionEntries(ctx, observed)
		if problem != nil {
			return nil, problem
		}
		result.Total = len(entries)
		end := len(entries)
		if offset < int64(end) {
			if int(offset)+limit < end {
				end = int(offset) + limit
				cursor := "offset:" + strconv.Itoa(end)
				result.NextCursor = &cursor
			}
			var totalBytes int64
			read := func(id, leaf string, limit int64) (runtimepaths.ObservedFile, error) {
				if ctx.Err() != nil {
					return runtimepaths.ObservedFile{}, fail("APPLICATION_READ_CANCELED")
				}
				remaining := maxResumeReportBytes - totalBytes
				if limit > remaining {
					limit = remaining
				}
				file, e := observed.ReadChildFile(id, leaf, limit)
				if e != nil {
					return runtimepaths.ObservedFile{}, fail("INTAKE_EXTRACTION_UNAVAILABLE")
				}
				totalBytes += int64(len(file.Content))
				return file, nil
			}
			for _, entry := range entries[int(offset):end] {
				if !validIntakeSourceID(entry.id) {
					result.Items = append(result.Items, invalidExtractionRow(entry.id, entry.metadata.Modified))
					continue
				}
				extraction, e := read(entry.id, "extracted.yaml", intakeread.MaxExtractionBytes)
				if e != nil {
					return nil, e
				}
				review, e := read(entry.id, "review.yaml", intakeread.MaxReviewBytes)
				if e != nil {
					return nil, e
				}
				approval, e := read(entry.id, "approval_result.yaml", intakeread.MaxApprovalBytes)
				if e != nil {
					return nil, e
				}
				result.Items = append(result.Items, extractionSummary(entry.id, extraction, review, approval))
			}
		}
		if e := observed.Recheck(); e != nil {
			return nil, fail("INTAKE_RUNS_CHANGED")
		}
		encoded, err := json.Marshal(result)
		if err != nil || len(encoded) > maxIntakeViewResponseBytes {
			return nil, fail("INTAKE_VIEW_RESULT_LIMIT")
		}
		return result, nil
	})
}

func extractionTimestamp(modified time.Time) string {
	modified = modified.UTC().Round(time.Microsecond)
	layout := "2006-01-02T15:04:05.000000+00:00"
	if modified.Nanosecond() == 0 {
		layout = "2006-01-02T15:04:05+00:00"
	}
	return modified.Format(layout)
}
func invalidExtractionRow(id string, modified time.Time) map[string]any {
	return map[string]any{"id": id, "valid": false, "error": "Extraction data could not be validated.", "modified_at": extractionTimestamp(modified)}
}
func extractionSummary(id string, extraction, review, approval runtimepaths.ObservedFile) map[string]any {
	detail, e := intakeview.Extraction(extraction.Content, extraction.Modified, review.Present)
	if e != nil || detail == nil || detail.ID != id {
		return invalidExtractionRow(id, extraction.Modified)
	}
	workflow := intakeread.ReviewWorkflow(intakeread.WorkflowInput{ExtractionRaw: extraction.Content, ReviewRaw: review.Content, ReviewPresent: review.Present, ApprovalRaw: approval.Content, ApprovalPresent: approval.Present})
	if approval.Present {
		parsed, e := intakeread.ParseApproval(approval.Content)
		if e == nil && parsed.SourceID != id {
			workflow = intakeread.WorkflowResult{Status: "review_error", ErrorCode: "INTAKE_SOURCE_MISMATCH"}
		}
	} else if review.Present {
		parsed, e := intakeread.ParseReview(review.Content)
		if e == nil && parsed.Source.ID != id {
			workflow = intakeread.WorkflowResult{Status: "review_error", ErrorCode: "INTAKE_SOURCE_MISMATCH"}
		}
	}
	var savedAt any
	if workflow.SavedAt != "" {
		savedAt = workflow.SavedAt
	}
	row := map[string]any{"id": id, "valid": true, "modified_at": detail.ModifiedAt, "source_name": detail.SourceName, "counts": detail.Counts, "status": detail.Status, "review_exists": detail.ReviewExists, "workflow_status": workflow.Status, "saved_at": savedAt, "review_entity_count": workflow.ReviewEntityCount, "unresolved_block_count": workflow.UnresolvedBlockCount, "approval_issue_count": workflow.ApprovalIssueCount}
	if workflow.ErrorCode != "" {
		row["workflow_error"] = "Review data could not be validated."
	}
	return row
}
