package applicationwrite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

// EditJobDetails replaces exactly one existing normalized card. It never
// writes its raw JD, mutates the ledger, or asserts that an assessment was
// recomputed. The caller supplies the SHA of the card bytes it displayed;
// fetching a current revision here would silently approve an unseen edit.
// This native owner is not exposed through HTTP until the multi-owner response
// and recovery contract has been reviewed.
func (s *Service) EditJobDetails(ctx context.Context, id, expectedRevision string, updates map[string]any) (out *Outcome, problem *Error) {
	out = &Outcome{}
	requestBytes, requestErr := json.Marshal(updates)
	if s == nil || ctx == nil || s.checkpoint == nil || !jobcard.ValidID(id) || !validJobDetailsRevision(expectedRevision) || updates == nil || requestErr != nil || len(requestBytes) > 16384 {
		return out, failed("input", "JOB_DETAILS_REQUEST_INVALID")
	}
	if s.expectedHome == nil {
		return out, failed("admission", "JOB_DETAILS_HOME_PIN_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return out, canceled()
	}
	defer release()
	root, openErr := runtimepaths.Open(s.root)
	if openErr != nil {
		return out, failed("path", openErr.Code)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			out.CleanupFailed, out.CleanupCode = true, "JOB_DETAILS_ROOT_CLOSE_FAILED"
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
		return out, readyErr
	}
	path := "jobs/normalized/" + id + ".yaml"
	loaded, readErr := jobcard.Read(ctx, root, path)
	if readErr != nil {
		return out, failed("source", readErr.Code)
	}
	if loaded.Report == nil || !loaded.Report.SchemaValid() || jobDetailsField(loaded, "id") != id || !loaded.RawPresent {
		return out, failed("source", "JOB_DETAILS_CARD_INVALID")
	}
	previous, present, fileErr := root.ReadSingleLinkFile(path, cataloginput.YAMLMaxInputBytes)
	if fileErr != nil || !present || sha256.Sum256(previous) != loaded.CardFileSHA256 {
		return out, failed("source", "JOB_DETAILS_CARD_CHANGED")
	}
	currentRevision := "sha256:" + hex.EncodeToString(loaded.CardFileSHA256[:])
	if currentRevision != expectedRevision {
		return out, failed("revision", "JOB_DETAILS_REVISION_CONFLICT")
	}
	if e := jobDetailsSource(ctx, root, loaded); e != nil {
		return out, e
	}
	card, editErr := jobcard.EditDetails(ctx, loaded, updates)
	if editErr != nil {
		return out, failed("input", editErr.Code)
	}
	report, validateErr := jobcard.Validate(ctx, card, func(ctx context.Context, relative string) (bool, *jobcard.Error) {
		if relative != jobDetailsField(loaded, "raw_jd_path") || jobDetailsSource(ctx, root, loaded) != nil {
			return false, &jobcard.Error{Code: "JOB_CARD_SOURCE_UNAVAILABLE"}
		}
		return true, nil
	})
	if validateErr != nil || report == nil || !report.SchemaValid() {
		return out, failed("validation", "JOB_DETAILS_CARD_INVALID")
	}
	next, encodeErr := jobcard.EncodeCard(ctx, card)
	if encodeErr != nil {
		return out, failed("encode", encodeErr.Code)
	}
	if _, e := s.ready(root, home); e != nil {
		return out, e
	}
	if e := jobDetailsSource(ctx, root, loaded); e != nil {
		return out, e
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	// ReplaceFile compares the exact prior bytes and retains the parent. It is
	// not cross-process CAS; process ownership and the operation gate are still
	// required. A post-attempt failure is not a safe blind retry.
	out.CommitAttempted = true
	committed, replaceErr := root.ReplaceFile(path, previous, next)
	out.Committed = committed
	if replaceErr != nil {
		if !committed && (replaceErr.Code == "FILE_CONFLICT" || replaceErr.Code == "FILE_MISSING") {
			out.CommitAttempted = false
			return out, failed("revision", "JOB_DETAILS_REVISION_CONFLICT")
		}
		return out, failed("write", "JOB_DETAILS_WRITE_UNCERTAIN")
	}
	if !committed {
		return out, failed("write", "JOB_DETAILS_WRITE_UNCERTAIN")
	}
	if e := s.checkpoint("job_details_committed"); e != nil {
		return out, e
	}
	readback, readbackErr := jobcard.Read(ctx, root, path)
	if readbackErr != nil || readback == nil || readback.Report == nil || !readback.Report.SchemaValid() || readback.CardFileSHA256 != sha256.Sum256(next) {
		return out, failed("verification", "JOB_DETAILS_COMMITTED_UNVERIFIED")
	}
	if _, e := s.ready(root, home); e != nil {
		return out, failed("verification", "JOB_DETAILS_COMMITTED_UNVERIFIED")
	}
	result, jsonErr := json.Marshal(map[string]any{"id": id, "card_revision": "sha256:" + hex.EncodeToString(readback.CardFileSHA256[:])})
	if jsonErr != nil {
		return out, failed("response", "JOB_DETAILS_COMMITTED_UNVERIFIED")
	}
	out.Result = result
	return out, nil
}

func validJobDetailsRevision(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[7:] {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func jobDetailsField(loaded *jobcard.Loaded, name string) string {
	if loaded == nil {
		return ""
	}
	for _, entry := range loaded.Card.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == name && entry.Value.Kind == "string" {
			return entry.Value.Text
		}
	}
	return ""
}

func jobDetailsSource(ctx context.Context, root *runtimepaths.Root, loaded *jobcard.Loaded) *Error {
	if ctx.Err() != nil {
		return canceled()
	}
	path := jobDetailsField(loaded, "raw_jd_path")
	raw, present, e := root.ReadSingleLinkFile(path, applicationanalysis.MaxSourceBytes)
	if e != nil || !present || sha256.Sum256(raw) != loaded.RawFileSHA256 {
		return failed("source", "JOB_DETAILS_SOURCE_CHANGED")
	}
	fingerprint, fingerprintErr := applicationanalysis.SourceFingerprint(strings.TrimPrefix(loaded.RawText, "\ufeff"))
	if fingerprintErr != nil {
		return failed("source", "JOB_DETAILS_SOURCE_INVALID")
	}
	for _, entry := range loaded.Card.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == "content_fingerprint" && entry.Value.Kind == "string" && entry.Value.Text != "" && entry.Value.Text != fingerprint {
			return failed("source", "JOB_DETAILS_SOURCE_CHANGED")
		}
	}
	return nil
}
