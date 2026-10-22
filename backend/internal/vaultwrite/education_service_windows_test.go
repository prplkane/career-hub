//go:build windows

package vaultwrite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

type recordEditNativeRequest struct {
	Data, Mode string
	Paths      map[string]string
	Steps      []recordEditStep
}
type recordEditNativeOutput struct {
	Outcome *Outcome
	Error   *Error
	Files   map[string]*string
	Writes  int
}

// Shared canonical-only process harness. A closed selector makes any accidental
// registry read or write fail the test instead of depending on installed data.
func recordEditRole(target string) string {
	if target == "skill" {
		return "skills"
	}
	if target == "school" || target == "certification" {
		return "education"
	}
	return ""
}

func TestNativeCanonicalRecordProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_RECORD_EDIT_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r recordEditNativeRequest
	if json.NewDecoder(os.Stdin).Decode(&r) != nil {
		t.Fatal("request framing")
	}
	root, err := runtimepaths.Open(r.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if r.Mode != "unowned" {
		if err := root.ClaimForProcess(); err != nil {
			t.Fatal(err)
		}
	}
	if r.Mode == "pending" {
		fixtureWrite(t, r.Data, datahome.EvidenceRecoveryPath, []byte("{}"))
	}
	outputs := []recordEditNativeOutput{}
	last := ""
	for _, step := range r.Steps {
		role := recordEditRole(step.Target)
		if role == "" {
			t.Fatal("unknown canonical target")
		}
		s := New(r.Data)
		ctx, cancel := context.WithCancel(context.Background())
		if r.Mode == "canceled" {
			cancel()
		}
		reads, writes := 0, 0
		s.read = func(root *runtimepaths.Root, path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
			known := false
			for _, p := range r.Paths {
				if path == p {
					known = true
					break
				}
			}
			if !known {
				t.Fatal("non-Vault read", path)
			}
			reads++
			raw, present, err := root.ReadFile(path, limit)
			if r.Mode == "read-error" || r.Mode == "readback-error" && writes > 0 {
				return nil, false, &runtimepaths.Error{Code: "FILE_READ"}
			}
			if reads == 9 && r.Mode == "changed-companion" {
				evidenceAnnotate(t, r.Data, r.Paths["projects"])
			}
			return raw, present, err
		}
		s.replace = func(root *runtimepaths.Root, path string, before, after []byte) (bool, *runtimepaths.Error) {
			writes++
			if path != r.Paths[role] {
				t.Fatal("non-selected canonical write")
			}
			if r.Mode == "write-error" {
				return false, &runtimepaths.Error{Code: "FILE_SYNC"}
			}
			ok, err := root.ReplaceFile(path, before, after)
			if err == nil && ok && r.Mode == "cleanup-error" {
				return true, &runtimepaths.Error{Code: "FILE_CLEANUP"}
			}
			return ok, err
		}
		if r.Mode == "close-error" {
			s.close = func(root *runtimepaths.Root) error {
				_ = root.Close()
				return errors.New("synthetic private close cause")
			}
		}
		raw, err := os.ReadFile(filepath.Join(r.Data, filepath.FromSlash(r.Paths[role])))
		if err != nil {
			t.Fatal(err)
		}
		expected := revision(raw)
		if step.Stale {
			expected = "stale"
		}
		id := step.ID
		if id == "@last" {
			id = last
		}
		var out *Outcome
		var problem *Error
		switch {
		case step.Target == "skill" && step.Op == "create":
			out, problem = s.CreateLegacySkillGroup(ctx, step.Payload, expected)
		case step.Target == "skill" && step.Op == "update":
			out, problem = s.UpdateLegacySkillGroup(ctx, id, step.Payload, expected)
		case step.Target == "skill" && step.Op == "archive":
			out, problem = s.SetLegacySkillGroupArchived(ctx, id, step.Archived, expected)
		case step.Target == "school" && step.Op == "create":
			out, problem = s.CreateSchool(ctx, step.Payload, expected)
		case step.Target == "school" && step.Op == "update":
			out, problem = s.UpdateSchool(ctx, id, step.Payload, expected)
		case step.Target == "school" && step.Op == "archive":
			out, problem = s.SetSchoolArchived(ctx, id, step.Archived, expected)
		case step.Target == "certification" && step.Op == "create":
			out, problem = s.CreateCertification(ctx, step.Payload, expected)
		case step.Target == "certification" && step.Op == "update":
			out, problem = s.UpdateCertification(ctx, id, step.Payload, expected)
		case step.Target == "certification" && step.Op == "archive":
			out, problem = s.SetCertificationArchived(ctx, id, step.Archived, expected)
		default:
			t.Fatal("fixture operation")
		}
		cancel()
		if problem == nil {
			last = evidenceResultRecord(t, out.Result)["id"].(string)
		}
		outputs = append(outputs, recordEditNativeOutput{out, problem, fixtureFiles(t, r.Data, r.Paths), writes})
	}
	if json.NewEncoder(os.Stdout).Encode(outputs) != nil {
		t.Fatal("response framing")
	}
}
