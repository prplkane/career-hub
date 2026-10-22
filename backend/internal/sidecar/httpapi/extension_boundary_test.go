package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const pairedOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"

func extensionFixture(t *testing.T) *handler {
	t.Helper()
	raw, _ := fixture(t, func() (map[string]any, error) { return map[string]any{}, nil })
	h := raw.(*handler)
	seed := append(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)...)
	if err := h.configureExtensions([]string{pairedOrigin + "/", "moz-extension://fictional-uuid", "safari-web-extension://fictional"}, bytes.NewReader(seed)); err != nil {
		t.Fatal("valid explicit extension configuration refused")
	}
	// A paired extension (ADR SEC-250 item 4): the store holds only the hash.
	store := &memoryPairingStore{}
	if err := ConfigureExtensionPairing(h, store); err != nil {
		t.Fatal(err)
	}
	h.pairing.paired = &PairingRecord{CredentialSHA256: sha256.Sum256(testExtensionCredentialBytes())}
	return h
}

func extensionRequest(method, path, token string) *http.Request {
	r := request(method, path, token)
	r.Header.Set("Origin", pairedOrigin)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set(ExtensionCredentialHeader, testExtensionCredential())
	return r
}

func TestExtensionBootstrapAndIsolation(t *testing.T) {
	h := extensionFixture(t)
	for _, tc := range []struct{ path, client, token string }{
		{"/api/session", "extension", h.extensions.capture},
		{extensionHelperSession, "application_helper", h.extensions.helper},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, extensionRequest("POST", tc.path, ""))
		var body map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["client"] != tc.client || body["token"] != tc.token || body["token"] == h.token {
			t.Fatal("purpose bootstrap changed or exposed browser token")
		}
		if w.Header().Get("Access-Control-Allow-Origin") != pairedOrigin || w.Header().Get("Vary") != "Origin" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("bootstrap cache/CORS boundary changed")
		}
		if tc.client == "application_helper" && body["contract_version"] != float64(2) {
			t.Fatal("helper contract changed")
		}
	}
	response(t, h, request("GET", "/api/session", ""), 403)
	response(t, h, request("GET", "/api/session", h.token), 200)
	paths := []string{"/api/discoveries/captures", "/api/application-sessions/claim", "/api/application-sessions/submission-signal", "/api/application-sessions/interrupt", "/api/application-helper/fields", "/api/application-helper/answers/save", "/api/application-helper/answers/delete"}
	for _, path := range paths {
		for _, token := range []string{"", "wrong", h.token, h.extensions.capture, h.extensions.helper} {
			w := httptest.NewRecorder()
			r := extensionRequest("POST", path, token)
			want := extensionPurpose(path) == "capture" && token == h.extensions.capture || extensionPurpose(path) == "helper" && token == h.extensions.helper
			if h.authorize(w, r, path) != want {
				t.Fatalf("purpose isolation changed for %s", path)
			}
		}
	}
	for _, path := range []string{"/api/health", "/api/vault/profile", "/api/documents", "/api/jobs/job/application/status", "/api/application-helper/configuration", "/api/logs", "/"} {
		for _, token := range []string{h.token, h.extensions.capture, h.extensions.helper} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, extensionRequest("POST", path, token))
			if w.Code != 403 {
				t.Fatalf("extension acquired private route %s", path)
			}
		}
	}
	for _, token := range []string{h.extensions.capture, h.extensions.helper} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("POST", "/api/discoveries/captures", token))
		if w.Code != 403 {
			t.Fatal("capability worked without paired origin")
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", h.extensions), h.extensions.capture) {
		t.Fatal("capability formatting leaked")
	}
}

