package agentmaterials

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var syntheticToken = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))

func sessionBody() string {
	return `{"client":"browser","capabilities":{"material_integrity":1}}` // no route returns a token (ADR SEC-250)
}

func clientAt(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	c, e := New(s.URL)
	if e != nil {
		t.Fatal(e.Code)
	}
	c.token = syntheticToken // as NewFromConnection sets it from the owner-only file
	return c, s
}

func TestExplicitLocalOriginOnly(t *testing.T) {
	for _, origin := range []string{"", "https://127.0.0.1:8766", "http://localhost:8766", "http://[::1]:8766", "http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:08766", "http://user@127.0.0.1:8766", "http://127.0.0.1:8766/", "http://127.0.0.1:8766?", "http://127.0.0.1:8766#", "http://example.test:8766", "http://127.0.0.1:8766/private"} {
		if _, e := New(origin); e == nil {
			t.Fatalf("origin accepted: %q", origin)
		}
	}
	for _, port := range []string{"1", "8766", "65535"} {
		c, e := New("http://127.0.0.1:" + port)
		if e != nil {
			t.Fatal(e.Code)
		}
		transport := c.http.Transport.(*http.Transport)
		if transport.Proxy != nil || !transport.DisableKeepAlives || c.http.Timeout != 30*time.Second {
			t.Fatal("local client limits/proxy policy changed")
		}
	}
}

func TestClientRejectsUnsupportedIntentBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	c, _ := clientAt(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	for _, r := range []Request{
		{Command: "confirm-facts", JobID: "plan_job", Kind: "resume"},
		{Command: "record-submission", JobID: "plan_job", Kind: "resume"},
		{Command: "material-context", JobID: "../private", Kind: "resume"},
		{Command: "material-context", JobID: "plan_job", Kind: "unknown"},
		{Command: "material-context", JobID: "plan_job", Kind: "resume", Approve: true},
		{Command: "material-context", JobID: "plan_job", Kind: "resume", FileFormat: "pdf"},
		{Command: "save-material", JobID: "plan_job", Kind: "resume", ExpectedRevision: -1, Payload: []byte(`{}`)},
		{Command: "save-material", JobID: "plan_job", Kind: "resume", Payload: []byte(`null`)},
		{Command: "save-material", JobID: "plan_job", Kind: "resume", Payload: []byte(`{"draft":null}`)},
		{Command: "save-material", JobID: "plan_job", Kind: "resume", Payload: []byte(`{"draft":[]}`)},
		{Command: "save-material", JobID: "plan_job", Kind: "resume", Payload: bytes.Repeat([]byte{'x'}, MaxPayloadBytes+1)},
		{Command: "render-material", JobID: "plan_job", Kind: "resume", FileFormat: "html"},
		{Command: "render-material", JobID: "plan_job", Kind: "resume", FileFormat: "txt", Approve: true},
	} {
		if _, e := c.Run(context.Background(), r); e == nil {
			t.Fatal("unsupported request accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid intent reached owner")
	}
}

func TestSessionAndResponseRefusals(t *testing.T) {
	for _, tc := range []struct{ name, body, contentType string }{
		{"python-capabilities", `{"client":"browser","capabilities":{}}`, "application/json"},
		{"extension-client", strings.Replace(sessionBody(), "browser", "extension", 1), "application/json"},
		{"oversized", `{"padding":"` + strings.Repeat("x", 8193) + `"}`, "application/json"},
		{"malformed", `{"token":`, "application/json"},
		{"invalid-utf8", "{\"x\":\"\xff\"}", "application/json"},
		{"html", sessionBody(), "text/html"},
		{"json-like", sessionBody(), "application/json-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c, _ := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = io.WriteString(w, tc.body)
			})
			if _, e := c.Run(context.Background(), Request{Command: "render-material", JobID: "plan_job", Kind: "resume", FileFormat: "txt"}); e == nil {
				t.Fatal("bad session accepted")
			}
			if calls.Load() != 1 {
				t.Fatal("request made after invalid session")
			}
		})
	}
}

func TestNoRedirectProxyOrUncertainWriteRetry(t *testing.T) {
	var escaped atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1); w.WriteHeader(500) }))
	defer trap.Close()
	t.Setenv("HTTP_PROXY", trap.URL)
	t.Setenv("HTTPS_PROXY", trap.URL)
	t.Setenv("ALL_PROXY", trap.URL)
	t.Setenv("NO_PROXY", "")
	for _, status := range []int{302, 401, 403, 409, 503, 200} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var operations atomic.Int32
			c, _ := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/session" {
					_, _ = io.WriteString(w, sessionBody())
					return
				}
				operations.Add(1)
				w.Header().Set("Location", trap.URL+"/private")
				w.WriteHeader(status)
				if status == 200 {
					_, _ = io.WriteString(w, `{"broken":`)
					return
				}
				_, _ = io.WriteString(w, `{"error_code":"MATERIAL_EXPORT_REFUSED","message":"private-secret","operation_state":{"known":true,"committed":false,"partial":false,"cleanup_failed":false}}`)
			})
			_, e := c.Run(context.Background(), Request{Command: "render-material", JobID: "plan_job", Kind: "resume", FileFormat: "txt"})
			if e == nil || strings.Contains(fmt.Sprintf("%+v", e), "private-secret") {
				t.Fatal("failure lost or disclosed private detail")
			}
			if status == 409 && (!e.State.Known || *e.State.Committed) {
				t.Fatal("verified owner operation state lost")
			}
			if status == 200 && (e.State.Known || e.State.Committed != nil) {
				t.Fatal("invalid response invented rollback")
			}
			if operations.Load() != 1 || escaped.Load() != 0 {
				t.Fatal("mutation retried, redirected or proxied")
			}
		})
	}
}

