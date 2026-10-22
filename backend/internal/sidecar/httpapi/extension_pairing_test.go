package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// unpairedExtensionFixture has extension origins but no pairing yet.
func unpairedExtensionFixture(t *testing.T) (*handler, *memoryPairingStore) {
	t.Helper()
	h := extensionFixture(t)
	store := &memoryPairingStore{}
	h.pairing = nil
	if err := ConfigureExtensionPairing(h, store); err != nil {
		t.Fatal(err)
	}
	return h, store
}

func pairingCode(t *testing.T, h *handler) string {
	t.Helper()
	return response(t, h, request(http.MethodPost, extensionPairingCodePath, h.token), 200)["code"].(string)
}

func pairWith(h *handler, code string) *httptest.ResponseRecorder {
	r := extensionRequest(http.MethodPost, extensionPairPath, "")
	r.Header.Del(ExtensionCredentialHeader)
	r.Body = httpBody(`{"code":"` + code + `"}`)
	r.ContentLength = int64(len(`{"code":"` + code + `"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func bootstrapWith(h *handler, credential string) *httptest.ResponseRecorder {
	r := extensionRequest(http.MethodPost, "/api/session", "")
	if credential == "" {
		r.Header.Del(ExtensionCredentialHeader)
	} else {
		r.Header.Set(ExtensionCredentialHeader, credential)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestUnpairedExtensionCannotBootstrap(t *testing.T) {
	h, _ := unpairedExtensionFixture(t)
	for _, credential := range []string{"", testExtensionCredential(), "not-a-credential"} {
		w := bootstrapWith(h, credential)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "EXTENSION_PAIRING_REQUIRED") ||
			strings.Contains(w.Body.String(), h.extensions.capture) || strings.Contains(w.Body.String(), h.extensions.helper) {
			t.Fatalf("unpaired bootstrap was not refused: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestPairingFlowIssuesACredentialAndStoresOnlyItsHash(t *testing.T) {
	h, store := unpairedExtensionFixture(t)
	code := pairingCode(t, h)
	if len(code) != 9 || code[4] != '-' || strings.Trim(strings.ReplaceAll(code, "-", ""), pairingAlphabet) != "" {
		t.Fatalf("pairing code has the wrong shape: %q", code)
	}
	// The owner may type it in lower case or without the hyphen.
	w := pairWith(h, strings.ToLower(strings.ReplaceAll(code, "-", "")))
	var paired struct{ Credential string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &paired) != nil || !validTokenFormat(paired.Credential) {
		t.Fatalf("pairing failed: %d %s", w.Code, w.Body.String())
	}
	if store.record == nil || store.saves != 1 {
		t.Fatal("pairing was not persisted")
	}
	stored, _ := json.Marshal(store.record)
	if bytes.Contains(stored, []byte(paired.Credential)) {
		t.Fatal("the store holds the credential itself")
	}
	if got := bootstrapWith(h, paired.Credential); got.Code != 200 || !strings.Contains(got.Body.String(), h.extensions.capture) {
		t.Fatalf("paired credential did not bootstrap: %d", got.Code)
	}
	// Single use: the same code cannot pair a second extension.
	if again := pairWith(h, code); again.Code != 403 || strings.Contains(again.Body.String(), "credential") {
		t.Fatal("a pairing code was accepted twice")
	}
	status := response(t, h, request(http.MethodGet, extensionPairingPath, h.token), 200)
	if status["paired"] != true || status["paired_at"] == nil || len(status) != 3 {
		t.Fatalf("status changed or disclosed more than it should: %v", status)
	}
}

func TestPairingCodeExpiresAndLimitsGuesses(t *testing.T) {
	h, _ := unpairedExtensionFixture(t)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	h.pairing.now = func() time.Time { return now }
	code := pairingCode(t, h)
	now = now.Add(pairingCodeLifetime)
	if w := pairWith(h, code); w.Code != 403 || !strings.Contains(w.Body.String(), "EXPIRED") {
		t.Fatalf("an expired code was accepted: %d", w.Code)
	}

	code = pairingCode(t, h)
	for i := 0; i < pairingMaxAttempts; i++ {
		if w := pairWith(h, "WRONG-CODE"); w.Code != 403 {
			t.Fatal("a wrong code was accepted")
		}
	}
	if w := pairWith(h, code); w.Code != 403 {
		t.Fatal("the right code still worked after too many wrong guesses")
	}
}

func TestRevokingPairingLocksTheExtensionOut(t *testing.T) {
	h := extensionFixture(t) // paired with testExtensionCredential
	if bootstrapWith(h, testExtensionCredential()).Code != 200 {
		t.Fatal("fixture is not paired")
	}
	response(t, h, request(http.MethodDelete, extensionPairingPath, h.token), 200)
	if w := bootstrapWith(h, testExtensionCredential()); w.Code != 403 {
		t.Fatalf("a revoked credential still bootstrapped: %d", w.Code)
	}
	if response(t, h, request(http.MethodGet, extensionPairingPath, h.token), 200)["paired"] != false {
		t.Fatal("status still reports paired after revoke")
	}
}

func TestOnlyTheOwnersWindowCanManagePairing(t *testing.T) {
	h, _ := unpairedExtensionFixture(t)
	// No token, and the extension's own scoped tokens, are refused.
	for _, token := range []string{"", h.extensions.capture, h.extensions.helper} {
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			path := extensionPairingCodePath
			if method == http.MethodDelete {
				path = extensionPairingPath
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, extensionRequest(method, path, token))
			if w.Code != 403 {
				t.Fatalf("%s %s with a non-window token: %d", method, path, w.Code)
			}
		}
	}
	// The agent token is refused too.
	app, _, _ := applicationFixture(t)
	agent, _ := AgentToken(app)
	for _, path := range []string{extensionPairingCodePath, extensionPairingPath, extensionPairPath} {
		if agentRoute(http.MethodPost, path) || agentRoute(http.MethodGet, path) || agentRoute(http.MethodDelete, path) {
			t.Fatalf("%s is inside the agent scope", path)
		}
		response(t, app, request(http.MethodPost, path, agent), 403)
	}
	// Redeeming needs the configured extension Origin, not a window token.
	r := request(http.MethodPost, extensionPairPath, h.token)
	r.Body = httpBody(`{"code":"ABCD-EFGH"}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == 200 || strings.Contains(w.Body.String(), "credential") {
		t.Fatal("a non-extension caller redeemed a pairing code")
	}
}

func TestPairingLoadsAnExistingRecordAndFailsClosedOnStoreErrors(t *testing.T) {
	h := extensionFixture(t)
	existing := &memoryPairingStore{record: &PairingRecord{CredentialSHA256: sha256.Sum256(testExtensionCredentialBytes())}}
	h.pairing = nil
	if err := ConfigureExtensionPairing(h, existing); err != nil {
		t.Fatal(err)
	}
	if bootstrapWith(h, testExtensionCredential()).Code != 200 {
		t.Fatal("a stored pairing was not honoured after restart")
	}

	failing, store := unpairedExtensionFixture(t)
	code := pairingCode(t, failing)
	store.failing = true
	if w := pairWith(failing, code); w.Code != 403 || strings.Contains(w.Body.String(), "credential\"") {
		t.Fatalf("a credential was issued without being stored: %d %s", w.Code, w.Body.String())
	}
}

func TestPairingRoutesWhenTheExtensionIsDisabled(t *testing.T) {
	h, _ := fixture(t, func() (map[string]any, error) { return map[string]any{}, nil })
	token := sessionTokenOf(h)
	if status := response(t, h, request(http.MethodGet, extensionPairingPath, token), 200); status["enabled"] != false {
		t.Fatal("disabled extension reported as enabled")
	}
	response(t, h, request(http.MethodPost, extensionPairingCodePath, token), http.StatusConflict)
}

func validTokenFormat(value string) bool {
	return len(value) == 43 && strings.Trim(value, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") == ""
}

func httpBody(s string) *readCloser { return &readCloser{strings.NewReader(s)} }

type readCloser struct{ *strings.Reader }

func (*readCloser) Close() error { return nil }
