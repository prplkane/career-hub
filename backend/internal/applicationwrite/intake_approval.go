package applicationwrite

import (
	"bytes"
	"context"
	"errors"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/catalogtransaction"
	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/intakereview"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sourceupload"
	"careerhub.local/backend/internal/vaultwrite"
)

// approvalInputs is a closed in-memory before-image reader, not a filesystem
// capability. Missing optional catalogs retain their absence during replanning.
type approvalInputs map[string]captureObservation

func (r approvalInputs) Check(path string) *runtimepaths.Error {
	if _, ok := r[path]; !ok {
		return &runtimepaths.Error{Code: "PATH_INVALID"}
	}
	return nil
}
func (r approvalInputs) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	v, ok := r[path]
	if !ok || limit < 0 || int64(len(v.raw)) > limit {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	return bytes.Clone(v.raw), v.present, nil
}

func approvalCoreUnchanged(core *sourceReads) bool {
	for _, v := range core.captures {
		raw, present, err := core.root.ReadSingleLinkFile(v.path, cataloginput.YAMLMaxInputBytes)
		if err != nil || present != v.present || !bytes.Equal(raw, v.raw) {
			return false
		}
	}
	return true
}

func approvalChanges(plan *vaultwrite.IntakeApprovalPlan, inputs approvalInputs, id string) []catalogtransaction.Change {
	paths := append(intakereview.ApprovalPreviewPaths(), catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath)
	replacements := plan.Replacements()
	changes := make([]catalogtransaction.Change, 0, 8)
	for _, path := range paths {
		before := inputs[path]
		after, changed := replacements[path]
		if !changed {
			after = before.raw
		}
		changes = append(changes, catalogtransaction.Change{Path: path, BeforePresent: before.present, Before: before.raw, After: after, CheckOnly: !changed})
	}
	_, _, resultPath := intakeReviewPaths(id)
	return append(changes, catalogtransaction.Change{Path: resultPath, RecordID: id, After: plan.ResultBytes()})
}

// Rebuild all after-images from retained decisions rather than trusting a
// checksum or merely validating the journal's prospective YAML shapes.
func replanApproval(ctx context.Context, root *runtimepaths.Root, core *sourceReads, changes []catalogtransaction.Change) ([]byte, *Error) {
	if len(changes) != 8 || !intakeReviewSourceID(changes[7].RecordID) {
		return nil, failed("recovery", "APPROVAL_JOURNAL_INVALID")
	}
	id := changes[7].RecordID
	extractionPath, reviewPath, resultPath := intakeReviewPaths(id)
	if changes[7].Path != resultPath {
		return nil, failed("recovery", "APPROVAL_JOURNAL_INVALID")
	}
	result, parseErr := intakeread.ParseApproval(changes[7].After)
	if parseErr != nil || result.SourceID != id || result.PreviousRegistryRevision == nil {
		return nil, failed("recovery", "APPROVAL_JOURNAL_INVALID")
	}
	extraction, present, pe := root.ReadSingleLinkFile(extractionPath, intakeread.MaxExtractionBytes)
	if pe != nil || !present {
		return nil, failed("source", "APPROVAL_UNAVAILABLE")
	}
	review, present, pe := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
	if pe != nil || !present {
		return nil, failed("source", "APPROVAL_UNAVAILABLE")
	}
	inputs := approvalInputs{}
	vault := map[string][]byte{}
	paths := append(intakereview.ApprovalPreviewPaths(), catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath)
	for i, path := range paths {
		if changes[i].Path != path {
			return nil, failed("recovery", "APPROVAL_JOURNAL_INVALID")
		}
		inputs[path] = captureObservation{path, changes[i].Before, changes[i].BeforePresent}
		if i < 5 {
			vault[path] = changes[i].Before
		}
	}
	state, loadErr := catalogstore.ReadState(core, inputs)
	if loadErr != nil {
		return nil, failed("catalog", "APPROVAL_UNAVAILABLE")
	}
	plan, planErr := vaultwrite.PlanIntakeApproval(ctx, extraction, review, vault, state, result.ReviewRevision, result.PreviousVaultRevision, *result.PreviousRegistryRevision, result.AppliedAt)
	if planErr != nil {
		return nil, failed("recovery", "APPROVAL_TARGET_INVALID")
	}
	expected := approvalChanges(plan, inputs, id)
	for i, c := range expected {
		actual := changes[i]
		if c.Path != actual.Path || c.BeforePresent != actual.BeforePresent || c.CheckOnly != actual.CheckOnly || c.RecordID != actual.RecordID || !bytes.Equal(c.Before, actual.Before) || !bytes.Equal(c.After, actual.After) {
			return nil, failed("recovery", "APPROVAL_TARGET_INVALID")
		}
	}
	if !approvalCoreUnchanged(core) {
		return nil, failed("catalog", "APPROVAL_SOURCE_CHANGED")
	}
	return plan.ResultBytes(), nil
}

