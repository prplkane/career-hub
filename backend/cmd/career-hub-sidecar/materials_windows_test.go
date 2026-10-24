//go:build windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/materials/materialintegrity"
	"careerhub.local/backend/internal/materials/materialwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/datahome"
)

type materialProcessFixture struct {
	Root, Resources string
	Draft           materials.Request
	ReadOnly        bool
}

type materialTestOutput struct{ bytes.Buffer }

func (b *materialTestOutput) Write(p []byte) (int, error) {
	if len(p) > (16<<20)-b.Len() {
		return 0, fmt.Errorf("synthetic fixture output limit")
	}
	return b.Buffer.Write(p)
}

func materialRunTool(t *testing.T, cmd *exec.Cmd) []byte {
	t.Helper()
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = time.Second
	var out, stderr materialTestOutput
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if e := cmd.Run(); e != nil {
		t.Fatalf("synthetic process failed: %v %.1400s %.1400s", e, out.Bytes(), stderr.Bytes())
	}
	return out.Bytes()
}

func materialFixtureFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
}

type materialWireClient struct {
	url, token string
	client     *http.Client
}

func (c *materialWireClient) call(t *testing.T, method, path string, body any, status int) ([]byte, http.Header) {
	t.Helper()
	var raw []byte
	if body != nil {
		var e error
		raw, e = json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
	}
	r, e := http.NewRequest(method, c.url+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set(httpapi.CapabilityHeader, c.token)
	r.Header.Set("Origin", c.url)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	res, e := c.client.Do(r)
	if e != nil {
		t.Fatal("local HTTP request failed:", e)
	}
	defer res.Body.Close()
	got, e := io.ReadAll(io.LimitReader(res.Body, materials.MaxResponseBytes+1))
	if e != nil || len(got) > materials.MaxResponseBytes {
		t.Fatal("HTTP response bound/read")
	}
	if res.StatusCode != status {
		t.Fatalf("%s %s: got %d want %d: %.1400s", method, path, res.StatusCode, status, got)
	}
	if res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("private response headers lost")
	}
	return got, res.Header
}

// A fresh OS process owns the native Data Home until exit. The helper calls the
// actual CLI/runtime, with real owned pipes and loopback HTTP; no fake handlers,
// material callbacks or storage adapters. The parent bounds and reaps each child.
func TestMaterialHTTPProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_MATERIAL_HTTP_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var f materialProcessFixture
	if json.NewDecoder(io.LimitReader(os.Stdin, 2<<20)).Decode(&f) != nil {
		t.Fatal("fixture input")
	}
	input, parentInput, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	parentOutput, output, e := os.Pipe()
	if e != nil {
		input.Close()
		parentInput.Close()
		t.Fatal(e)
	}
	defer parentOutput.Close()
	mode := "--sidecar-bootstrap"
	if f.ReadOnly {
		mode = "--sidecar"
	}
	exited := make(chan int, 1)
	go func() { exited <- execute([]string{mode}, input, output) }()
	defer func() {
		_ = parentInput.Close() // Actual EOF shutdown; runtime owns input/output.
		select {
		case code := <-exited:
			if code != 0 {
				t.Errorf("runtime exit %d", code)
			}
		case <-time.After(5 * time.Second):
			t.Error("runtime failed to stop; parent will reap this process")
		}
	}()
	if e := protocol.WriteRecord(parentInput, map[string]any{"command": "start", "protocol_version": 1, "port": 0, "channel": "dev", "resource_root": f.Resources, "data_root": f.Root, "session_token": testSessionToken}); e != nil {
		t.Fatal(e)
	}
	ready, e := protocol.ReadRecord(parentOutput)
	if e != nil || ready["event"] != "ready" {
		t.Fatalf("startup failed: %v %v", e, ready)
	}
	port, ok := ready["port"].(json.Number)
	if !ok {
		t.Fatalf("readiness port type %T", ready["port"])
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	c := materialWireClient{url: "http://127.0.0.1:" + string(port), client: &http.Client{Transport: transport, Timeout: 30 * time.Second}}
	c.token = testSessionToken
	session, _ := c.call(t, "GET", "/api/session", nil, 200)
	var auth struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(session, &auth) != nil || auth.Token != "" {
		t.Fatal("private session unavailable or disclosed a token")
	}
	base := "/api/jobs/plan_job/materials/" + strings.ReplaceAll(f.Draft.Kind, "_", "-")
	if f.ReadOnly {
		before := materialTree(t, f.Root)
		c.call(t, "GET", base, nil, 404)
		c.call(t, "POST", base+"/exports", map[string]any{"format": "pdf"}, 404)
		if !reflect.DeepEqual(before, materialTree(t, f.Root)) {
			t.Fatal("default HTTP wrote files")
		}
		return
	}
	materialWireWorkflow(t, &c, f, base)
}

func materialTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	result := map[string][32]byte{}
	e := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		result[filepath.ToSlash(rel)] = sha256.Sum256(raw)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return result
}

