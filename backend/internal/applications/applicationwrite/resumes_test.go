package applicationwrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

type resumeRequest struct {
	Data, Mode string
	Steps      []sessionStep
	OmitState  bool
}
type resumeOutput struct {
	Outcome  *Outcome
	Error    *Error
	State    resumeState
	IDsUsed  int
	IsFile   bool
	ByteHash string
	ByteSize int
}

func TestResumeNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_RESUME_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r resumeRequest
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
	outputs := []resumeOutput{}
	for _, step := range r.Steps {
		entropy := &fixtureSessionEntropy{ids: step.IDs, broken: r.Mode == "entropy-error"}
		s := New(r.Data)
		s.now = func() string { return step.Now }
		s.resumeEntropy = entropy
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
				return failed("fixture", "SYNTHETIC_RESUME_FAILURE")
			}
			if r.Mode == "resume-source-changed" && stage == "before_commit" {
				fixtureFile(t, filepath.Join(r.Data, "applications", "resumes", "library", resumeFixtureID+".txt"), []byte("Changed by a synthetic external writer."))
			}
			if r.Mode == "cancel-"+stage {
				cancel()
			}
			return nil
		}
		var result *ResumeOutcome
		var p *Error
		switch step.Op {
		case "recover":
			var out *Outcome
			out, p = s.RecoverResumeWrites(ctx)
			result = &ResumeOutcome{Outcome: out}
		case "status":
			var out *Outcome
			out, p = s.SetStatus(ctx, step.Input["job_id"].(string), step.Input["status"].(string), step.Input["occurred_at"], "", nil)
			result = &ResumeOutcome{Outcome: out}
		default:
			result, p = s.Resume(ctx, resumeInput(t, step))
		}
		cancel()
		out := resumeOutput{Outcome: result.Outcome, Error: p, IDsUsed: entropy.idsUsed, IsFile: result.IsFile}
		if result.IsFile {
			out.ByteHash = digestBytes(result.Bytes)
			out.ByteSize = len(result.Bytes)
		} else if len(result.Bytes) != 0 {
			t.Fatal("unexpected bytes")
		}
		if !r.OmitState && r.Mode != "missing-database" {
			out.State = resumeState{Tables: sessionTables(t, r.Data), Files: resumeFileState(t, r.Data)}
		}
		outputs = append(outputs, out)
	}
	if json.NewEncoder(os.Stdout).Encode(outputs) != nil {
		t.Fatal("output")
	}
}
