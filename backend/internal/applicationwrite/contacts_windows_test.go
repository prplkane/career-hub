//go:build windows

package applicationwrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/runtimepaths"
)

type contactStep struct {
	Op, Now, Fixture, Before string
	Input                    map[string]any `json:"resolved_input"`
	IDs                      []string       `json:"generated_ids"`
	Order                    []string       `json:"update_order"`
}

type contactRequest struct {
	Data, Mode string
	Steps      []contactStep
	OmitTables bool
}
type contactOutput struct {
	Outcome *Outcome
	Error   *Error
	Tables  map[string]table
	IDsUsed int
}

func TestContactNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_CONTACT_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r contactRequest
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
	out := []contactOutput{}
	for _, step := range r.Steps {
		entropy := &fixtureSessionEntropy{ids: step.IDs, broken: r.Mode == "entropy-error"}
		s := New(r.Data)
		s.now = func() string { return step.Now }
		s.contactEntropy = entropy
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
		result, p := s.contact(ctx, step.Op, step.Input, step.Order)
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
		out = append(out, contactOutput{result, p, tables, entropy.idsUsed})
	}
	if json.NewEncoder(os.Stdout).Encode(out) != nil {
		t.Fatal("output")
	}
}
