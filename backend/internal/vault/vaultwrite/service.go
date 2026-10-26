package vaultwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/vault/vaultread"
)

type Outcome struct {
	Result        json.RawMessage
	Committed     bool
	CleanupFailed bool
}

// Service never claims, creates or migrates a Data Home. A host must already own
// it and stop legacy writers. Process gates + exact-byte checks exclude
// cooperating writers, not arbitrary same-user processes or the old Python app.
type Service struct {
	root    string
	gate    chan struct{}
	read    func(*runtimepaths.Root, string, int64) ([]byte, bool, *runtimepaths.Error)
	replace func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error)
	close   func(*runtimepaths.Root) error
}

func New(dataRoot string) *Service {
	return &Service{root: dataRoot, gate: make(chan struct{}, 1),
		read: (*runtimepaths.Root).ReadFile, replace: (*runtimepaths.Root).ReplaceFile,
		close: (*runtimepaths.Root).Close}
}
func canceled() *Error { return &Error{Stage: "context", Code: "VAULT_EDITOR_CANCELED"} }
func vaultFailure(stage string, e *vaultread.Error) *Error {
	if e.Code == "VAULT_READ_CANCELED" {
		return canceled()
	}
	return &Error{Stage: stage, Code: e.Code, Vault: e}
}
func revision(raw []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) }
func conflict(target vaultread.EditTarget, during bool) *Error {
	message := "Career Vault changed after the Profile editor loaded."
	if target == vaultread.EligibilityTarget {
		message = "Career Vault changed after the Eligibility editor loaded."
	}
	if _, education := target.EducationCollection(); education {
		message = "Career Vault changed after the Education editor loaded."
	}
	if target == vaultread.LegacySkillTarget {
		message = "Career Vault changed after the Skill Group editor loaded."
	}
	if during {
		message = "Career Vault changed while the Profile was being validated."
		if target == vaultread.EligibilityTarget {
			message = "Career Vault changed while Eligibility facts were being validated."
		}
		if _, education := target.EducationCollection(); education {
			message = "Career Vault changed while the Education record was being validated."
		}
		if target == vaultread.LegacySkillTarget {
			message = "Career Vault changed while the Skill Group was being validated."
		}
	}
	return &Error{Stage: "revision", Code: "VAULT_EDITOR_REVISION_CONFLICT",
		SourceClass: "VaultEditorRevisionConflict", SourceMessage: message}
}
func ready(root *runtimepaths.Root, expected *datahome.Info) (*datahome.Info, *Error) {
	info, e := datahome.InspectOwnedForRegistryRecovery(root, nil)
	if e != nil {
		return nil, &Error{Stage: "admission", Code: e.Code}
	}
	pending, pathErr := root.Exists(datahome.RegistryRecoveryPath)
	if pathErr != nil || pending {
		return nil, &Error{Stage: "recovery", Code: "VAULT_EDITOR_RECOVERY_REQUIRED"}
	}
	if expected != nil && *info != *expected {
		return nil, &Error{Stage: "admission", Code: "VAULT_EDITOR_HOME_CHANGED"}
	}
	return info, nil
}

type observation struct {
	raw     []byte
	present bool
}
type readSet struct {
	service *Service
	root    *runtimepaths.Root
	files   map[string]observation
}

func (s *readSet) Check(path string) *runtimepaths.Error { return s.root.Check(path) }

func (s *readSet) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	raw, present, e := s.service.read(s.root, path, limit)
	if e == nil {
		s.files[path] = observation{bytes.Clone(raw), present}
	}
	return raw, present, e
}
func (s *readSet) unchanged() bool {
	for path, expected := range s.files {
		raw, present, e := s.service.read(s.root, path, cataloginput.YAMLMaxInputBytes)
		if e != nil || present != expected.present || !bytes.Equal(raw, expected.raw) {
			return false
		}
	}
	return true
}

func (s *Service) UpdateProfile(ctx context.Context, id string, payload map[string]any, expectedRevision string) (*Outcome, *Error) {
	return s.update(ctx, vaultread.ProfileTarget, id, payload, expectedRevision)
}
func (s *Service) UpdateEligibility(ctx context.Context, id string, payload map[string]any, expectedRevision string) (*Outcome, *Error) {
	return s.update(ctx, vaultread.EligibilityTarget, id, payload, expectedRevision)
}

func (s *Service) update(ctx context.Context, target vaultread.EditTarget, id string, payload map[string]any, expectedRevision string) (out *Outcome, problem *Error) {
	out = &Outcome{}
	recordID, missing := "profile", "Profile record does not exist"
	if target == vaultread.EligibilityTarget {
		recordID, missing = "eligibility", "Eligibility profile does not exist"
	}
	// Source identity rejection comes before path access, revision and payload.
	if id != recordID {
		return out, &Error{Stage: "identity", Code: "VAULT_EDITOR_RECORD_NOT_FOUND",
			SourceClass: "VaultRecordNotFound", SourceMessage: missing}
	}
	return s.runCanonical(ctx, func(root *runtimepaths.Root, home *datahome.Info, out *Outcome) (*Outcome, *Error) {
		return s.updateProfileCandidate(ctx, root, home, target, payload, expectedRevision, out)
	})
}