// ApplyIntakeApproval is explicit approval, not review auto-save. It performs no
// SQLite operation and never falls back to the legacy Python transaction.
func (s *Service) ApplyIntakeApproval(ctx context.Context, id, reviewRevision, vaultRevision, registryRevision string) (*Outcome, *Error) {
	if !intakeReviewSourceID(id) || !intakeReviewRevision(reviewRevision) || !intakeReviewRevision(vaultRevision) || !intakeReviewRevision("sha256:"+registryRevision) {
		return &Outcome{}, failed("input", "APPROVAL_INVALID")
	}
	return s.runApproval(ctx, false, id, reviewRevision, vaultRevision, registryRevision)
}

// RecoverIntakeApproval only advances an existing closed approval intent.
// It must be exposed through an explicit owned recovery boundary, not retried
// as an ordinary approval request or invoked automatically on startup.
func (s *Service) RecoverIntakeApproval(ctx context.Context) (*Outcome, *Error) {
	return s.runApproval(ctx, true, "", "", "", "")
}

func (s *Service) runApproval(ctx context.Context, recovery bool, id, reviewRevision, vaultRevision, registryRevision string) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || s.expectedHome == nil || s.resourceRoot == "" || s.closeRoot == nil || s.now == nil || s.newIntakeID == nil || s.checkpoint == nil {
		return out, failed("admission", "APPROVAL_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, intakeOperationTimeout)
	defer cancel()
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return out, failed("context", "APPROVAL_CANCELED")
	}
	defer release()
	root, pe := runtimepaths.Open(s.root)
	if pe != nil {
		return out, failed("path", "APPROVAL_UNAVAILABLE")
	}
	var resources *runtimepaths.Root
	defer func() {
		for _, r := range []*runtimepaths.Root{resources, root} {
			if r != nil && s.closeRoot(r) != nil {
				out.CleanupFailed = true
				out.CleanupCode = "APPROVAL_CLEANUP_FAILED"
			}
		}
		if out.CleanupFailed && problem == nil {
			problem = failed("cleanup", "APPROVAL_COMMITTED_UNVERIFIED")
		}
		if problem != nil {
			out.Result = nil
		}
	}()
	mode, operation := "", "approval_save"
	if recovery {
		mode, operation = "approval", "approval_recover"
	}
	home, e := s.readyMode(root, nil, mode)
	if e != nil {
		out.RecoveryRequired = e.Code == "DATA_HOME_RECOVERY_REQUIRED"
		return out, failed("admission", "APPROVAL_UNAVAILABLE")
	}
	finish, e := s.beginIntakeOperation(root, out, operation)
	if e != nil {
		return out, e
	}
	defer func() { problem = finish(problem) }()
	resources, pe = runtimepaths.Open(s.resourceRoot)
	if pe != nil {
		return out, failed("resources", "APPROVAL_UNAVAILABLE")
	}
	core := &sourceReads{ctx: ctx, root: resources}
	var changes []catalogtransaction.Change
	var guards []catalogtransaction.Guard
	if !recovery {
		extractionPath, reviewPath, resultPath := intakeReviewPaths(id)
		if present, pe := root.Exists(resultPath); pe != nil {
			return out, failed("result", "APPROVAL_UNAVAILABLE")
		} else if present {
			return out, failed("result", "APPROVAL_ALREADY_APPLIED")
		}
		extraction, present, pe := root.ReadSingleLinkFile(extractionPath, intakeread.MaxExtractionBytes)
		if pe != nil || !present {
			return out, failed("source", "APPROVAL_UNAVAILABLE")
		}
		parsed, parseErr := intakeread.ParseExtraction(extraction)
		if parseErr != nil || parsed.Source.ID != id {
			return out, failed("source", "APPROVAL_INVALID")
		}
		review, present, pe := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
		if pe != nil || !present {
			return out, failed("source", "APPROVAL_UNAVAILABLE")
		}
		inputs := approvalInputs{}
		vault := map[string][]byte{}
		for i, path := range append(intakereview.ApprovalPreviewPaths(), catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath) {
			raw, present, pe := root.ReadSingleLinkFile(path, cataloginput.YAMLMaxInputBytes)
			if pe != nil || i < 5 && !present {
				return out, failed("vault", "APPROVAL_UNAVAILABLE")
			}
			inputs[path] = captureObservation{path, raw, present}
			if i < 5 {
				vault[path] = raw
			}
		}
		state, loadErr := catalogstore.ReadState(core, inputs)
		if loadErr != nil {
			return out, failed("catalog", "APPROVAL_UNAVAILABLE")
		}
		plan, planErr := vaultwrite.PlanIntakeApproval(ctx, extraction, review, vault, state, reviewRevision, vaultRevision, registryRevision, s.now())
		if planErr != nil {
			code := planErr.Code
			if !sourceupload.ValidError(code) {
				code = "APPROVAL_INVALID"
			}
			return out, failed("plan", code)
		}
		changes = approvalChanges(plan, inputs, id)
		guards = []catalogtransaction.Guard{{Path: extractionPath, Revision: "sha256:" + digestBytes(extraction)}, {Path: reviewPath, Revision: "sha256:" + digestBytes(review)}}
	}
	if e := s.checkpoint("intake_approval_prepared"); e != nil {
		return out, failed("validation", "APPROVAL_SOURCE_CHANGED")
	}
	var response []byte
	var validationErr *Error
	var sourceWitnesses []captureObservation
	validate := func(ctx context.Context, proposed []catalogtransaction.Change) error {
		if _, e := s.readyMode(root, home, "approval"); e != nil {
			return catalogtransaction.ErrTarget
		}
		if !approvalCoreUnchanged(core) {
			validationErr = failed("catalog", "APPROVAL_SOURCE_CHANGED")
			return catalogtransaction.ErrTarget
		}
		response, validationErr = replanApproval(ctx, root, core, proposed)
		if validationErr != nil {
			return catalogtransaction.ErrTarget
		}
		extractionPath, reviewPath, _ := intakeReviewPaths(proposed[7].RecordID)
		sourceWitnesses = nil
		for _, path := range []string{extractionPath, reviewPath} {
			raw, present, pe := root.ReadSingleLinkFile(path, cataloginput.YAMLMaxInputBytes)
			if pe != nil || !present {
				return catalogtransaction.ErrTarget
			}
			sourceWitnesses = append(sourceWitnesses, captureObservation{path, raw, true})
		}
		if _, e := s.readyMode(root, home, "approval"); e != nil {
			return catalogtransaction.ErrTarget
		}
		changes = proposed
		return nil
	}
	out.CommitAttempted = true
	if recovery {
		out.ApprovalWrite, err = catalogtransaction.RecoverApprovalOwned(ctx, root, home.DataHomeID, validate)
	} else {
		begin := s.approvalBegin
		if begin == nil {
			begin = catalogtransaction.BeginApprovalOwned
		}
		out.ApprovalWrite, err = begin(ctx, root, home.DataHomeID, changes, guards, validate)
	}
	out.Committed = out.ApprovalWrite.FilesComplete || len(out.ApprovalWrite.AppliedFiles) > 0
	pending, pendingErr := root.Exists(catalogtransaction.ApprovalJournalPath)
	out.RecoveryRequired = pending || pendingErr != nil
	out.CleanupFailed = out.ApprovalWrite.FilesComplete && !out.ApprovalWrite.Completed
	if err != nil {
		if !out.ApprovalWrite.IntentRecorded && (errors.Is(err, catalogtransaction.ErrInput) || errors.Is(err, catalogtransaction.ErrTarget) || errors.Is(err, catalogtransaction.ErrConflict) || errors.Is(err, catalogtransaction.ErrPending)) {
			out.CommitAttempted = false
		}
		if validationErr != nil {
			return out, validationErr
		}
		code := "APPROVAL_WRITE_UNCERTAIN"
		if errors.Is(err, catalogtransaction.ErrJournal) {
			code = "APPROVAL_JOURNAL_INVALID"
		}
		if errors.Is(err, catalogtransaction.ErrConflict) {
			code = "APPROVAL_SOURCE_CHANGED"
		}
		if errors.Is(err, catalogtransaction.ErrTarget) || errors.Is(err, catalogtransaction.ErrInput) {
			code = "APPROVAL_TARGET_INVALID"
		}
		if errors.Is(err, catalogtransaction.ErrPending) {
			code = "APPROVAL_RECOVERY_REQUIRED"
		}
		return out, failed("transaction", code)
	}
	if recovery && !out.ApprovalWrite.IntentRecorded {
		out.CommitAttempted = false
		return out, nil
	}
	if !out.ApprovalWrite.Completed || out.RecoveryRequired {
		return out, failed("verification", "APPROVAL_COMMITTED_UNVERIFIED")
	}
	if e := s.checkpoint("intake_approval_saved"); e != nil {
		return out, failed("verification", "APPROVAL_COMMITTED_UNVERIFIED")
	}
	for _, c := range changes {
		raw, present, pe := root.ReadSingleLinkFile(c.Path, cataloginput.YAMLMaxInputBytes)
		wantPresent := !c.CheckOnly || c.BeforePresent
		if pe != nil || present != wantPresent || !bytes.Equal(raw, c.After) {
			return out, failed("verification", "APPROVAL_COMMITTED_UNVERIFIED")
		}
	}
	for _, v := range sourceWitnesses {
		raw, present, pe := root.ReadSingleLinkFile(v.path, cataloginput.YAMLMaxInputBytes)
		if pe != nil || !present || !bytes.Equal(raw, v.raw) {
			return out, failed("verification", "APPROVAL_COMMITTED_UNVERIFIED")
		}
	}
	if _, e := s.ready(root, home); e != nil || !approvalCoreUnchanged(core) {
		return out, failed("verification", "APPROVAL_COMMITTED_UNVERIFIED")
	}
	out.Result = response
	return out, nil
}
