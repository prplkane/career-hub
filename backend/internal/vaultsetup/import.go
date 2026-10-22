// Package vaultsetup creates a complete, explicitly confirmed Career Vault from
// selected files or an empty-profile bundle. It never invents candidate facts,
// restores a whole Data Home or replaces user files.
package vaultsetup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/vaultread"
)

type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }

// Bundle keeps owned bytes immutable after full validation; no public method
// exposes contents or accepts an arbitrary destination path.
type Bundle struct{ files map[string][]byte }

func Prepare(ctx context.Context, files map[string][]byte) (*Bundle, *Error) {
	if problem := vaultread.ValidateSetupFiles(ctx, files); problem != nil {
		return nil, &Error{problem.Code}
	}
	b := &Bundle{files: make(map[string][]byte, len(files))}
	for path, raw := range files {
		b.files[path] = bytes.Clone(raw)
	}
	return b, nil
}

type Plan struct {
	ContractVersion int      `json:"contract_version"`
	PlanID          string   `json:"plan_id"`
	Create          []string `json:"create"`
	Retain          []string `json:"retain"`
}
type Outcome struct {
	Plan          *Plan `json:"plan,omitempty"`
	Created       int   `json:"created"`
	Complete      bool  `json:"complete"`
	Committed     bool  `json:"committed"`
	CommitUnknown bool  `json:"commit_unknown"`
	CleanupFailed bool  `json:"cleanup_failed"`
}
type Service struct {
	root   string
	home   datahome.Info
	read   func(*runtimepaths.Root, string, int64) ([]byte, bool, *runtimepaths.Error)
	create func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
	close  func(*runtimepaths.Root) error
}

func New(root string, expected datahome.Info) *Service {
	return &Service{root: root, home: expected, read: (*runtimepaths.Root).ReadFile, create: (*runtimepaths.Root).CreateFile, close: (*runtimepaths.Root).Close}
}
func (s *Service) Preview(ctx context.Context, bundle *Bundle) (*Outcome, *Error) {
	return s.run(ctx, bundle, "", false)
}
func (s *Service) Apply(ctx context.Context, bundle *Bundle, confirmedPlan string) (*Outcome, *Error) {
	return s.run(ctx, bundle, confirmedPlan, true)
}

func (s *Service) admit(root *runtimepaths.Root) *Error {
	// Existing admission requires the process lease and bypasses only registry
	// recovery. Explicitly refuse that sentinel too; this importer recovers none.
	info, problem := datahome.InspectOwnedForRegistryRecovery(root, nil)
	if problem != nil {
		return &Error{problem.Code}
	}
	if info == nil || *info != s.home {
		return &Error{"VAULT_SETUP_HOME_CHANGED"}
	}
	pending, err := root.Exists(datahome.RegistryRecoveryPath)
	if err != nil || pending {
		return &Error{"DATA_HOME_RECOVERY_REQUIRED"}
	}
	return nil
}

type observed struct {
	raw     []byte
	present bool
}

