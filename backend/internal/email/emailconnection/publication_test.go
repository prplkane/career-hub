package emailconnection

import (
	"context"
	"testing"

	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/storage/datahome"
)

func TestPublicationRefusesUnownedAndMalformedCalls(t *testing.T) {
	intent := emailcredentials.ScopedIntent{Version: 2, Home: datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "dh_0123456789abcdef0123456789abcdef"}}
	backend := &fakeSecrets{}
	if session, problem := stageCredentialIntent(context.Background(), t.TempDir(), backend, intent); session != nil || problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("unowned publication admitted: %v %v", session, problem)
	}
	if session, problem := stageCredentialIntent(nil, t.TempDir(), backend, intent); session != nil || problem == nil || problem.Code != "EMAIL_CREDENTIAL_PUBLICATION_INVALID" {
		t.Fatalf("nil context admitted: %v %v", session, problem)
	}
	if session, problem := stageCredentialIntent(context.Background(), t.TempDir(), nil, intent); session != nil || problem == nil || problem.Code != "EMAIL_CREDENTIAL_PUBLICATION_INVALID" {
		t.Fatalf("nil backend admitted: %v %v", session, problem)
	}
	intent.After.SecretName = "arbitrary-target"
	if session, problem := stageCredentialIntent(context.Background(), t.TempDir(), backend, intent); session != nil || problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("invalid intent admitted: %v %v", session, problem)
	}
}
