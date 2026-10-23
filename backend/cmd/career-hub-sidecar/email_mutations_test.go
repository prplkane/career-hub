package main

import (
	"context"
	"errors"
	"testing"

	"careerhub.local/backend/internal/email/emailconnection"
	"careerhub.local/backend/internal/storage/secretstore"
)

func TestEmailMutationRefusesUnavailableBackendBeforeWrite(t *testing.T) {
	called := false
	mutate := func() (emailconnection.ConnectionMutationResult, *emailconnection.Error) {
		called = true
		return emailconnection.ConnectionMutationResult{Committed: true}, nil
	}
	for _, factory := range []func() (secretstore.Backend, error){
		nil,
		func() (secretstore.Backend, error) { return nil, errors.New("private backend failure") },
	} {
		out, problem := runEmailMutation(context.Background(), nil, "", factory, mutate)
		if called || out == nil || out.Committed || problem == nil || problem.Code != "EMAIL_SECRET_STORE_FAILED" {
			t.Fatalf("backend failure reached native writer or claimed a commit: %+v %+v", out, problem)
		}
	}
}
