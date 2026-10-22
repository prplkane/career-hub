package vaultwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/catalogtransaction"
	"careerhub.local/backend/internal/catalogwrite"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/vaultread"
)

// EvidenceOutcome retains publication/recovery facts on every error. An early
// false Committed is not proof that a previous interrupted call wrote nothing.
type EvidenceOutcome struct {
	Result        json.RawMessage
	Committed     bool
	Write         catalogtransaction.Report
	CleanupFailed bool
}

// EvidenceService accepts only explicit native-owned Data Homes. It opens
// resources lazily: edits without a Skills selection never read the catalogs.
type EvidenceService struct {
	files        *Service
	resourceRoot string
	now          func() time.Time
	begin        func(context.Context, *runtimepaths.Root, string, []catalogtransaction.Change, []catalogtransaction.Guard, catalogtransaction.Validate) (catalogtransaction.Report, error)
	recover      func(context.Context, *runtimepaths.Root, string, catalogtransaction.Validate) (catalogtransaction.Report, error)
}

func NewEvidence(resourceRoot, dataRoot string) *EvidenceService {
	return &EvidenceService{files: New(dataRoot), resourceRoot: resourceRoot, now: time.Now,
		begin: catalogtransaction.BeginEvidenceOwned, recover: catalogtransaction.RecoverEvidenceOwned}
}

type evidenceSession struct {
	service         *EvidenceService
	data, resources *runtimepaths.Root
	home            datahome.Info
	private, core   *readSet
}

func (a *evidenceSession) sameHome(ready bool) bool {
	info, e := datahome.InspectOwnedForEvidenceRecovery(a.data, &a.home.Channel)
	if e != nil || *info != a.home {
		return false
	}
	if ready {
		pending, e := a.data.Exists(datahome.EvidenceRecoveryPath)
		return e == nil && !pending
	}
	return true
}

func (a *evidenceSession) openResources() *Error {
	if a.resources != nil {
		return nil
	}
	root, e := runtimepaths.Open(a.service.resourceRoot)
	if e != nil {
		return &Error{Stage: "resources", Code: e.Code}
	}
	a.resources = root
	a.core = &readSet{a.service.files, root, map[string]observation{}}
	return nil
}

func (s *EvidenceService) run(ctx context.Context, recovery bool, action func(*evidenceSession, *EvidenceOutcome) *Error) (out *EvidenceOutcome, problem *Error) {
	out = &EvidenceOutcome{}
	if s == nil || s.files == nil || s.files.gate == nil || s.files.read == nil || s.files.replace == nil || s.files.close == nil || s.now == nil || s.begin == nil || s.recover == nil || ctx == nil {
		return out, &Error{Stage: "configuration", Code: "VAULT_EDITOR_UNAVAILABLE"}
	}
	select {
	case s.files.gate <- struct{}{}:
		defer func() { <-s.files.gate }()
	case <-ctx.Done():
		return out, canceled()
	}
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return out, canceled()
	}
	defer release()
	if ctx.Err() != nil {
		return out, canceled()
	}
	root, pathErr := runtimepaths.Open(s.files.root)
	if pathErr != nil {
		return out, &Error{Stage: "path", Code: pathErr.Code}
	}
	a := &evidenceSession{service: s, data: root, private: &readSet{s.files, root, map[string]observation{}}}
	defer func() {
		for _, r := range []*runtimepaths.Root{a.resources, a.data} {
			if r != nil && s.files.close(r) != nil {
				out.CleanupFailed = true
				if problem == nil {
					problem = &Error{Stage: "close", Code: "VAULT_EDITOR_CLOSE_FAILED"}
				}
			}
		}
		out.Committed = out.Committed || len(out.Write.AppliedFiles) > 0 || out.Write.FilesComplete
		out.CleanupFailed = out.CleanupFailed || (out.Write.FilesComplete && !out.Write.Completed)
		if problem != nil {
			problem.Committed = out.Committed
			out.Result = nil
		}
	}()
	info, admissionErr := datahome.InspectOwnedForEvidenceRecovery(root, nil)
	if admissionErr != nil {
		return out, &Error{Stage: "admission", Code: admissionErr.Code}
	}
	a.home = *info
	if !recovery && !a.sameHome(true) {
		return out, &Error{Stage: "recovery", Code: "VAULT_EDITOR_RECOVERY_REQUIRED"}
	}
	return out, action(a, out)
}

