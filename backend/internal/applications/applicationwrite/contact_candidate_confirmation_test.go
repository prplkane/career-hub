//go:build windows

package applicationwrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"careerhub.local/backend/internal/applications/contactcandidate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type candidateConfirmationProcessRequest struct {
	Data, Mode string
	Request    contactcandidate.ConfirmationRequest
}

type candidateConfirmationProcessOutput struct {
	Outcome *Outcome
	Error   *Error
}

func TestContactCandidateConfirmationProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_CONTACT_CANDIDATE_CONFIRMATION_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var request candidateConfirmationProcessRequest
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 256<<10))
	decoder.UseNumber()
	if decoder.Decode(&request) != nil {
		t.Fatal("invalid request")
	}
	root, p := runtimepaths.Open(request.Data)
	if p != nil {
		t.Fatal(p)
	}
	if p = root.ClaimForProcess(); p != nil {
		t.Fatal(p)
	}
	defer root.Close()
	service := New(request.Data)
	service.now = func() string { return "2026-09-20T13:05:00Z" }
	if request.Mode == "before_commit" {
		service.checkpoint = func(stage string) *Error {
			if stage == "before_commit" {
				return failed("fixture", "TEST_PRECOMMIT_STOP")
			}
			return nil
		}
	}
	if request.Mode == "after_commit" {
		service.checkpoint = func(stage string) *Error {
			if stage == "committed" {
				return failed("fixture", "TEST_POSTCOMMIT_STOP")
			}
			return nil
		}
	}
	if request.Mode == "commit_ambiguous" {
		service.commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("synthetic ambiguous commit")
		}
	}
	outcome, problem := service.ConfirmContactCandidate(context.Background(), request.Request)
	if json.NewEncoder(os.Stdout).Encode(candidateConfirmationProcessOutput{outcome, problem}) != nil {
		t.Fatal("encode")
	}
}
