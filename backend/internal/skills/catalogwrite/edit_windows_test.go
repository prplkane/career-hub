//go:build windows

package catalogwrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/catalogtransaction"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type editNativeRequest struct {
	Data, Resources, Timestamp, Mode string
	Steps                            []editReferenceStep
}
type editNativeResponse struct {
	Result        any
	Revision      string
	Write         catalogtransaction.Report
	CleanupFailed bool
	Problem       *Error
	Files         map[string]*string
	Snapshot      map[string]any
}

func nativeEditOutcome[T any](out *EditOutcome[T], e *Error) editNativeResponse {
	var result any
	if out.Result != nil {
		result = out.Result
	}
	return editNativeResponse{Result: result, Revision: out.Revision, Write: out.Write,
		CleanupFailed: out.CleanupFailed, Problem: e}
}
func TestEditNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_EDIT_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var request editNativeRequest
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		t.Fatal("request")
	}
	root, e := runtimepaths.Open(request.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if request.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	service := New(request.Resources, request.Data)
	stamp, err := time.Parse(time.RFC3339, request.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		// Test-only edits after the owning service captured its read set.
		switch request.Mode {
		case "changed-private", "changed-core":
			base, path := request.Data, catalogstore.CustomGroupsPath
			if request.Mode == "changed-core" {
				base, path = request.Resources, catalogstore.CoreGroupsPath
			}
			full := filepath.Join(base, filepath.FromSlash(path))
			raw, e := os.ReadFile(full)
			if e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(full, append([]byte("# external edit\n"), raw...), 0600); e != nil {
				t.Fatal(e)
			}
		case "pending-after-read":
			putFixture(t, request.Data, datahome.RegistryRecoveryPath, []byte("unrecognized external intent"))
		}
		return stamp
	}
	if request.Mode == "nil-clock" {
		service.now = nil
	}
	output := []editNativeResponse{}
	for _, step := range request.Steps {
		revision := "unavailable"
		if saved, e := catalogstore.Load(request.Resources, request.Data); e == nil {
			revision = saved.Revision()
		}
		if step.Stale {
			revision = "stale"
		}
		ctx, cancel := context.WithCancel(context.Background())
		release := func() {}
		if request.Mode == "gate" {
			var e error
			release, e = operationgate.Acquire(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			cancel()
		}
		if request.Mode == "cancelled" {
			cancel()
		}
		var result editNativeResponse
		switch editOperation(step.Op) {
		case createGroup:
			result = nativeEditOutcome(service.CreateCustomGroup(ctx, step.Payload, revision))
		case createSkill:
			result = nativeEditOutcome(service.CreateCustomSkill(ctx, step.Payload, revision))
		case updateSkill:
			result = nativeEditOutcome(service.UpdateCustomSkill(ctx, step.ID, step.Payload, revision))
		case archiveSkill:
			result = nativeEditOutcome(service.SetCustomSkillArchived(ctx, step.ID, step.Archived, revision))
		default:
			t.Fatal("unknown native test operation")
		}
		cancel()
		release()
		result.Files = map[string]*string{}
		for _, role := range []string{"custom_groups", "custom_skills"} {
			raw, e := os.ReadFile(filepath.Join(request.Data, filepath.FromSlash(archivePaths[role])))
			if os.IsNotExist(e) {
				result.Files[role] = nil
				continue
			}
			if e != nil {
				t.Fatal(e)
			}
			value := string(raw)
			result.Files[role] = &value
		}
		if saved, e := catalogstore.Load(request.Resources, request.Data); e == nil {
			result.Snapshot = saved.Payload()
		}
		output = append(output, result)
	}
	if json.NewEncoder(os.Stdout).Encode(output) != nil {
		t.Fatal("report")
	}
	os.Exit(0)
}
