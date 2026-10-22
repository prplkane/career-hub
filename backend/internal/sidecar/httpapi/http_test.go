package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"careerhub.local/backend/internal/vaultread"
)

const port = 8766

func emptyProfile(context.Context, vaultread.View) (json.RawMessage, error) {
	return json.RawMessage(`{"record":{},"revision":"synthetic"}`), nil
}

func fixture(t *testing.T, registry Registry) (http.Handler, string) {
	t.Helper()
	seed := bytes.Repeat([]byte{0x37}, 32)
	h, err := newHandler(port, "0.1.7-dev+test", registry, emptyProfile, emptyDocuments, bytes.NewReader(seed))
	if err != nil {
		t.Fatal("valid handler construction failed")
	}
	return h, base64.RawURLEncoding.EncodeToString(seed)
}

func request(method, target, token string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.Host = "127.0.0.1:8766"
	if token != "" {
		r.Header.Set(CapabilityHeader, token)
	}
	return r
}

func response(t *testing.T, h http.Handler, r *http.Request, status int) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("status %d, want %d", w.Code, status)
	}
	var value map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal("response is not one JSON object")
	}
	if w.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
		w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" ||
		w.Header().Get("Content-Security-Policy") != contentSecurityPolicy ||
		w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
		t.Fatal("source security/content headers changed")
	}
	for key := range w.Header() {
		if strings.HasPrefix(strings.ToLower(key), "access-control-") || key == "Location" || key == "Set-Cookie" {
			t.Fatal("unexpected CORS, redirect, or cookie")
		}
	}
	return value
}

func TestConstructionChecksBeforeRandomRead(t *testing.T) {
	registry := func() (map[string]any, error) { return map[string]any{}, nil }
	for _, tc := range []struct {
		port     int
		version  string
		registry Registry
	}{
		{0, "1", registry}, {-1, "1", registry}, {65536, "1", registry},
		{1, "", registry}, {1, strings.Repeat("1", 129), registry}, {1, "version\nsecret", registry},
		{1, "é", registry}, {1, "1.2 3", registry}, {1, "1", nil},
	} {
		h, err := newHandler(tc.port, tc.version, tc.registry, emptyProfile, emptyDocuments, failReader{t: t})
		var typed *Error
		if h != nil || !errors.As(err, &typed) || typed.Code != CodeConfiguration || strings.Contains(err.Error(), "secret") {
			t.Fatal("configuration failure changed")
		}
	}
	for _, random := range []io.Reader{nil, bytes.NewReader(make([]byte, 31)), errorReader{}} {
		h, err := newHandler(1, "1", registry, emptyProfile, emptyDocuments, random)
		var typed *Error
		if h != nil || !errors.As(err, &typed) || typed.Code != CodeToken || strings.Contains(fmt.Sprint(err), "private") {
			t.Fatal("random failure exposed cause or accepted short bytes")
		}
	}
	for _, p := range []int{1, 65535} {
		h, err := newHandler(p, strings.Repeat("a", 128), registry, emptyProfile, emptyDocuments, bytes.NewReader(make([]byte, 32)))
		if h == nil || err != nil {
			t.Fatal("exact valid construction bound rejected")
		}
	}
}

type failReader struct{ t *testing.T }

func (f failReader) Read([]byte) (int, error) {
	f.t.Fatal("invalid configuration read RNG")
	return 0, io.EOF
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("private random failure") }

func TestHealthSessionAndRegistry(t *testing.T) {
	calls := 0
	h, token := fixture(t, func() (map[string]any, error) {
		calls++
		return map[string]any{"groups": []any{}, "skills": []any{}, "revision": "synthetic"}, nil
	})
	for _, host := range []string{"127.0.0.1:8766", "localhost:8766", "LOCALHOST:8766", "[::1]:8766", "\u001c localhost:8766 \u001c"} {
		r := request("GET", "/api/health?ignored=1", "")
		r.Host = host
		value := response(t, h, r, 200)
		if len(value) != 3 || value["status"] != "ok" || value["protocol_version"] != float64(1) || value["app_version"] != "0.1.7-dev+test" {
			t.Fatal("health protocol changed")
		}
	}
	for _, origin := range []string{"", "http://127.0.0.1:8766", "http://localhost:8766", "http://[::1]:8766", " http://localhost:8766\u001c"} {
		r := request("GET", "/api/session", token)
		r.Header.Set("Origin", origin)
		value := response(t, h, r, 200)
		// ADR SEC-250: capabilities only; the token is never in the response.
		if len(value) != 1 || value["client"] != "browser" || value["token"] != nil || len(token) != 43 {
			t.Fatal("session capability contract changed")
		}
	}
	// Without a token the session route is refused like every other /api/ route.
	response(t, h, request("GET", "/api/session", ""), 403)
	if calls != 0 {
		t.Fatal("bootstrap accessed registry")
	}
	for _, fetchSite := range []string{"", "none", "same-site", "same-origin", "cross-site", "unrecognized"} {
		r := request("GET", "/api/skills-registry?ignored=1", token)
		r.Header.Set("Sec-Fetch-Site", fetchSite)
		value := response(t, h, r, 200)
		if len(value) != 3 || value["revision"] != "synthetic" {
			t.Fatal("registry payload changed")
		}
	}
	if calls != 6 {
		t.Fatal("registry invocation count changed")
	}
}

