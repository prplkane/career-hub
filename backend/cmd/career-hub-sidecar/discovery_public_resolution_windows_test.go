//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/diagnosticslog"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/discovery/publicjobresolver"
	"careerhub.local/backend/internal/email/emailhost"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/secretstore"
)

func TestPublicResolutionNative(t *testing.T) {
	if base := os.Getenv("CAREERHUB_PUBLIC_RESOLUTION_TEST_ROOT"); base != "" {
		runPublicResolutionNative(t, base, os.Getenv("CAREERHUB_PUBLIC_RESOLUTION_TEST_MODE"))
		return
	}
	for _, mode := range []string{"success", "override", "stale_before", "stale_after", "archived", "protected", "content_fail"} {
		t.Run(mode, func(t *testing.T) {
			base := newJobHostFixture(t)
			installFreshJobAssessmentFixture(t, base)
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPublicResolutionNative$")
			child.Env = append(os.Environ(), "CAREERHUB_PUBLIC_RESOLUTION_TEST_ROOT="+base, "CAREERHUB_PUBLIC_RESOLUTION_TEST_MODE="+mode)
			child.WaitDelay = time.Second
			if output, err := child.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v %.3500s", mode, err, output)
			}
		})
	}
}

func runPublicResolutionNative(t *testing.T, base, mode string) {
	ctx := context.Background()
	start := protocol.StartRecord{DataRoot: filepath.Join(base, "data"), ResourceRoot: filepath.Join(base, "resources"), Channel: "dev"}
	w, err := readonly.OpenForProcess(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schema.PrepareOwned(ctx, start.DataRoot); err != nil {
		t.Fatal(err)
	}
	home, err := w.StartupHome()
	if err != nil {
		t.Fatal(err)
	}
	owner := applicationwrite.New(start.DataRoot).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
	observedURL := "https://alerts.example.com/redirect/42?token=synthetic-only"
	if mode == "protected" {
		observedURL = "https://linkedin.com/jobs/42"
	}
	created, problem := owner.CaptureDiscovery(ctx, discovery.CaptureRequest{"source_kind": "email_file", "source_channel": "job_alert", "request_fingerprint": "synthetic-public-link", "observed_url": observedURL, "title": "Backend Engineer", "company": "Fictional Labs", "content_scope": "link_only", "source_text": ""})
	if problem != nil {
		t.Fatal(problem)
	}
	var captured struct{ Discovery discovery.Detail }
	if json.Unmarshal(created.Result, &captured) != nil {
		t.Fatal("bad capture")
	}
	id := captured.Discovery.DiscoveryID
	revision := captured.Discovery.Revision
	if mode == "override" {
		edited, p := owner.EditDiscovery(ctx, discovery.EditRequest{Operation: "overrides", DiscoveryID: id, ExpectedRevision: int64(0), Overrides: map[string]any{"title": "My reviewed title", "company": nil, "location": nil}})
		if p != nil {
			t.Fatal(p)
		}
		var detail discovery.Detail
		if json.Unmarshal(edited.Result, &detail) != nil {
			t.Fatal("bad override")
		}
		revision = detail.Revision
	}
	if mode == "archived" {
		edited, p := owner.EditDiscovery(ctx, discovery.EditRequest{Operation: "transition", DiscoveryID: id, State: "dismissed", ExpectedRevision: revision})
		if p != nil {
			t.Fatal(p)
		}
		var detail discovery.Detail
		json.Unmarshal(edited.Result, &detail)
		revision = detail.Revision
	}
	if mode == "stale_before" {
		revision++
	}
	fetches := 0
	fetch := func(ctx context.Context, url string) (*publicjobresolver.HTMLResponse, *publicjobresolver.Error) {
		fetches++
		if url != observedURL {
			t.Errorf("wrong source URL %s", url)
		}
		if mode == "protected" {
			return publicjobresolver.Fetch(ctx, url)
		}
		if mode == "stale_after" {
			_, p := owner.EditDiscovery(ctx, discovery.EditRequest{Operation: "transition", DiscoveryID: id, State: "shortlisted", ExpectedRevision: revision})
			if p != nil {
				t.Error(p)
			}
		}
		body := "<section itemprop=description><h2>Responsibilities</h2><p>" + reanalysisCoordinatorSource + "</p><h2>Requirements</h2><p>Engineering experience and collaborative delivery skills.</p></section>"
		if mode == "content_fail" {
			body = "<html>Sign in</html>"
		}
		return &publicjobresolver.HTMLResponse{FinalURL: "https://careers.example.com/jobs/42", Body: []byte(body), Charset: "utf-8", BytesRead: len(body), RedirectCount: 1}, nil
	}
	start.SessionToken = testSessionToken
	h, err := preparedApplicationWithPublicFetcher(w, start, secretstore.NewWindowsBackend, emailhost.Run, fetch).Handler(8766)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/session", nil)
	r.Header.Set(httpapi.CapabilityHeader, testSessionToken)
	r.Host = "127.0.0.1:8766"
	response := httptest.NewRecorder()
	h.ServeHTTP(response, r)
	var session struct{ Token string }
	session.Token = testSessionToken // the response no longer carries it
	if json.Unmarshal(response.Body.Bytes(), &session) != nil || session.Token == "" {
		t.Fatal("missing capability")
	}
	post := func(want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"contract_version": 1, "expected_revision": revision})
		r := httptest.NewRequest("POST", "/api/discoveries/"+id+"/resolve-public-link", bytes.NewReader(raw))
		r.Host = "127.0.0.1:8766"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(httpapi.CapabilityHeader, session.Token)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, r)
		var result map[string]any
		if response.Code != want || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("HTTP=%d %.1800s", response.Code, response.Body.String())
		}
		runID, _ := result["run_id"].(string)
		if runID == "" {
			t.Fatal("missing resolver diagnostic")
		}
		page, p := diagnosticslog.ReadPage(ctx, start.DataRoot, home, "")
		if p != nil {
			t.Fatal(p)
		}
		found := false
		for _, event := range page.Events {
			var v map[string]any
			json.Unmarshal(event, &v)
			if v["run_id"] == runID {
				found = true
				if v["component"] != "public_job_resolution" {
					t.Fatal("wrong diagnostic", v)
				}
				if strings.Contains(string(event), "synthetic-only") || strings.Contains(string(event), "careers.example") {
					t.Fatal("diagnostic leaked URL")
				}
			}
		}
		if !found {
			t.Fatal("resolver missing from Diagnostics")
		}
		return result
	}
	want, code := 200, ""
	switch mode {
	case "stale_before", "stale_after":
		want, code = 409, "PUBLIC_JOB_RESOLUTION_STALE"
	case "archived":
		want, code = 409, "PUBLIC_JOB_RESOLUTION_NOT_ELIGIBLE"
	case "protected":
		want, code = 422, "PUBLIC_JOB_RESOLUTION_PROTECTED_SOURCE"
	case "content_fail":
		want, code = 422, "PUBLIC_JOB_RESOLUTION_CONTENT_NOT_FOUND"
	}
	result := post(want)
	detail, err := w.Discovery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if want != 200 {
		if result["error_code"] != code || detail.ContentScope != "link_only" || detail.SourceText != "" || len(detail.Observations) != 1 {
			t.Fatalf("failure published content: %+v %+v", result, detail)
		}
		if (mode == "stale_before" || mode == "archived") && fetches != 0 {
			t.Fatal("ineligible record fetched")
		}
		return
	}
	if result["outcome"] != "updated" || detail.ContentScope != "page_visible" || detail.ContentVerification.Verified || !detail.NeedsContent || detail.ObservedURL != observedURL || detail.ResolvedURL != "https://careers.example.com/jobs/42" || len(detail.Observations) != 2 {
		t.Fatalf("lost source semantics: %+v", detail)
	}
	if mode == "override" && detail.Title != "My reviewed title" {
		t.Fatal("override lost")
	}
	oldRevision := detail.Revision
	again := post(200)
	if again["outcome"] != "duplicate" || fetches != 1 {
		t.Fatal("duplicate fetched again", again, fetches)
	}
	detail, err = w.Discovery(ctx, id)
	if err != nil || detail.Revision != oldRevision || len(detail.Observations) != 2 {
		t.Fatal("duplicate mutated discovery", err)
	}
	for _, name := range []string{"jobs/incoming", "jobs/normalized"} {
		entries, err := os.ReadDir(filepath.Join(start.DataRoot, filepath.FromSlash(name)))
		if err != nil || len(entries) != 0 {
			t.Fatal("resolver created a saved job", name, err)
		}
	}
}