func (s *EvidenceService) CreateExperience(ctx context.Context, payload map[string]any, expected string) (*EvidenceOutcome, *Error) {
	return s.mutate(ctx, vaultread.ExperienceTarget, "create", "", payload, false, expected)
}
func (s *EvidenceService) UpdateExperience(ctx context.Context, id string, payload map[string]any, expected string) (*EvidenceOutcome, *Error) {
	return s.mutate(ctx, vaultread.ExperienceTarget, "update", id, payload, false, expected)
}
func (s *EvidenceService) SetExperienceArchived(ctx context.Context, id string, archived bool, expected string) (*EvidenceOutcome, *Error) {
	return s.mutate(ctx, vaultread.ExperienceTarget, "archive", id, nil, archived, expected)
}
func (s *EvidenceService) CreateProject(ctx context.Context, payload map[string]any, expected string) (*EvidenceOutcome, *Error) {
	return s.mutate(ctx, vaultread.ProjectTarget, "create", "", payload, false, expected)
}
func (s *EvidenceService) UpdateProject(ctx context.Context, id string, payload map[string]any, expected string) (*EvidenceOutcome, *Error) {
	return s.mutate(ctx, vaultread.ProjectTarget, "update", id, payload, false, expected)
}
func (s *EvidenceService) SetProjectArchived(ctx context.Context, id string, archived bool, expected string) (*EvidenceOutcome, *Error) {
	return s.mutate(ctx, vaultread.ProjectTarget, "archive", id, nil, archived, expected)
}

func evidenceConflict(target vaultread.EditTarget, during bool) *Error {
	label, _ := evidenceLabel(target)
	message := "Career Vault changed after the " + label + " editor loaded."
	if during {
		message = "Career Vault changed while the " + label + " record was being validated."
	}
	return &Error{Stage: "revision", Code: "VAULT_EDITOR_REVISION_CONFLICT", SourceClass: "VaultEditorRevisionConflict", SourceMessage: message}
}