func TestBoundaryRejectionsNeverCallRegistryOrExposeToken(t *testing.T) {
	h, token := fixture(t, func() (map[string]any, error) { t.Fatal("rejected request called registry"); return nil, nil })
	tests := []struct{ name, method, path, host, origin, site, supplied, message string }{
		{"host", "GET", "/api/session", "attacker.example:8766", "", "", "", "Request Host is not an allowed loopback authority"},
		{"wrong port", "GET", "/api/health", "localhost:1", "", "", "", "Request Host is not an allowed loopback authority"},
		{"no port", "GET", "/api/health", "localhost", "", "", "", "Request Host is not an allowed loopback authority"},
		{"health method", "POST", "/api/health", "", "https://bad", "cross-site", "", "Health accepts GET only"},
		{"health origin", "GET", "/api/health", "", "https://bad", "cross-site", "", "Health Origin is not allowed"},
		{"health site", "GET", "/api/health", "", "", " Cross-Site ", "", "Cross-site health requests are not allowed"},
		{"session without token", "GET", "/api/session", "", "", "", "", "A valid local capability token is required"},
		{"session origin", "GET", "/api/session", "", "http://LOCALHOST:8766", "", token, "Request Origin is not allowed for the local UI"},
		{"extension", "POST", "/api/session", "", "chrome-extension://synthetic", "cross-site", "", "A valid local capability token is required"},
		{"null origin", "GET", "/api/session", "", "null", "", token, "Request Origin is not allowed for the local UI"},
		{"session site", "POST", "/api/session", "", "", "cross-site", token, "Cross-site browser mutations are not allowed"},
		{"session method", "POST", "/api/session", "", "", "", "", "A valid local capability token is required"},
		{"missing capability", "GET", "/api/skills-registry", "", "https://bad", "", "", "A valid local capability token is required"},
		{"wrong capability", "GET", "/api/skills-registry", "", "", "", "private-marker", "A valid local capability token is required"},
		{"untrimmed capability", "GET", "/api/skills-registry", "", "", "", " " + token, "A valid local capability token is required"},
		{"browser origin", "GET", "/api/skills-registry", "", "https://bad", "", token, "Request Origin is not allowed for the local UI"},
		{"mutation site", "POST", "/api/skills-registry", "", "", "cross-site", token, "Cross-site browser mutations are not allowed"},
		{"options", "OPTIONS", "/api/session", "", "", "", "", "Preflight target is not allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := request(tc.method, tc.path, tc.supplied)
			if tc.host != "" {
				r.Host = tc.host
			}
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			value := response(t, h, r, 403)
			if len(value) != 7 || value["error_code"] != "LOCAL_API_ACCESS_DENIED" || value["technical_message"] != tc.message || value["retryable"] != false || value["severity"] != "error" || value["summary"] != value["error"] {
				t.Fatal("boundary source error envelope changed")
			}
			encoded, _ := json.Marshal(value)
			if bytes.Contains(encoded, []byte(token)) || bytes.Contains(encoded, []byte("private-marker")) || bytes.Contains(encoded, []byte("https://bad")) {
				t.Fatal("boundary error disclosed request/token")
			}
		})
	}
}

func TestDuplicateSensitiveHeadersRejected(t *testing.T) {
	h, token := fixture(t, func() (map[string]any, error) { t.Fatal("duplicate headers reached registry"); return nil, nil })
	for _, name := range []string{"Origin", "Sec-Fetch-Site", CapabilityHeader, "Host"} {
		for _, mixedCase := range []bool{false, true} {
			r := request("GET", "/api/skills-registry", token)
			if mixedCase {
				r.Header[name] = []string{""}
				r.Header[strings.ToLower(name)] = []string{""}
			} else {
				r.Header[name] = []string{"", ""}
			}
			response(t, h, r, 403)
		}
	}
}

