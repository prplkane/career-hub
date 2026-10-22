//go:build windows

package applicationwrite

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"careerhub.local/backend/internal/runtimepaths"
)

type submissionNativeRequest struct {
	Home, Mode string
	Request    RecordSubmissionRequest
}
type submissionNativeOutput struct {
	Outcome *Outcome
	Problem *Error
}

func TestSubmissionNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_SUBMISSION_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r submissionNativeRequest
	if json.NewDecoder(os.Stdin).Decode(&r) != nil {
		t.Fatal("request")
	}
	if r.Mode != "unowned" {
		root, p := runtimepaths.Open(r.Home)
		if p != nil {
			t.Fatal(p)
		}
		if p = root.ClaimForProcess(); p != nil {
			_ = root.Close()
			t.Fatal(p)
		}
		t.Cleanup(func() {
			if e := root.Close(); e != nil {
				t.Error(e)
			}
		})
	}
	s := New(r.Home)
	s.now = func() string { return sessionFixtureNow }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	installFaults(t, s, nativeRequest{Data: r.Home, Mode: r.Mode}, ctx, cancel)
	out, p := s.RecordSubmission(ctx, r.Request)
	if json.NewEncoder(os.Stdout).Encode(submissionNativeOutput{out, p}) != nil {
		t.Fatal("response")
	}
}
