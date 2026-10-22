// Package assessmentpolicy owns the existing fixed hard-blocker policy file.
// It does not change candidate facts, assess a job, or discover a Data Home.
package assessmentpolicy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/discoverypreview"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

const Path = "career_vault/hard_blocker_policy.yaml"
const MaxBytes = 16384

var classes = [...]string{"citizenship_work_authorization", "active_clearance", "minimum_experience_years"}

type Error struct{ Code string }

func (*Error) Error() string                { return "The assessment policy could not be read or saved." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func failure(code string) *Error            { return &Error{Code: code} }

type Outcome struct {
	Result                   json.RawMessage
	Committed, CleanupFailed bool
}

type Service struct {
	root    string
	read    func(*runtimepaths.Root, string, int64) ([]byte, bool, *runtimepaths.Error)
	create  func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
	replace func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error)
	close   func(*runtimepaths.Root) error
}

func New(root string) *Service {
	return &Service{root, (*runtimepaths.Root).ReadSingleLinkFile, (*runtimepaths.Root).CreateFile, (*runtimepaths.Root).ReplaceFile, (*runtimepaths.Root).Close}
}

func normalized(value any) (map[string]bool, *Error) {
	v, ok := value.(map[string]any)
	if !ok || len(v) != len(classes) {
		return nil, failure("HARD_BLOCKER_POLICY_INVALID")
	}
	result := map[string]bool{}
	for _, name := range classes {
		b, ok := v[name].(bool)
		if !ok {
			return nil, failure("HARD_BLOCKER_POLICY_INVALID")
		}
		result[name] = b
	}
	return result, nil
}

func encode(value map[string]bool) []byte {
	var out bytes.Buffer
	out.WriteString("schema_version: 1\nclasses:\n")
	for _, name := range classes {
		fmt.Fprintf(&out, "  %s: %t\n", name, value[name])
	}
	return out.Bytes()
}

func revision(raw []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) }

func decode(ctx context.Context, raw []byte, present bool) (map[string]bool, []byte, *Error) {
	var document cataloginput.Value
	if len(raw) > MaxBytes {
		return nil, nil, failure("HARD_BLOCKER_POLICY_INVALID")
	}
	if present {
		var e *cataloginput.YAMLError
		document, e = cataloginput.ParseYAML(raw)
		if e != nil {
			return nil, nil, failure("HARD_BLOCKER_POLICY_INVALID")
		}
	}
	v, e := discoverypreview.Policy(ctx, document, present)
	if e != nil {
		return nil, nil, failure("HARD_BLOCKER_POLICY_INVALID")
	}
	result := map[string]bool{}
	for _, entry := range v.Entries {
		result[entry.Key.Text] = entry.Value.Boolean
	}
	if !present {
		raw = encode(result)
	}
	return result, raw, nil
}

func admitted(root *runtimepaths.Root, before *datahome.Info) (*datahome.Info, *Error) {
	info, e := datahome.InspectOwnedForRegistryRecovery(root, nil)
	if e != nil {
		return nil, failure("HARD_BLOCKER_POLICY_UNAVAILABLE")
	}
	pending, pathErr := root.Exists(datahome.RegistryRecoveryPath)
	if pathErr != nil || pending || before != nil && *before != *info {
		return nil, failure("HARD_BLOCKER_POLICY_UNAVAILABLE")
	}
	return info, nil
}

// Run is synchronous through post-publication verification and native cleanup.
// The host must already own the initialized home and stop legacy writers.
func (s *Service) Run(ctx context.Context, save bool, value any, expected string) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || s.read == nil || s.create == nil || s.replace == nil || s.close == nil {
		return out, failure("HARD_BLOCKER_POLICY_UNAVAILABLE")
	}
	if ctx == nil {
		return out, failure("LOCAL_OPERATION_CANCELED")
	}
	if save && expected == "" {
		return out, failure("HARD_BLOCKER_POLICY_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return out, failure("LOCAL_OPERATION_CANCELED")
	}
	defer release()
	root, pathErr := runtimepaths.Open(s.root)
	if pathErr != nil {
		return out, failure("HARD_BLOCKER_POLICY_UNAVAILABLE")
	}
	defer func() {
		if s.close(root) != nil {
			out.CleanupFailed = true
			problem = failure("HARD_BLOCKER_POLICY_SAVE_FAILED")
		}
		if problem != nil {
			out.Result = nil
		}
	}()
	home, problem := admitted(root, nil)
	if problem != nil {
		return out, problem
	}
	raw, present, pathErr := s.read(root, Path, MaxBytes)
	if pathErr != nil {
		return out, failure("HARD_BLOCKER_POLICY_UNAVAILABLE")
	}
	values, current, problem := decode(ctx, raw, present)
	if problem != nil {
		return out, problem
	}
	resultRaw, configured := current, present
	if save {
		if expected != revision(current) {
			return out, failure("HARD_BLOCKER_POLICY_REVISION_CONFLICT")
		}
		values, problem = normalized(value)
		if problem != nil {
			return out, problem
		}
		resultRaw, configured = encode(values), true
	}
	before, beforePresent, pathErr := s.read(root, Path, MaxBytes)
	if pathErr != nil || beforePresent != present || !bytes.Equal(before, raw) {
		return out, failure("HARD_BLOCKER_POLICY_REVISION_CONFLICT")
	}
	if _, problem = admitted(root, home); problem != nil {
		return out, problem
	}
	if ctx.Err() != nil {
		return out, failure("LOCAL_OPERATION_CANCELED")
	}
	if save {
		if present {
			out.Committed, pathErr = s.replace(root, Path, raw, resultRaw)
		} else {
			out.Committed, pathErr = s.create(root, Path, resultRaw)
		}
		if pathErr != nil || !out.Committed {
			if pathErr != nil {
				out.CleanupFailed = pathErr.Code == "FILE_CLOSE" || pathErr.Code == "FILE_CLEANUP"
			}
			return out, failure("HARD_BLOCKER_POLICY_SAVE_FAILED")
		}
		check, exists, err := s.read(root, Path, MaxBytes)
		if err != nil || !exists || !bytes.Equal(check, resultRaw) {
			return out, failure("HARD_BLOCKER_POLICY_SAVE_FAILED")
		}
		if _, problem = admitted(root, home); problem != nil {
			return out, problem
		}
	}
	if ctx.Err() != nil {
		return out, failure("LOCAL_OPERATION_CANCELED")
	}
	out.Result, _ = json.Marshal(map[string]any{"contract_version": 1, "revision": revision(resultRaw), "configured": configured, "classes": values})
	return out, nil
}