func TestUnsupportedRoutesMethodsAndRawTargets(t *testing.T) {
	h, token := fixture(t, func() (map[string]any, error) { t.Fatal("unavailable route reached registry"); return nil, nil })
	for _, path := range []string{"/", "/index.html", "/api/runtime", "/api/jobs", "/api/skills-registry/", "/api/skills-registry/../session", "/api/%73ession", "/api/session%2f", "/api/session\\", "http://127.0.0.1:8766/api/session", "/api/session?x=" + strings.Repeat("a", MaxRequestTargetBytes)} {
		value := response(t, h, request("GET", path, token), 404)
		if value["error_code"] != "RESOURCE_NOT_FOUND" || value["technical_message"] != "Route not found" || value["retryable"] != true {
			t.Fatal("unavailable route envelope changed")
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD", "TRACE", "CONNECT"} {
		response(t, h, request(method, "/api/skills-registry", token), 404)
	}
	r := request("GET", "/api/session", token)
	r.URL = nil
	response(t, h, r, 404)
	r = request("GET", "/api/session", token)
	r.URL.Fragment = "secret"
	response(t, h, r, 404)
}

func TestRegistryFailuresAreSanitizedAndBounded(t *testing.T) {
	cycle := map[string]any{}
	cycle["cycle"] = cycle
	for _, registry := range []Registry{
		func() (map[string]any, error) { return nil, errors.New("C:/private/data key-secret") },
		func() (map[string]any, error) { panic("private panic key-secret") },
		func() (map[string]any, error) { return nil, nil },
		func() (map[string]any, error) { return map[string]any{"unsupported": make(chan int)}, nil },
		func() (map[string]any, error) { return map[string]any{"unsupported": math.NaN()}, nil },
		func() (map[string]any, error) { return cycle, nil },
		func() (map[string]any, error) {
			return map[string]any{"value": strings.Repeat("x", MaxResponseBytes)}, nil
		},
	} {
		h, token := fixture(t, registry)
		for i := 0; i < 2; i++ {
			value := response(t, h, request("GET", "/api/skills-registry", token), 500)
			if value["error_code"] != "UNEXPECTED_ERROR" || value["technical_message"] != "The local operation could not be completed safely." {
				t.Fatal("registry error envelope changed")
			}
			encoded, _ := json.Marshal(value)
			if bytes.Contains(encoded, []byte("private")) || bytes.Contains(encoded, []byte(token)) || bytes.Contains(encoded, []byte("key-secret")) {
				t.Fatal("registry error disclosed private cause/token")
			}
		}
	}
	for _, size := range []int{MaxResponseBytes, MaxResponseBytes + 1} {
		// {"value":"..."} adds 12 encoded bytes.
		h, token := fixture(t, func() (map[string]any, error) { return map[string]any{"value": strings.Repeat("x", size-12)}, nil })
		status := 200
		if size > MaxResponseBytes {
			status = 500
		}
		response(t, h, request("GET", "/api/skills-registry", token), status)
	}
}

func TestRegistryCallsAreSerialized(t *testing.T) {
	var active, maximum atomic.Int32
	h, token := fixture(t, func() (map[string]any, error) {
		count := active.Add(1)
		maximum.Store(max(maximum.Load(), count))
		defer active.Add(-1)
		return map[string]any{}, nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() { h.ServeHTTP(httptest.NewRecorder(), request("GET", "/api/skills-registry", token)) })
	}
	wg.Wait()
	if maximum.Load() != 1 {
		t.Fatal("concurrent registry callbacks")
	}
}

type failingWriter struct {
	headers          http.Header
	writes, statuses int
}

func (f *failingWriter) Header() http.Header { return f.headers }
func (f *failingWriter) WriteHeader(int)     { f.statuses++ }
func (f *failingWriter) Write([]byte) (int, error) {
	f.writes++
	return 1, errors.New("private network path")
}

func TestResponseWriteFailureIsNotRetried(t *testing.T) {
	h, token := fixture(t, func() (map[string]any, error) { return map[string]any{}, nil })
	for _, path := range []string{"/api/health", "/api/session", "/api/skills-registry", "/api/vault/profile", "/unknown"} {
		w := &failingWriter{headers: make(http.Header)}
		h.ServeHTTP(w, request("GET", path, token))
		if w.writes != 1 || w.statuses != 1 {
			t.Fatal("partial response was retried or replaced")
		}
	}
	w := httptest.NewRecorder()
	sendJSON(w, map[string]any{"invalid": math.Inf(1)})
	if w.Code != 500 {
		t.Fatal("internal encoding failure became success")
	}
}
