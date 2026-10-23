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
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type batchNativeRequest struct {
	Data, Resources, Timestamp, Mode string
	Steps                            []batchReferenceStep
}

func TestBatchNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_BATCH_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var request batchNativeRequest
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		t.Fatal("request")
	}
	root, e := runtimepaths.Open(request.Data)
	if e != nil {
		t.Fatal(e)
	}
	if request.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	service := New(request.Resources, request.Data)
	now, err := time.Parse(time.RFC3339, request.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		if request.Mode == "changed-noop" {
			raw := fileBytes(t, request.Data, catalogstore.CustomGroupsPath)
			putFixture(t, request.Data, catalogstore.CustomGroupsPath, append([]byte("# external change\n"), raw...))
		}
		return now
	}
	results := []editNativeResponse{}
	for _, step := range request.Steps {
		revision := "unavailable"
		if snapshot, e := catalogstore.Load(request.Resources, request.Data); e == nil {
			revision = snapshot.Revision()
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
		switch step.Op {
		case "batch":
			result = nativeEditOutcome(service.CreateCustomSkills(ctx, step.Payload, revision))
		case "references":
			result = nativeEditOutcome(service.CommitRegistrySkillReferences(ctx, referenceDrafts(t, step.References), revision))
		case "default":
			out, e := service.EnsureDefaultCustomGroup(ctx)
			result = nativeEditOutcome(out, e)
			if out.Result != nil {
				result.Result = out.Result.Payload()
			}
		default:
			t.Fatal("unknown test operation")
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
		if snapshot, e := catalogstore.Load(request.Resources, request.Data); e == nil {
			result.Snapshot = snapshot.Payload()
		}
		results = append(results, result)
	}
	if json.NewEncoder(os.Stdout).Encode(results) != nil {
		t.Fatal("response")
	}
	os.Exit(0) // Ownership ends before any old-runtime continuation.
}