func (s *EvidenceService) mutate(ctx context.Context, target vaultread.EditTarget, op, id string, payload map[string]any, archived bool, expected string) (*EvidenceOutcome, *Error) {
	if len(id) > maxPayloadBytes || !utf8.ValidString(id) {
		return &EvidenceOutcome{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_UNSUPPORTED"}
	}
	if op != "create" && !validEvidenceID(id) {
		return &EvidenceOutcome{}, evidenceMissing(target, nil)
	}
	return s.run(ctx, false, func(a *evidenceSession, out *EvidenceOutcome) *Error {
		path, ok := target.Path()
		if !ok {
			return &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
		}
		before, present, pathErr := s.files.read(a.data, path, cataloginput.YAMLMaxInputBytes)
		if pathErr != nil || !present {
			return &Error{Stage: "revision", Code: "VAULT_EDITOR_TARGET_UNAVAILABLE"}
		}
		if revision(before) != expected {
			return evidenceConflict(target, false)
		}
		var normalized cataloginput.Value
		var problem *Error
		if op != "archive" {
			normalized, problem = normalizeEvidence(target, payload)
			if problem != nil {
				return problem
			}
		}
		state, e := vaultread.ReadEditorState(ctx, a.private)
		if e != nil {
			return vaultFailure("load", e)
		}
		if loaded, _ := state.Revision(target); loaded != expected {
			return evidenceConflict(target, true)
		}
		original, _ := state.Record(target)
		if op == "create" {
			existing, e := state.CreationIDs(ctx, target)
			if e != nil {
				return vaultFailure("identity", e)
			}
			id, problem = allocateEvidenceID(target, normalized, existing)
			if problem != nil {
				return problem
			}
		}
		plan, problem := planEvidenceRecord(target, original, normalized, op, id, archived)
		if problem != nil {
			return problem
		}
		var skillPlan *catalogwrite.SkillReferencePlan
		if op != "archive" {
			selection, e := parseEvidenceSelection(payload)
			if e != nil {
				return e
			}
			if selection != nil {
				if e := a.openResources(); e != nil {
					return e
				}
				registry, loadErr := catalogstore.ReadState(a.core, a.private)
				if loadErr != nil {
					return catalogFailure(&catalogwrite.Error{Stage: "load", Code: "CATALOG_LOAD_FAILED", Load: loadErr})
				}
				record, planned, e := planEvidenceSkills(ctx, registry, selection, id, s.now().UTC().Format("2006-01-02T15:04:05Z"), plan.Candidate.Items[plan.Index])
				if e != nil {
					return e
				}
				plan.Candidate.Items[plan.Index] = record
				skillPlan = planned
			}
		}
		after, problem := encode(ctx, plan.Candidate)
		if problem != nil {
			return problem
		}
		response, e := state.ValidateRecordReplacement(ctx, target, after, id)
		if e != nil {
			return vaultFailure("validation", e)
		}
		if skillPlan != nil {
			return a.publish(ctx, target, id, after, response, skillPlan, out)
		}
		if !a.private.unchanged() || !a.sameHome(true) {
			return evidenceConflict(target, true)
		}
		if ctx.Err() != nil {
			return canceled()
		}
		out.Committed, pathErr = s.files.replace(a.data, path, before, after)
		if pathErr != nil {
			if !out.Committed && pathErr.Code == "FILE_CONFLICT" {
				return evidenceConflict(target, true)
			}
			out.CleanupFailed = pathErr.Code == "FILE_CLOSE" || pathErr.Code == "FILE_CLEANUP"
			return &Error{Stage: "save", Code: pathErr.Code}
		}
		if !out.Committed {
			return &Error{Stage: "save", Code: "VAULT_EDITOR_SAVE_UNCONFIRMED"}
		}
		a.private.files[path] = observation{bytes.Clone(after), true}
		if !a.private.unchanged() || !a.sameHome(true) {
			return &Error{Stage: "readback", Code: "VAULT_EDITOR_CHANGED_AFTER_SAVE"}
		}
		out.Result = bytes.Clone(response)
		return nil
	})
}

type evidenceOverlay struct {
	base    *readSet
	changes []catalogtransaction.Change
}

func (r evidenceOverlay) Check(path string) *runtimepaths.Error { return r.base.Check(path) }
func (r evidenceOverlay) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	for _, change := range r.changes {
		if change.Path == path {
			return bytes.Clone(change.After), !change.CheckOnly || change.BeforePresent, nil
		}
	}
	return r.base.ReadFile(path, limit)
}

var evidenceVaultPaths = []string{
	"career_vault/profile.yaml", "career_vault/education.yaml", "career_vault/visa_profile.yaml",
	"career_vault/experience_blocks.yaml", "career_vault/project_blocks.yaml", "career_vault/skill_blocks.yaml",
	"career_vault/proof_blocks.yaml", "career_vault/style_rules.yaml",
}

func evidenceMetadata(raw []byte, revision string, created *[]string) (json.RawMessage, *Error) {
	metadata, err := json.Marshal(struct {
		RegistryRevision string    `json:"registry_revision"`
		CreatedSkillIDs  *[]string `json:"created_skill_ids,omitempty"`
	}{revision, created})
	if err != nil || len(raw) < 2 || raw[len(raw)-1] != '}' {
		return nil, &Error{Stage: "response", Code: "VAULT_RESPONSE_UNSUPPORTED"}
	}
	if len(raw)+len(metadata)-1 > vaultread.MaxProfileBytes {
		return nil, &Error{Stage: "response", Code: "VAULT_RESPONSE_LIMIT"}
	}
	out := make([]byte, 0, len(raw)+len(metadata)-1)
	out = append(out, raw[:len(raw)-1]...)
	out = append(out, ',')
	out = append(out, metadata[1:]...)
	return out, nil
}