// runCanonical owns the common native/gate/close boundary for fixed single-file
// editors. It neither bootstraps a home nor skips a pending recovery sentinel.
func (s *Service) runCanonical(ctx context.Context, action func(*runtimepaths.Root, *datahome.Info, *Outcome) (*Outcome, *Error)) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || s.gate == nil || s.read == nil || s.replace == nil || s.close == nil || action == nil {
		return out, &Error{Stage: "configuration", Code: "VAULT_EDITOR_UNAVAILABLE"}
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
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
	root, pathErr := runtimepaths.Open(s.root)
	if pathErr != nil {
		return out, &Error{Stage: "path", Code: pathErr.Code}
	}
	defer func() {
		if s.close(root) != nil {
			out.CleanupFailed = true
			if problem == nil {
				problem = &Error{Stage: "close", Code: "VAULT_EDITOR_CLOSE_FAILED"}
			}
		}
		if problem != nil {
			problem.Committed = out.Committed
			out.Result = nil
		}
	}()
	home, problem := ready(root, nil)
	if problem != nil {
		return out, problem
	}
	return action(root, home, out)
}

func (s *Service) updateProfileCandidate(ctx context.Context, root *runtimepaths.Root, home *datahome.Info,
	target vaultread.EditTarget, payload map[string]any, expectedRevision string, out *Outcome) (*Outcome, *Error) {
	path, _ := target.Path()
	raw, present, pathErr := s.read(root, path, cataloginput.YAMLMaxInputBytes)
	if pathErr != nil || !present {
		return out, &Error{Stage: "revision", Code: "VAULT_EDITOR_TARGET_UNAVAILABLE"}
	}
	if revision(raw) != expectedRevision {
		return out, conflict(target, false)
	}
	normalized, problem := normalize(target, payload)
	reads := &readSet{s, root, map[string]observation{}}
	state, vaultErr := vaultread.ReadEditorState(ctx, reads)
	if problem != nil {
		// Preserve the legacy payload-error precedence. A valid same-read
		// version-2 profile can opt in to the more expressive unknown inputs;
		// missing/corrupt files never activate this branch.
		if vaultErr != nil || !state.AllowsUnknownFacts() {
			return out, problem
		}
		normalized, problem = normalizeVersioned(target, payload, true)
		if problem != nil {
			return out, problem
		}
	}
	if vaultErr != nil {
		return out, vaultFailure("load", vaultErr)
	}
	loadedRevision, _ := state.Revision(target)
	if loadedRevision != expectedRevision {
		return out, conflict(target, true)
	}
	record, _ := state.Record(target)
	record, problem = merge(target, record, normalized)
	if problem != nil {
		return out, problem
	}
	proposed, problem := encode(ctx, record)
	if problem != nil {
		return out, problem
	}
	response, vaultErr := state.ValidateReplacement(ctx, target, proposed)
	if vaultErr != nil {
		return out, vaultFailure("validation", vaultErr)
	}
	return s.replaceCanonical(ctx, reads, home, target, raw, proposed, response, out)
}

func (s *Service) replaceCanonical(ctx context.Context, reads *readSet, home *datahome.Info,
	target vaultread.EditTarget, raw, proposed, response []byte, out *Outcome) (*Outcome, *Error) {
	root := reads.root
	path, _ := target.Path()
	var problem *Error
	var pathErr *runtimepaths.Error
	if !reads.unchanged() {
		return out, conflict(target, true)
	}
	if _, problem = ready(root, home); problem != nil {
		return out, problem
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	// Source same-state saves rewrite too. One existing file, no catalog/history
	// side effect and no journal/partial multi-file transaction to manufacture.
	out.Committed, pathErr = s.replace(root, path, raw, proposed)
	if pathErr != nil {
		if !out.Committed && pathErr.Code == "FILE_CONFLICT" {
			return out, conflict(target, true)
		}
		out.CleanupFailed = pathErr.Code == "FILE_CLOSE" || pathErr.Code == "FILE_CLEANUP"
		return out, &Error{Stage: "save", Code: pathErr.Code}
	}
	if !out.Committed {
		return out, &Error{Stage: "save", Code: "VAULT_EDITOR_SAVE_UNCONFIRMED"}
	}
	// Do not abandon bounded verification merely because the caller timed out
	// after publication. Never report that a committed write was canceled away.
	reads.files[path] = observation{bytes.Clone(proposed), true}
	if !reads.unchanged() {
		return out, &Error{Stage: "readback", Code: "VAULT_EDITOR_CHANGED_AFTER_SAVE"}
	}
	if _, problem = ready(root, home); problem != nil {
		return out, problem
	}
	out.Result = bytes.Clone(response)
	return out, nil
}