func materialWireWorkflow(t *testing.T, c *materialWireClient, f materialProcessFixture, base string) {
	t.Helper()
	before := materialTree(t, f.Root)
	unauthorized := *c
	unauthorized.token = "wrong"
	unauthorized.call(t, "POST", base+"/exports", map[string]any{"format": "pdf"}, 403)
	c.call(t, "GET", base, nil, 200)
	if !reflect.DeepEqual(before, materialTree(t, f.Root)) {
		t.Fatal("editor/unauthorized request mutated home")
	}
	payload := map[string]any{"action": "approve", "expected_revision": f.Draft.ExpectedRevision, "draft": f.Draft.Payload}
	c.call(t, "POST", base, payload, 200)
	saved := materialTree(t, f.Root)
	c.call(t, "POST", base, payload, 409) // stale editor cannot overwrite approval
	if !reflect.DeepEqual(saved, materialTree(t, f.Root)) {
		t.Fatal("conflict mutated saved state")
	}
	reviewRaw, _ := c.call(t, "GET", base+"/reviews", nil, 200)
	var view materialintegrity.ReviewView
	if json.Unmarshal(reviewRaw, &view) != nil {
		t.Fatal("review JSON")
	}
	if !reflect.DeepEqual(saved, materialTree(t, f.Root)) {
		t.Fatal("review inspection wrote a confirmation")
	}
	if len(view.Targets) > 0 {
		c.call(t, "POST", base+"/exports", map[string]any{"format": "pdf"}, 409)
		ids := []string{}
		for _, target := range view.Targets {
			ids = append(ids, target.ID)
		}
		// This explicit synthetic user action is test-only. Production export
		// never confirms findings, and this route is not an agent permission.
		c.call(t, "POST", base+"/reviews", map[string]any{"operation": "confirm", "expected_revision": view.Revision, "review_sha256": view.ReviewSHA256, "target_ids": ids}, 200)
	}
	sources := materialTree(t, f.Root)
	for _, format := range []string{"txt", "docx", "pdf"} {
		raw, _ := c.call(t, "POST", base+"/exports", map[string]any{"format": format}, 200)
		var meta materialwrite.ExportMetadata
		if json.Unmarshal(raw, &meta) != nil || meta.ArtifactID == "" || meta.Provenance != "versioned" || meta.FileFormat != format {
			t.Fatal("export metadata")
		}
		exported := materialTree(t, f.Root)
		for name, hash := range sources {
			if exported[name] != hash {
				t.Fatalf("export changed source %s", name)
			}
		}
		download, headers := c.call(t, "GET", base+"/file?artifact_id="+meta.ArtifactID, nil, 200)
		if fmt.Sprintf("%x", sha256.Sum256(download)) != meta.SHA256 || int64(len(download)) != meta.SizeBytes || headers.Get("X-Career-Hub-Artifact-ID") != meta.ArtifactID || !strings.Contains(headers.Get("Content-Disposition"), meta.Filename) {
			t.Fatal("download bytes/identity/filename")
		}
		legacyURL, _ := c.call(t, "GET", base+"/file?format="+format, nil, 200)
		retry, _ := c.call(t, "POST", base+"/exports", map[string]any{"format": format}, 200)
		if !bytes.Equal(download, legacyURL) || !bytes.Equal(raw, retry) || !reflect.DeepEqual(exported, materialTree(t, f.Root)) {
			t.Fatal("exact export retry/download rewrote artifact or home")
		}
	}
	name := "Synthetic_Resume_Legacy.pdf"
	if f.Draft.Kind == "cover_letter" {
		name = "Synthetic_Cover_Letter_Legacy.pdf"
	}
	materialFixtureFile(t, filepath.Join(f.Root, "outputs/application_materials/plan_job/exports", name), []byte("opaque original Python export"))
	legacyBefore := materialTree(t, f.Root)
	legacy, headers := c.call(t, "GET", base+"/legacy-file?filename="+name, nil, 200)
	if string(legacy) != "opaque original Python export" || headers.Get("X-Career-Hub-Provenance") != "legacy_unverified" || headers.Get("X-Career-Hub-Artifact-ID") != "" || !reflect.DeepEqual(legacyBefore, materialTree(t, f.Root)) {
		t.Fatal("legacy read changed bytes/provenance")
	}
	// Admission is bound to the original home identity, not just an allowed path.
	marker := filepath.Join(f.Root, datahome.MarkerPath)
	raw, e := os.ReadFile(marker)
	if e != nil {
		t.Fatal(e)
	}
	defer materialFixtureFile(t, marker, raw)
	materialFixtureFile(t, marker, bytes.Replace(raw, []byte("0123456789abcdef0123456789abcdef"), []byte("abcdef0123456789abcdef0123456789"), 1))
	drifted := materialTree(t, f.Root)
	c.call(t, "POST", base+"/exports", map[string]any{"format": "pdf"}, 503)
	if !reflect.DeepEqual(drifted, materialTree(t, f.Root)) {
		t.Fatal("changed workspace allowed a mutation")
	}
}
