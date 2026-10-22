//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/runtimepaths"
)

type discoveryStep struct {
	Op, Now, Fixture, Before, ID string
	Input                        map[string]any `json:"resolved_input"`
}
type discoveryState struct {
	Tables map[string]table
	Files  map[string]string
}

type discoveryRequest struct {
	Data, Mode string
	Steps      []discoveryStep
}
type discoveryOutput struct {
	Outcome *Outcome
	Error   *Error
	State   discoveryState
	Pending bool
}

func discoverySnapshot(t *testing.T, root string) discoveryState {
	out := discoveryState{Tables: snapshotTables(t, root), Files: map[string]string{}}
	base := filepath.Join(root, "jobs", "discovery", "captures")
	e := filepath.WalkDir(base, func(path string, entry fs.DirEntry, e error) error {
		if os.IsNotExist(e) && path == base {
			return nil
		}
		if e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		name, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		out.Files[filepath.ToSlash(name)] = base64.StdEncoding.EncodeToString(raw)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	// Parent snapshots and child responses use the same JSON number carrier;
	// comparing native int64 to decoded json.Number would invent a state change.
	raw, e := json.Marshal(out)
	if e != nil {
		t.Fatal(e)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&out) != nil {
		t.Fatal("snapshot framing")
	}
	return out
}
func TestDiscoveryNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_DISCOVERY_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r discoveryRequest
	d := json.NewDecoder(os.Stdin)
	d.UseNumber()
	if d.Decode(&r) != nil {
		t.Fatal("request")
	}
	root, e := runtimepaths.Open(r.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if r.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	nativeSetup(t, nativeRequest{Data: r.Data, Mode: r.Mode})
	outputs := []discoveryOutput{}
	for _, step := range r.Steps {
		s := New(r.Data)
		s.now = func() string { return step.Now }
		s.newDiscoveryID = func() (string, error) { return step.ID, nil }
		ctx, cancel := context.WithCancel(context.Background())
		installFaults(t, s, nativeRequest{Data: r.Data, Mode: r.Mode}, ctx, cancel)
		prior := s.checkpoint
		s.checkpoint = func(stage string) *Error {
			if e := prior(stage); e != nil {
				return e
			}
			if r.Mode == "crash-"+stage {
				os.Exit(77)
			}
			if r.Mode == "fail-"+stage {
				return failed("fixture", "SYNTHETIC_INTERRUPTION")
			}
			if r.Mode == "promotion-source-drift" && stage == "before_commit" {
				fixtureFile(t, filepath.Join(r.Data, filepath.FromSlash(recoveryPath)), []byte("Changed synthetic JD"))
			}
			return nil
		}
		var out *Outcome
		var problem *Error
		a := step.Input
		switch step.Op {
		case "finalize_promotion":
			id, _ := a["discovery_id"].(string)
			job, _ := a["job_id"].(string)
			action, _ := a["action"].(string)
			out, problem = s.FinalizeDiscoveryPromotion(ctx, id, a["expected_revision"], job, action)
		case "capture":
			out, problem = s.CaptureDiscovery(ctx, discovery.CaptureRequest(a))
		case "delete", "flush":
			out, problem = s.DeleteDiscoveries(ctx, discovery.DeleteRequest{Operation: step.Op, DiscoveryID: a["discovery_id"], ExpectedRevision: a["expected_revision"]})
		case "recover":
			out, problem = s.RecoverDiscoveryWrites(ctx)
		default:
			out, problem = s.EditDiscovery(ctx, discovery.EditRequest{Operation: step.Op, DiscoveryID: a["discovery_id"], JobID: a["job_id"], State: a["state"], Overrides: a["overrides"], ExpectedRevision: a["expected_revision"]})
		}
		cancel()
		_, stat := os.Stat(filepath.Join(r.Data, datahome.DiscoveryRecoveryPath))
		outputs = append(outputs, discoveryOutput{Outcome: out, Error: problem, State: discoverySnapshot(t, r.Data), Pending: stat == nil})
	}
	if json.NewEncoder(os.Stdout).Encode(outputs) != nil {
		t.Fatal("output")
	}
}