func (s *Service) observe(root *runtimepaths.Root, bundle *Bundle) (map[string]observed, *Error) {
	state := map[string]observed{}
	for _, path := range vaultread.SetupPaths() {
		raw, present, problem := s.read(root, path, cataloginput.YAMLMaxInputBytes)
		if problem != nil {
			return nil, &Error{"VAULT_SETUP_DESTINATION_UNAVAILABLE"}
		}
		if present && !bytes.Equal(raw, bundle.files[path]) {
			return nil, &Error{"VAULT_SETUP_FILE_CONFLICT"}
		}
		state[path] = observed{bytes.Clone(raw), present}
	}
	return state, nil
}
func (s *Service) unchanged(ctx context.Context, root *runtimepaths.Root, state map[string]observed) *Error {
	if ctx.Err() != nil {
		return &Error{"VAULT_SETUP_CANCELED"}
	}
	if problem := s.admit(root); problem != nil {
		return problem
	}
	for _, path := range vaultread.SetupPaths() {
		want := state[path]
		raw, present, problem := s.read(root, path, cataloginput.YAMLMaxInputBytes)
		if problem != nil || present != want.present || !bytes.Equal(raw, want.raw) {
			return &Error{"VAULT_SETUP_DESTINATION_CHANGED"}
		}
	}
	// A file read can expose a concurrent marker/root change; recheck before
	// dispatching the next effect, not merely at the start of the loop.
	if ctx.Err() != nil {
		return &Error{"VAULT_SETUP_CANCELED"}
	}
	return s.admit(root)
}
func (s *Service) plan(bundle *Bundle, state map[string]observed) *Plan {
	plan := &Plan{ContractVersion: 1, Create: []string{}, Retain: []string{}}
	hash := sha256.New()
	header, _ := json.Marshal(struct {
		Home      datahome.Info
		Operation string
	}{s.home, "vault-setup-import-v1"})
	hash.Write(header)
	for _, path := range vaultread.SetupPaths() {
		entry := state[path]
		if entry.present {
			plan.Retain = append(plan.Retain, path)
		} else {
			plan.Create = append(plan.Create, path)
		}
		digest := sha256.Sum256(bundle.files[path])
		encoded, _ := json.Marshal(struct {
			Path, Digest string
			Present      bool
		}{path, hex.EncodeToString(digest[:]), entry.present})
		hash.Write(encoded)
	}
	plan.PlanID = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	return plan
}
func (s *Service) run(ctx context.Context, bundle *Bundle, confirmation string, apply bool) (out *Outcome, failure *Error) {
	out = &Outcome{}
	if s == nil || bundle == nil || len(bundle.files) != len(vaultread.SetupPaths()) || ctx == nil || !s.home.Initialized || s.home.DataHomeID == "" || s.read == nil || s.create == nil || s.close == nil {
		return out, &Error{"VAULT_SETUP_INVALID"}
	}
	if apply && (len(confirmation) != 71 || confirmation[:7] != "sha256:") {
		return out, &Error{"VAULT_SETUP_CONFIRMATION_REQUIRED"}
	}
	if ctx.Err() != nil {
		return out, &Error{"VAULT_SETUP_CANCELED"}
	}
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return out, &Error{"VAULT_SETUP_CANCELED"}
	}
	defer release()
	root, pathErr := runtimepaths.Open(s.root)
	if pathErr != nil {
		return out, &Error{"VAULT_SETUP_DESTINATION_UNAVAILABLE"}
	}
	defer func() {
		if s.close(root) != nil {
			out.CleanupFailed = true
			if failure == nil {
				failure = &Error{"VAULT_SETUP_CLOSE_FAILED"}
			}
		}
		if failure != nil {
			out.Plan = nil
			out.Complete = false
		}
	}()
	if problem := s.admit(root); problem != nil {
		return out, problem
	}
	state, problem := s.observe(root, bundle)
	if problem != nil {
		return out, problem
	}
	plan := s.plan(bundle, state)
	if problem = s.unchanged(ctx, root, state); problem != nil {
		return out, problem
	}
	if !apply {
		out.Plan = plan
		return out, nil
	}
	if plan.PlanID != confirmation {
		return out, &Error{"VAULT_SETUP_PLAN_CHANGED"}
	}
	for _, path := range plan.Create {
		if problem = s.unchanged(ctx, root, state); problem != nil {
			return out, problem
		}
		created, createErr := s.create(root, path, bundle.files[path])
		out.Committed = out.Committed || created
		if created {
			out.Created++
			state[path] = observed{bundle.files[path], true}
		}
		if createErr != nil || !created {
			// No rollback or second publication attempt. The guarded filesystem
			// reports confirmed creation separately; later errors remain explicit.
			out.CommitUnknown = true
			return out, &Error{"VAULT_SETUP_PUBLICATION_UNVERIFIED"}
		}
	}
	if problem = s.unchanged(ctx, root, state); problem != nil {
		return out, problem
	}
	out.Plan, out.Complete = plan, true
	return out, nil
}
