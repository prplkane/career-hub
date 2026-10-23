//go:build windows

package applicationwrite

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestPreparationNativeHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_PREPARATION_NATIVE_HELPER") != "1" {
		return
	}
	var r nativeRequest
	if json.NewDecoder(os.Stdin).Decode(&r) != nil {
		t.Fatal("request")
	}
	if r.Mode != "unowned" {
		root, e := runtimepaths.Open(r.Data)
		if e != nil {
			t.Fatal(e)
		}
		if e = root.ClaimForProcess(); e != nil {
			_ = root.Close()
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if e := root.Close(); e != nil {
				t.Error(e)
			}
		})
	}
	s := New(r.Data)
	s.now = func() string { return sessionFixtureNow }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	installFaults(t, s, r, ctx, cancel)
	if r.Mode == "card-drift" {
		s.checkpoint = func(stage string) *Error {
			if stage == "before_commit" {
				fixtureFile(t, filepath.Join(r.Data, "jobs/normalized/job-one.yaml"), []byte("id: changed\n"))
			}
			return nil
		}
	}
	id := "job-one"
	if r.Mode == "missing" {
		id = "missing"
	}
	var out *Outcome
	var p *Error
	if strings.HasPrefix(r.Mode, "binding-") {
		card, err := os.ReadFile(filepath.Join(r.Data, "jobs/normalized/job-one.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(r.Data, "jobs/incoming/job-one.md"))
		if err != nil {
			t.Fatal(err)
		}
		binding := PreparationSourceBinding{CardSHA256: sha256.Sum256(card), RawSHA256: sha256.Sum256(raw), Status: "captured"}
		switch r.Mode {
		case "binding-card":
			binding.CardSHA256 = sha256.Sum256([]byte("different"))
		case "binding-raw":
			binding.RawSHA256 = sha256.Sum256([]byte("different"))
		case "binding-status":
			binding.Status = "applied"
		case "binding-empty":
			binding.CardSHA256 = [32]byte{}
		}
		out, p = s.PrepareSavedJobAfterIntake(ctx, id, binding)
	} else {
		out, p = s.PrepareSavedJob(ctx, id)
	}
	if json.NewEncoder(os.Stdout).Encode(submissionNativeOutput{out, p}) != nil {
		t.Fatal("response")
	}
}
