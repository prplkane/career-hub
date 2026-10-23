package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"careerhub.local/backend/internal/intake/intakeextract"
	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// UploadIntakeSource saves raw bytes only. Extraction and approval are separate
// explicit operations. Existing files are never replaced or deleted.
func (s *Service) UploadIntakeSource(ctx context.Context, encodedName string, content []byte, reimport bool) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || s.expectedHome == nil || s.now == nil || s.newIntakeID == nil || s.closeRoot == nil || s.checkpoint == nil {
		return out, failed("admission", "SOURCE_UPLOAD_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return out, failed("context", "SOURCE_UPLOAD_CANCELED")
	}
	defer release()
	root, pe := runtimepaths.Open(s.root)
	if pe != nil {
		return out, failed("path", "SOURCE_UPLOAD_UNAVAILABLE")
	}
	defer func() {
		if s.closeRoot(root) != nil {
			out.CleanupFailed, out.CleanupCode = true, "SOURCE_UPLOAD_CLEANUP_FAILED"
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
		return out, failed("admission", "SOURCE_UPLOAD_UNAVAILABLE")
	}
	finish, problem := s.beginIntakeOperation(root, out, "source_upload")
	if problem != nil {
		return out, problem
	}
	defer func() { problem = finish(problem) }()
	name, e := intakeextract.ValidateUpload(encodedName, content)
	if e != nil {
		return out, failed("validation", "SOURCE_UPLOAD_FAILED")
	}
	if reimport {
		found, problem := priorUploadExists(ctx, root, s.root, digestBytes(content))
		if problem != nil {
			return out, problem
		}
		if !found {
			return out, failed("validation", "SOURCE_REIMPORT_NOT_FOUND")
		}
		base, suffix := strings.TrimSuffix(name, path.Ext(name)), path.Ext(name)
		foundName := false
		for n := 2; n <= 10000; n++ {
			if ctx.Err() != nil {
				return out, failed("context", "SOURCE_UPLOAD_CANCELED")
			}
			candidate := fmt.Sprintf("%s (reimport %d)%s", base, n, suffix)
			if len(candidate) > 255 {
				return out, failed("validation", "SOURCE_UPLOAD_FAILED")
			}
			present, pe := root.Exists("career_vault/raw/" + candidate)
			if pe != nil {
				return out, failed("path", "SOURCE_UPLOAD_UNAVAILABLE")
			}
			if !present {
				name, foundName = candidate, true
				break
			}
		}
		if !foundName {
			return out, failed("validation", "SOURCE_UPLOAD_UNAVAILABLE")
		}
	}
	if _, pe := root.EnsureDirectory("career_vault/raw"); pe != nil {
		return out, failed("path", "SOURCE_UPLOAD_UNAVAILABLE")
	}
	if _, e := s.ready(root, home); e != nil {
		return out, failed("admission", "SOURCE_UPLOAD_UNAVAILABLE")
	}
	if ctx.Err() != nil {
		return out, failed("context", "SOURCE_UPLOAD_CANCELED")
	}
	target := "career_vault/raw/" + name
	out.CommitAttempted = true
	created, pe := s.intakeMutations.createFile(root, target, content)
	out.Committed = created
	if !created && pe != nil && pe.Code == "FILE_EXISTS" {
		out.CommitAttempted = false
		return out, failed("conflict", "SOURCE_ALREADY_EXISTS")
	}
	if pe != nil || !created {
		return out, failed("write", "SOURCE_UPLOAD_WRITE_UNCERTAIN")
	}
	if e := s.checkpoint("source_upload_committed"); e != nil {
		return out, failed("verification", "SOURCE_UPLOAD_COMMITTED_UNVERIFIED")
	}
	readback, present, pe := root.ReadSingleLinkFile(target, intakeextract.MaxUploadBytes)
	if pe != nil || !present || !bytes.Equal(readback, content) {
		return out, failed("verification", "SOURCE_UPLOAD_COMMITTED_UNVERIFIED")
	}
	if _, e := s.ready(root, home); e != nil {
		return out, failed("verification", "SOURCE_UPLOAD_COMMITTED_UNVERIFIED")
	}
	out.Result, _ = json.Marshal(struct {
		Name       string `json:"name"`
		Path       string `json:"path"`
		RunID      string `json:"run_id"`
		Reimported bool   `json:"reimported"`
	}{name, target, out.RunID, reimport})
	return out, nil
}

// This is a write-admission predicate, not a second workflow classifier.
// It holds bounded witnesses while proving at least one exact extraction.
func priorUploadExists(ctx context.Context, data *runtimepaths.Root, dataRoot, hash string) (found bool, problem *Error) {
	present, pe := data.Exists("career_vault/intake_runs")
	if pe != nil {
		return false, failed("source", "SOURCE_UPLOAD_UNAVAILABLE")
	}
	if !present {
		return false, nil
	}
	runs, pe := runtimepaths.Open(filepath.Join(dataRoot, "career_vault", "intake_runs"))
	if pe != nil {
		return false, failed("source", "SOURCE_UPLOAD_UNAVAILABLE")
	}
	defer func() {
		if runs.Close() != nil {
			found, problem = false, failed("source", "SOURCE_UPLOAD_UNAVAILABLE")
		}
	}()
	observed, pe := runs.ObserveRunDirectories()
	if pe != nil {
		return false, failed("source", "SOURCE_UPLOAD_UNAVAILABLE")
	}
	remaining := int64(32 << 20)
	for _, id := range observed.Names() {
		if ctx.Err() != nil {
			return false, failed("context", "SOURCE_UPLOAD_CANCELED")
		}
		if !intakeReviewSourceID(id) {
			continue
		}
		limit := min(int64(intakeread.MaxExtractionBytes), remaining)
		file, pe := observed.ReadChildFile(id, "extracted.yaml", limit)
		if pe != nil {
			return false, failed("source", "SOURCE_UPLOAD_UNAVAILABLE")
		}
		remaining -= int64(len(file.Content))
		extraction, e := intakeread.ParseExtraction(file.Content)
		if e == nil && extraction.Source.ID == id && extraction.Source.SHA256 == hash {
			found = true
			break
		}
	}
	if observed.Recheck() != nil {
		return false, failed("source", "SOURCE_UPLOAD_UNAVAILABLE")
	}
	return found, nil
}
