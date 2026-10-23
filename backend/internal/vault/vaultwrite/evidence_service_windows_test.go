//go:build windows

package vaultwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/catalogtransaction"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type evidenceStep struct {
	Target, Op, ID  string
	Payload         map[string]any
	Stale, Archived bool
}

type evidenceNativeRequest struct {
	Data, Resources, Stamp, Mode string
	Paths                        map[string]string
	Steps                        []evidenceStep
}
type evidenceNativeOutput struct {
	Outcome                                  *EvidenceOutcome
	Error                                    *Error
	Files                                    map[string]*string
	CatalogReads, Transactions, Replacements int
	RegistryRevision                         string
}

func evidenceNativeFiles(t *testing.T, r evidenceNativeRequest) map[string]*string {
	t.Helper()
	files := map[string]*string{}
	for role, path := range r.Paths {
		root := r.Data
		if strings.HasPrefix(role, "core_") {
			root = r.Resources
		}
		raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if os.IsNotExist(e) {
			files[role] = nil
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		text := string(raw)
		files[role] = &text
	}
	return files
}
func TestNativeEvidenceProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_EVIDENCE_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var request evidenceNativeRequest
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		t.Fatal("native request")
	}
	root, pathErr := runtimepaths.Open(request.Data)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	defer root.Close()
	if request.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	if request.Mode == "pending" {
		fixtureWrite(t, request.Data, datahome.EvidenceRecoveryPath, []byte("{}"))
	}
	if request.Mode == "foreign-pending" {
		fixtureWrite(t, request.Data, datahome.RegistryRecoveryPath, []byte("{}"))
	}
	outputs := []evidenceNativeOutput{}
	lastID := ""
	for _, step := range request.Steps {
		ctx, cancel := context.WithCancel(context.Background())
		service := NewEvidence(request.Resources, request.Data)
		stamp, e := time.Parse(time.RFC3339, request.Stamp)
		if e != nil {
			t.Fatal("stamp")
		}
		service.now = func() time.Time { return stamp }
		catalogReads, transactions, replacements := 0, 0, 0
		service.files.read = func(r *runtimepaths.Root, path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
			if path == catalogstore.CoreGroupsPath || path == catalogstore.CoreSkillsPath || path == catalogstore.CustomGroupsPath || path == catalogstore.CustomSkillsPath {
				catalogReads++
			}
			return r.ReadFile(path, limit)
		}
		service.files.replace = func(r *runtimepaths.Root, path string, before, after []byte) (bool, *runtimepaths.Error) {
			replacements++
			return r.ReplaceFile(path, before, after)
		}
		service.begin = func(ctx context.Context, r *runtimepaths.Root, homeID string, changes []catalogtransaction.Change, guards []catalogtransaction.Guard, validate catalogtransaction.Validate) (catalogtransaction.Report, error) {
			transactions++
			return catalogtransaction.BeginEvidenceOwned(ctx, r, homeID, changes, guards, validate)
		}
		configureEvidenceFaults(t, request, step, service, cancel)
		if step.Payload["registry_revision"] == "@current" {
			state, e := catalogstore.Load(request.Resources, request.Data)
			step.Payload["registry_revision"] = "unavailable"
			if e == nil {
				step.Payload["registry_revision"] = state.Revision()
			}
		}
		id := step.ID
		if id == "@last" {
			id = lastID
		}
		path := request.Paths["experiences"]
		if step.Target == "project" {
			path = request.Paths["projects"]
		}
		raw, e := os.ReadFile(filepath.Join(request.Data, filepath.FromSlash(path)))
		if e != nil {
			t.Fatal(e)
		}
		expected := revision(raw)
		if step.Stale {
			expected = "stale"
		}
		var out *EvidenceOutcome
		var problem *Error
		switch {
		case step.Op == "recover":
			out, problem = service.RecoverEvidence(ctx)
		case step.Target == "experience" && step.Op == "create":
			out, problem = service.CreateExperience(ctx, step.Payload, expected)
		case step.Target == "experience" && step.Op == "update":
			out, problem = service.UpdateExperience(ctx, id, step.Payload, expected)
		case step.Target == "experience" && step.Op == "archive":
			out, problem = service.SetExperienceArchived(ctx, id, step.Archived, expected)
		case step.Target == "project" && step.Op == "create":
			out, problem = service.CreateProject(ctx, step.Payload, expected)
		case step.Target == "project" && step.Op == "update":
			out, problem = service.UpdateProject(ctx, id, step.Payload, expected)
		case step.Target == "project" && step.Op == "archive":
			out, problem = service.SetProjectArchived(ctx, id, step.Archived, expected)
		default:
			t.Fatal("unsupported synthetic operation")
		}
		if problem == nil && len(out.Result) > 0 {
			var result struct{ Record struct{ ID string } }
			if json.Unmarshal(out.Result, &result) != nil {
				t.Fatal("response")
			}
			lastID = result.Record.ID
		}
		registryRevision := ""
		if problem == nil && bytes.Contains(out.Result, []byte("registry_revision")) {
			observed, loadErr := catalogstore.Load(request.Resources, request.Data)
			if loadErr != nil {
				t.Fatal("saved catalog readback")
			}
			registryRevision = observed.Revision()
		}
		outputs = append(outputs, evidenceNativeOutput{out, problem, evidenceNativeFiles(t, request), catalogReads, transactions, replacements, registryRevision})
		cancel()
	}
	if json.NewEncoder(os.Stdout).Encode(outputs) != nil {
		t.Fatal("response")
	}
}
