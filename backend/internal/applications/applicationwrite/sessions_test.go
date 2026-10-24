package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

type sessionStep struct {
	Op, Now, Fixture, Before string
	Input                    map[string]any `json:"resolved_input"`
	IDs                      []string       `json:"generated_ids"`
	Tokens                   []string       `json:"generated_tokens"`
}

type sessionRequest struct {
	Data, Mode string
	Steps      []sessionStep
	OmitTables bool // Large synthetic bounds fixtures are verified by file hash.
}
type sessionOutput struct {
	Outcome             *Outcome
	Error               *Error
	Tables              map[string]table
	IDsUsed, TokensUsed int
}
type fixtureSessionEntropy struct {
	ids, tokens         []string
	idsUsed, tokensUsed int
	broken              bool
}

func (f *fixtureSessionEntropy) ID() (string, error) {
	if f.broken || f.idsUsed >= len(f.ids) {
		return "", errors.New("synthetic entropy unavailable")
	}
	v := f.ids[f.idsUsed]
	f.idsUsed++
	return v, nil
}
func (f *fixtureSessionEntropy) Token() (string, error) {
	if f.broken || f.tokensUsed >= len(f.tokens) {
		return "", errors.New("synthetic entropy unavailable")
	}
	v := f.tokens[f.tokensUsed]
	f.tokensUsed++
	return v, nil
}

func sessionTables(t *testing.T, data string) map[string]table {
	t.Helper()
	raw, e := json.Marshal(snapshotTables(t, data))
	if e != nil {
		t.Fatal(e)
	}
	var out map[string]table
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&out) != nil {
		t.Fatal("snapshot framing")
	}
	return out
}
func sessionOperation(ctx context.Context, s *Service, step sessionStep) (*Outcome, *Error) {
	a := step.Input
	cap := SessionCapability{SessionID: a["session_id"], Token: a["session_token"], TabID: a["tab_id"], PageOrigin: a["page_origin"]}
	switch step.Op {
	case "create":
		return s.CreateApplicationSession(ctx, a["job_id"].(string))
	case "latest":
		return s.LatestApplicationSession(ctx, a["job_id"].(string))
	case "reviews":
		return s.SubmissionReviews(ctx)
	case "claim":
		return s.ClaimApplicationSession(ctx, a["request_id"], a["current_url"], a["tab_id"])
	case "claim_create":
		return s.ClaimOrCreateApplicationSession(ctx, a["request_id"], a["current_url"], a["tab_id"])
	case "signal":
		return s.RecordSubmissionSignal(ctx, SubmissionSignal{Capability: cap, EventKey: a["event_key"], SuccessRule: a["success_rule"], SubmitObservedAt: a["submit_observed_at"], SuccessObservedAt: a["success_observed_at"]})
	case "interrupt":
		return s.InterruptApplicationSession(ctx, cap, a["reason"])
	case "confirm":
		return s.ConfirmApplicationSession(ctx, a["session_id"], a["expected_revision"])
	case "dismiss":
		return s.DismissApplicationSession(ctx, a["session_id"], a["expected_revision"])
	case "cancel":
		return s.CancelApplicationSession(ctx, a["session_id"], a["expected_revision"])
	default:
		return emptyFailure(failed("fixture", "UNKNOWN_FIXTURE_OPERATION"))
	}
}
func TestSessionNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_SESSION_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r sessionRequest
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
	out := []sessionOutput{}
	for _, step := range r.Steps {
		now, e := time.Parse(time.RFC3339Nano, step.Now)
		if e != nil {
			t.Fatal("fixture clock")
		}
		entropy := &fixtureSessionEntropy{ids: step.IDs, tokens: step.Tokens, broken: r.Mode == "entropy-error"}
		s := New(r.Data)
		s.sessionClock = func() time.Time { return now }
		s.sessionEntropy = entropy
		ctx, cancel := context.WithCancel(context.Background())
		installFaults(t, s, nativeRequest{Data: r.Data, Mode: r.Mode}, ctx, cancel)
		prior := s.checkpoint
		s.checkpoint = func(stage string) *Error {
			if p := prior(stage); p != nil {
				return p
			}
			if r.Mode == "crash-"+stage {
				os.Exit(77)
			}
			return nil
		}
		result, p := sessionOperation(ctx, s, step)
		cancel()
		if r.Mode == "unsafe-companion" {
			if os.Remove(filepath.Join(r.Data, filepath.FromSlash(databasePath))+"-journal") != nil {
				t.Fatal("remove synthetic directory")
			}
		}
		tables := map[string]table{}
		if r.Mode != "missing-database" && !r.OmitTables {
			tables = sessionTables(t, r.Data)
		}
		out = append(out, sessionOutput{Outcome: result, Error: p, Tables: tables, IDsUsed: entropy.idsUsed, TokensUsed: entropy.tokensUsed})
	}
	if json.NewEncoder(os.Stdout).Encode(out) != nil {
		t.Fatal("output")
	}
}