func TestClientCancellationAndUnknownFailureState(t *testing.T) {
	c, _ := clientAt(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	_, e := c.Run(ctx, Request{Command: "material-context", JobID: "plan_job", Kind: "resume"})
	if e == nil || e.State.Known || e.State.Committed != nil {
		t.Fatal("cancellation invented operation state")
	}
	for _, body := range []string{`null`, `{"error_code":"private-secret"}`, `{"error_code":"MATERIAL_REFUSED","operation_state":{"known":true,"committed":false}}`, `{"operation_state":{"known":false,"committed":false,"partial":false,"cleanup_failed":false}}`} {
		e := apiFailure([]byte(body))
		if e.State.Known || e.State.Committed != nil || strings.Contains(e.Code, "private-secret") {
			t.Fatal("unverified state/details accepted")
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", Request{Payload: []byte("private-secret"), JobID: "private-secret"}), "private-secret") {
		t.Fatal("request logging disclosed payload")
	}
}

func exportFixture() map[string]any {
	id := "material_" + strings.Repeat("a", 64)
	prefix := "outputs/application_materials/plan_job/exports/v1/" + id + "/"
	return map[string]any{"job_id": "plan_job", "kind": "resume", "format": "txt", "revision": 1, "filename": "Sample_Resume.txt", "artifact_id": id, "artifact_path": prefix + "resume.txt", "manifest_path": prefix + "manifest.json", "created_at": "2026-09-12T12:00:00Z", "size_bytes": 100, "sha256": strings.Repeat("b", 64), "provenance": "versioned", "validation": map[string]any{"status": "pass", "findings": []any{}}, "pagination": map[string]any{"target_pages": 1, "page_count": nil, "page_count_unavailable": "plain_text", "page_budget": "not_applicable"}}
}

func TestRenderReceiptPinsReturnedArtifact(t *testing.T) {
	v := exportFixture()
	raw, _ := json.Marshal(v)
	got, e := RenderReceipt("plan_job", "resume", "txt", raw)
	if e != nil {
		t.Fatal(e.Code)
	}
	receipt := decode(t, got).(map[string]any)
	if receipt["result"] != "rendered" || receipt["provenance"] != "versioned" || receipt["artifact_id"] != v["artifact_id"] {
		t.Fatal("export identity lost")
	}
	for key, bad := range map[string]any{"job_id": "other", "kind": "cover_letter", "format": "pdf", "revision": 0, "filename": "../secret.txt", "artifact_id": "material_bad", "artifact_path": "outputs/elsewhere/resume.txt", "manifest_path": "../manifest.json", "created_at": "unknown", "size_bytes": 0, "sha256": "bad", "provenance": "legacy_unverified", "validation": map[string]any{"status": "blocked"}, "pagination": nil} {
		t.Run(key, func(t *testing.T) {
			v := exportFixture()
			v[key] = bad
			raw, _ := json.Marshal(v)
			if _, e := RenderReceipt("plan_job", "resume", "txt", raw); e == nil {
				t.Fatal("mismatched/incomplete export accepted")
			}
		})
	}
	var exports atomic.Int32
	c, _ := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/session" {
			_, _ = io.WriteString(w, sessionBody())
			return
		}
		exports.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/materials/resume/exports") || string(body) != `{"format":"txt"}` {
			t.Error("render intent changed")
		}
		_, _ = w.Write(raw)
	})
	result, e := c.Run(context.Background(), Request{Command: "render-material", JobID: "plan_job", Kind: "resume", FileFormat: "txt"})
	if e != nil || !bytes.Equal(got, result) || exports.Load() != 1 {
		t.Fatal("render client result differs")
	}
}

// Without the connection file there is no token, and nothing is sent.
func TestClientWithoutConnectionTokenSendsNothing(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer s.Close()
	c, e := New(s.URL)
	if e != nil {
		t.Fatal(e.Code)
	}
	_, e = c.Run(context.Background(), Request{Command: "material-context", JobID: "plan_job", Kind: "resume"})
	if e == nil || e.Code != "AGENT_CONNECTION_REQUIRED" || calls.Load() != 0 {
		t.Fatalf("tokenless client reached the service: %+v %d", e, calls.Load())
	}
}