func TestExtensionBoundaryRefusals(t *testing.T) {
	h := extensionFixture(t)
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Host = "example.test:8766" },
		func(r *http.Request) { r.Header.Set("Origin", "chrome-extension://other") },
		func(r *http.Request) { r.Header.Set("Origin", pairedOrigin+"/") },
		func(r *http.Request) { r.Header.Set("Origin", "null") },
		func(r *http.Request) { r.Header.Add("Origin", pairedOrigin) },
		func(r *http.Request) { r.Header.Add(CapabilityHeader, h.extensions.capture) },
		func(r *http.Request) { r.Header.Add("Sec-Fetch-Site", "cross-site") },
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "invalid") },
		func(r *http.Request) { r.Method = "GET" },
		func(r *http.Request) { r.URL.RawQuery = "x=1" },
		func(r *http.Request) { r.URL.ForceQuery = true },
		func(r *http.Request) { r.URL.RawPath = "/api/discoveries/%63aptures" },
		func(r *http.Request) { r.RequestURI = "http://127.0.0.1:8766/api/discoveries/captures" },
		func(r *http.Request) { r.URL.Fragment = "fragment" },
	} {
		r := extensionRequest("POST", "/api/discoveries/captures", h.extensions.capture)
		mutate(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("noncanonical or untrusted request accepted")
		}
	}
	for _, path := range []string{"/api/session", extensionHelperSession} {
		r := extensionRequest("POST", path, "")
		r.ContentLength = 1
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 || strings.Contains(w.Body.String(), h.extensions.capture) || strings.Contains(w.Body.String(), h.extensions.helper) {
			t.Fatal("nonempty bootstrap exposed session")
		}
	}
	raw, _ := fixture(t, func() (map[string]any, error) { return map[string]any{}, nil })
	for _, path := range []string{"/api/session", extensionHelperSession, "/api/discoveries/captures"} {
		w := httptest.NewRecorder()
		raw.ServeHTTP(w, extensionRequest("POST", path, ""))
		if w.Code != 403 {
			t.Fatal("default handler enabled extension")
		}
	}
}

func TestExtensionPreflight(t *testing.T) {
	h := extensionFixture(t)
	for _, tc := range []struct {
		path, method, headers string
		status                int
	}{
		{"/api/discoveries/captures", "POST", "Content-Type, X-Career-Hub-Token", 204},
		{extensionHelperSession, "POST", "", 204},
		{"/api/application-helper/fields", "POST", "x-career-hub-token", 204},
		{"/api/application-helper/answers/save", "POST", "content-type", 204},
		{"/api/session", "POST", "", 204},
		{"/api/session", "POST", "x-career-hub-extension-credential", 204},
		{extensionPairPath, "POST", "content-type", 204},
		{"/api/vault/profile", "POST", "", 403},
		{"/api/discoveries/captures", "DELETE", "", 403},
		{"/api/discoveries/captures", "POST", "authorization", 403},
	} {
		r := extensionRequest("OPTIONS", tc.path, "")
		r.Header.Set("Access-Control-Request-Method", tc.method)
		r.Header.Set("Access-Control-Request-Headers", tc.headers)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("preflight %s got %d", tc.path, w.Code)
		}
		if w.Code == 204 && (w.Body.Len() != 0 || w.Header().Get("Access-Control-Allow-Origin") != pairedOrigin || w.Header().Get("Access-Control-Allow-Credentials") != "") {
			t.Fatal("preflight widened authority")
		}
	}
	for _, name := range []string{"Access-Control-Request-Method", "Access-Control-Request-Headers"} {
		r := extensionRequest("OPTIONS", "/api/discoveries/captures", "")
		r.Header.Set("Access-Control-Request-Method", "POST")
		r.Header.Set("Access-Control-Request-Headers", "content-type")
		r.Header.Add(name, r.Header.Get(name))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("duplicate preflight header accepted")
		}
	}
}

func TestExtensionConfigurationFailsClosed(t *testing.T) {
	for _, origins := range [][]string{nil, {"https://example.test"}, {pairedOrigin + "/path"}, {pairedOrigin + "?x=1"}, {"chrome-extension://user@host"}, {"chrome-extension://host:80"}, {"chrome-extension://"}, {"chrome-extension://é"}, {pairedOrigin + "//"}, {" chrome-extension://host"}, make([]string, 17)} {
		raw, _ := fixture(t, func() (map[string]any, error) { return map[string]any{}, nil })
		h := raw.(*handler)
		if h.configureExtensions(origins, bytes.NewReader(make([]byte, 64))) == nil || h.extensions != nil {
			t.Fatal("invalid configuration changed authority")
		}
	}
	for _, seed := range [][]byte{nil, make([]byte, 63), make([]byte, 64), append(bytes.Repeat([]byte{0x37}, 32), bytes.Repeat([]byte{1}, 32)...)} {
		raw, _ := fixture(t, func() (map[string]any, error) { return map[string]any{}, nil })
		h := raw.(*handler)
		if h.configureExtensions([]string{pairedOrigin}, bytes.NewReader(seed)) == nil || h.extensions != nil {
			t.Fatal("failed entropy or colliding token accepted")
		}
	}
	h := extensionFixture(t)
	before := h.extensions
	if h.configureExtensions([]string{pairedOrigin}, bytes.NewReader(make([]byte, 64))) == nil || h.extensions != before {
		t.Fatal("live reconfiguration accepted")
	}
}
