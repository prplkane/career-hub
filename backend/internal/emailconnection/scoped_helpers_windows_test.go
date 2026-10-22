//go:build windows

package emailconnection

import (
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/runtimepaths"
)

func testScope(t *testing.T, path string) runtimepaths.CredentialScope {
	t.Helper()
	scope, e := scopeAtPath(path)
	if e != nil {
		t.Fatal(e)
	}
	return scope
}

func testGenerationName(t *testing.T, path string, home datahome.Info) (string, error) {
	t.Helper()
	return emailcredentials.NewScopedGenerationName(home, testScope(t, path))
}

func testDecodeIntent(t *testing.T, path string, raw []byte, home datahome.Info) (emailcredentials.ScopedIntent, error) {
	t.Helper()
	return emailcredentials.DecodeScopedIntent(raw, home, testScope(t, path))
}

func testEncodeIntent(t *testing.T, path string, intent emailcredentials.ScopedIntent) ([]byte, error) {
	t.Helper()
	return emailcredentials.EncodeScopedIntent(intent, testScope(t, path))
}