func (a *evidenceSession) publish(ctx context.Context, target vaultread.EditTarget, id string, after, response []byte, plan *catalogwrite.SkillReferencePlan, out *EvidenceOutcome) *Error {
	path, _ := target.Path()
	if !a.private.unchanged() || !a.core.unchanged() || !a.sameHome(true) {
		return evidenceConflict(target, true)
	}
	before := a.private.files[path]
	changes := []catalogtransaction.Change{{Path: path, RecordID: id, BeforePresent: before.present, Before: before.raw, After: after}}
	replacements := plan.CatalogReplacements()
	for _, path := range []string{catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath} {
		before := a.private.files[path]
		after, changed := replacements[path]
		if !changed {
			after = before.raw
		}
		changes = append(changes, catalogtransaction.Change{Path: path, BeforePresent: before.present, Before: before.raw, After: after, CheckOnly: !changed})
	}
	guards := make([]catalogtransaction.Guard, 0, 7)
	for _, companion := range evidenceVaultPaths {
		if companion != path {
			guards = append(guards, catalogtransaction.Guard{Path: companion, Revision: revision(a.private.files[companion].raw)})
		}
	}
	result := plan.Result()
	created := []string{}
	for _, skill := range result.CreatedSkills {
		created = append(created, skill.ID)
	}
	expected, problem := evidenceMetadata(response, result.Revision, &created)
	if problem != nil {
		return problem
	}
	validate := func(ctx context.Context, changes []catalogtransaction.Change) error {
		actual, e := a.validateTarget(ctx, changes, &created)
		if e != nil {
			problem = e
			return catalogtransaction.ErrTarget
		}
		if !bytes.Equal(actual, expected) {
			return catalogtransaction.ErrTarget
		}
		return nil
	}
	var err error
	out.Write, err = a.service.begin(ctx, a.data, a.home.DataHomeID, changes, guards, validate)
	if err != nil {
		if problem != nil {
			return problem
		}
		return evidenceTransactionError(err)
	}
	if !out.Write.Completed {
		return &Error{Stage: "save", Code: "VAULT_EDITOR_SAVE_UNCONFIRMED"}
	}
	for _, change := range changes {
		a.private.files[change.Path] = observation{bytes.Clone(change.After), !change.CheckOnly || change.BeforePresent}
	}
	if !a.private.unchanged() || !a.core.unchanged() || !a.sameHome(true) {
		return &Error{Stage: "readback", Code: "VAULT_EDITOR_CHANGED_AFTER_SAVE"}
	}
	out.Result = expected
	return nil
}

