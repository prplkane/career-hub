package applicationwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// ReanalysisPreparation is a fresh, unpublished read from the admitted Go
// digest owner. A caller must supply the revision it displayed separately;
// this structure is not permission to overwrite an unseen card.
type ReanalysisPreparation struct {
	Digest                                           *jobdigest.Result
	CardFileSHA256, RawFileSHA256, ProfileFileSHA256 [32]byte
	PlannedCardSHA256                                [32]byte
	ProfilePresent                                   bool
	CatalogRevision                                  string
}

// ReanalyzeJob replaces one draft card after rechecking every input that
// produced the prepared digest. It never edits the raw JD or application
// ledger and is not yet connected to HTTP or the installed runtime.
func (s *Service) ReanalyzeJob(ctx context.Context, id, expectedRevision string, prepared ReanalysisPreparation) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || s.checkpoint == nil || s.closeRoot == nil || s.resourceRoot == "" ||
		!jobcard.ValidID(id) || !validJobDetailsRevision(expectedRevision) || prepared.Digest == nil ||
		!validJobDetailsRevision("sha256:"+prepared.CatalogRevision) || prepared.PlannedCardSHA256 == ([32]byte{}) {
		return out, failed("input", "JOB_REANALYSIS_REQUEST_INVALID")
	}
	if s.expectedHome == nil {
		return out, failed("admission", "JOB_REANALYSIS_HOME_PIN_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return out, canceled()
	}
	defer release()
	data, openErr := runtimepaths.Open(s.root)
	if openErr != nil {
		return out, failed("path", "JOB_REANALYSIS_ROOT_UNAVAILABLE")
	}
	defer func() {
		if e := s.closeRoot(data); e != nil {
			out.CleanupFailed, out.CleanupCode = true, "JOB_REANALYSIS_ROOT_CLOSE_FAILED"
			if problem == nil {
				problem = failed("cleanup", out.CleanupCode)
			}
		}
		if problem != nil {
			out.Result = nil
		}
	}()
	resources, resourceErr := runtimepaths.Open(s.resourceRoot)
	if resourceErr != nil {
		return out, failed("path", "JOB_REANALYSIS_RESOURCE_UNAVAILABLE")
	}
	defer func() {
		if e := s.closeRoot(resources); e != nil {
			out.CleanupFailed, out.CleanupCode = true, "JOB_REANALYSIS_RESOURCE_CLOSE_FAILED"
			if problem == nil {
				problem = failed("cleanup", out.CleanupCode)
			}
		}
	}()
	home, readyErr := s.ready(data, nil)
	if readyErr != nil {
		return out, readyErr
	}
	path := "jobs/normalized/" + id + ".yaml"
	loaded, readErr := jobcard.Read(ctx, data, path)
	if readErr != nil || loaded == nil || loaded.Report == nil || !loaded.Report.SchemaValid() || jobDetailsField(loaded, "id") != id {
		return out, failed("source", "JOB_REANALYSIS_CARD_INVALID")
	}
	if jobDetailsField(loaded, "status") != "draft" {
		return out, failed("source", "JOB_REANALYSIS_NOT_DRAFT")
	}
	previous, present, fileErr := data.ReadSingleLinkFile(path, cataloginput.YAMLMaxInputBytes)
	if fileErr != nil || !present || sha256.Sum256(previous) != loaded.CardFileSHA256 {
		return out, failed("source", "JOB_REANALYSIS_CARD_CHANGED")
	}
	if "sha256:"+hex.EncodeToString(loaded.CardFileSHA256[:]) != expectedRevision {
		return out, failed("revision", "JOB_REANALYSIS_REVISION_CONFLICT")
	}
	if e := reanalysisWitnesses(ctx, data, resources, loaded, prepared); e != nil {
		return out, e
	}
	card, transformErr := jobcard.PrepareReanalysis(ctx, loaded, prepared.Digest)
	if transformErr != nil {
		return out, failed("transform", transformErr.Code)
	}
	next, encodeErr := jobcard.EncodeCard(ctx, card)
	if encodeErr != nil {
		return out, failed("encode", encodeErr.Code)
	}
	if sha256.Sum256(next) != prepared.PlannedCardSHA256 {
		return out, failed("transform", "JOB_REANALYSIS_PLAN_CHANGED")
	}
	// A no-plan caller is permitted only when the exact bytes are unchanged;
	// equal hashes alone must never authorize an unjournaled replacement.
	if prepared.PlannedCardSHA256 == prepared.CardFileSHA256 && !bytes.Equal(next, previous) {
		return out, failed("transform", "JOB_REANALYSIS_PLAN_CHANGED")
	}
	if _, e := s.ready(data, home); e != nil {
		return out, e
	}
	if e := reanalysisWitnesses(ctx, data, resources, loaded, prepared); e != nil {
		return out, e
	}
	current, stillPresent, currentErr := data.ReadSingleLinkFile(path, cataloginput.YAMLMaxInputBytes)
	if currentErr != nil || !stillPresent || !bytes.Equal(previous, current) {
		return out, failed("revision", "JOB_REANALYSIS_REVISION_CONFLICT")
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	if bytes.Equal(previous, next) {
		result, marshalErr := json.Marshal(map[string]any{"id": id, "card_revision": expectedRevision, "unchanged": true})
		if marshalErr != nil {
			return out, failed("response", "JOB_REANALYSIS_RESULT_INVALID")
		}
		out.Result = result
		return out, nil
	}
	// The exact-prior-byte replacement is the sole card publication. An error
	// after this call can mean a committed card and must retain that fact.
	out.CommitAttempted = true
	committed, replaceErr := data.ReplaceFile(path, previous, next)
	out.Committed = committed
	if replaceErr != nil {
		if !committed && (replaceErr.Code == "FILE_CONFLICT" || replaceErr.Code == "FILE_MISSING") {
			out.CommitAttempted = false
			return out, failed("revision", "JOB_REANALYSIS_REVISION_CONFLICT")
		}
		return out, failed("write", "JOB_REANALYSIS_WRITE_UNCERTAIN")
	}
	if !committed {
		return out, failed("write", "JOB_REANALYSIS_WRITE_UNCERTAIN")
	}
	if e := s.checkpoint("job_reanalysis_committed"); e != nil {
		return out, e
	}
	readback, readbackErr := jobcard.Read(ctx, data, path)
	if readbackErr != nil || readback == nil || readback.Report == nil || !readback.Report.SchemaValid() || readback.CardFileSHA256 != sha256.Sum256(next) {
		return out, failed("verification", "JOB_REANALYSIS_COMMITTED_UNVERIFIED")
	}
	if _, e := s.ready(data, home); e != nil {
		return out, failed("verification", "JOB_REANALYSIS_COMMITTED_UNVERIFIED")
	}
	if e := reanalysisContextWitnesses(ctx, data, resources, readback, prepared); e != nil {
		return out, failed("verification", "JOB_REANALYSIS_COMMITTED_UNVERIFIED")
	}
	result, marshalErr := json.Marshal(map[string]any{"id": id, "card_revision": "sha256:" + hex.EncodeToString(readback.CardFileSHA256[:])})
	if marshalErr != nil {
		return out, failed("response", "JOB_REANALYSIS_COMMITTED_UNVERIFIED")
	}
	out.Result = result
	return out, nil
}

func reanalysisWitnesses(ctx context.Context, data, resources *runtimepaths.Root, loaded *jobcard.Loaded, prepared ReanalysisPreparation) *Error {
	if loaded.CardFileSHA256 != prepared.CardFileSHA256 {
		return failed("revision", "JOB_REANALYSIS_REVISION_CONFLICT")
	}
	return reanalysisContextWitnesses(ctx, data, resources, loaded, prepared)
}

func reanalysisContextWitnesses(ctx context.Context, data, resources *runtimepaths.Root, loaded *jobcard.Loaded, prepared ReanalysisPreparation) *Error {
	if ctx == nil || ctx.Err() != nil {
		return canceled()
	}
	rawPath := jobDetailsField(loaded, "raw_jd_path")
	raw, present, rawErr := data.ReadSingleLinkFile(rawPath, applicationanalysis.MaxSourceBytes)
	if rawErr != nil || !present || sha256.Sum256(raw) != prepared.RawFileSHA256 || loaded.RawFileSHA256 != prepared.RawFileSHA256 {
		return failed("source", "JOB_REANALYSIS_INPUT_CHANGED")
	}
	profile, profilePresent, profileErr := data.ReadSingleLinkFile("career_vault/visa_profile.yaml", cataloginput.YAMLMaxInputBytes)
	if profileErr != nil || profilePresent != prepared.ProfilePresent || sha256.Sum256(profile) != prepared.ProfileFileSHA256 {
		return failed("profile", "JOB_REANALYSIS_INPUT_CHANGED")
	}
	registry, registryErr := catalogstore.ReadState(resources, data)
	if registryErr != nil || registry == nil || registry.Snapshot == nil || registry.Snapshot.CatalogRevision() != prepared.CatalogRevision {
		return failed("catalog", "JOB_REANALYSIS_INPUT_CHANGED")
	}
	if ctx.Err() != nil {
		return canceled()
	}
	return nil
}