func (a *evidenceSession) validateTarget(ctx context.Context, changes []catalogtransaction.Change, created *[]string) (json.RawMessage, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, canceled()
	}
	if !a.sameHome(false) {
		return nil, &Error{Stage: "admission", Code: "VAULT_EDITOR_HOME_CHANGED"}
	}
	if len(changes) != 3 {
		return nil, &Error{Stage: "transaction", Code: "VAULT_EDITOR_TRANSACTION_INVALID"}
	}
	if e := a.openResources(); e != nil {
		return nil, e
	}
	if !a.core.unchanged() {
		return nil, &Error{Stage: "resources", Code: "VAULT_EDITOR_RESOURCES_CHANGED"}
	}
	target := vaultread.ExperienceTarget
	if path, _ := vaultread.ProjectTarget.Path(); changes[0].Path == path {
		target = vaultread.ProjectTarget
	}
	overlay := evidenceOverlay{a.private, changes}
	state, e := vaultread.ReadEditorState(ctx, overlay)
	if e != nil {
		return nil, vaultFailure("validation", e)
	}
	response, e := state.ValidateRecordReplacement(ctx, target, changes[0].After, changes[0].RecordID)
	if e != nil {
		return nil, vaultFailure("validation", e)
	}
	// Capture core observations on recovery; on normal save preserve the first
	// read set and compare current reads without replacing those observations.
	core := a.core
	if len(core.files) != 0 {
		core = &readSet{a.service.files, a.resources, map[string]observation{}}
	}
	registry, loadErr := catalogstore.ReadState(core, overlay)
	if loadErr != nil {
		return nil, catalogFailure(&catalogwrite.Error{Stage: "validation", Code: "CATALOG_LOAD_FAILED", Load: loadErr})
	}
	before, inputErr := cataloginput.ParseYAML(changes[0].Before)
	if inputErr != nil {
		return nil, &Error{Stage: "transaction", Code: "VAULT_EDITOR_TRANSACTION_INVALID"}
	}
	candidate, _ := state.Record(target)
	if problem := validateEvidenceSkillLinks(before, candidate, changes[0].RecordID, registry); problem != nil {
		return nil, problem
	}
	if !a.core.unchanged() || !core.unchanged() || !a.sameHome(false) {
		return nil, &Error{Stage: "validation", Code: "VAULT_EDITOR_CONTEXT_CHANGED"}
	}
	return evidenceMetadata(response, registry.Snapshot.Revision(), created)
}

// RecoverEvidence explicitly rolls a pending record/catalog save forward. It
// returns the recovered record and current registry revision, not fabricated
// created_skill_ids from an earlier caller. Refresh the editor after recovery.
func (s *EvidenceService) RecoverEvidence(ctx context.Context) (*EvidenceOutcome, *Error) {
	return s.run(ctx, true, func(a *evidenceSession, out *EvidenceOutcome) *Error {
		var response json.RawMessage
		var problem *Error
		var intended []catalogtransaction.Change
		validate := func(ctx context.Context, changes []catalogtransaction.Change) error {
			var e *Error
			response, e = a.validateTarget(ctx, changes, nil)
			if e != nil {
				problem = e
				return catalogtransaction.ErrTarget
			}
			intended = make([]catalogtransaction.Change, len(changes))
			for i, change := range changes {
				intended[i] = change
				intended[i].After = bytes.Clone(change.After)
			}
			return nil
		}
		var err error
		out.Write, err = s.recover(ctx, a.data, a.home.DataHomeID, validate)
		if err != nil {
			if problem != nil {
				return problem
			}
			return evidenceTransactionError(err)
		}
		if !out.Write.IntentRecorded {
			return nil
		}
		if !out.Write.Completed || len(intended) != 3 {
			return &Error{Stage: "recovery", Code: "VAULT_EDITOR_SAVE_UNCONFIRMED"}
		}
		for _, change := range intended {
			a.private.files[change.Path] = observation{change.After, !change.CheckOnly || change.BeforePresent}
		}
		if !a.private.unchanged() || !a.core.unchanged() || !a.sameHome(true) {
			return &Error{Stage: "readback", Code: "VAULT_EDITOR_CHANGED_AFTER_SAVE"}
		}
		out.Result = response
		return nil
	})
}

func evidenceTransactionError(err error) *Error {
	code := "VAULT_EDITOR_TRANSACTION_FAILED"
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return canceled()
	case errors.Is(err, catalogtransaction.ErrPending):
		code = "VAULT_EDITOR_RECOVERY_REQUIRED"
	case errors.Is(err, catalogtransaction.ErrConflict):
		code = "VAULT_EDITOR_TRANSACTION_CONFLICT"
	case errors.Is(err, catalogtransaction.ErrJournal):
		code = "VAULT_EDITOR_JOURNAL_INVALID"
	case errors.Is(err, catalogtransaction.ErrTarget):
		code = "VAULT_EDITOR_TARGET_INVALID"
	case errors.Is(err, catalogtransaction.ErrInput):
		code = "VAULT_EDITOR_TRANSACTION_INVALID"
	}
	return &Error{Stage: "transaction", Code: code}
}
