package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/materials/materialintegrity"
	"careerhub.local/backend/internal/materials/materialwrite"
)

type materialSpy struct {
	calls     []string
	draft     materials.Request
	review    materialintegrity.ReviewRequest
	export    materialwrite.ExportRequest
	download  materialwrite.ExportReadRequest
	legacy    materialwrite.LegacyExportRequest
	out       *materialwrite.Outcome
	problem   *materialwrite.Error
	panicCall bool
	cancel    context.CancelFunc
}

func (s *materialSpy) finish(name string) (*materialwrite.Outcome, *materialwrite.Error) {
	s.calls = append(s.calls, name)
	if s.panicCall {
		panic("private-marker")
	}
	if s.cancel != nil {
		s.cancel()
	}
	return s.out, s.problem
}
func (s *materialSpy) actions() MaterialActions {
	return MaterialActions{
		Draft: func(_ context.Context, r materials.Request) (*materialwrite.Outcome, *materialwrite.Error) {
			s.draft = r
			return s.finish("draft")
		},
		Review: func(_ context.Context, r materialintegrity.ReviewRequest) (*materialwrite.Outcome, *materialwrite.Error) {
			s.review = r
			return s.finish("review")
		},
		Export: func(_ context.Context, r materialwrite.ExportRequest) (*materialwrite.Outcome, *materialwrite.Error) {
			s.export = r
			return s.finish("export")
		},
		Download: func(_ context.Context, r materialwrite.ExportReadRequest) (*materialwrite.Outcome, *materialwrite.Error) {
			s.download = r
			return s.finish("download")
		},
		Legacy: func(_ context.Context, r materialwrite.LegacyExportRequest) (*materialwrite.Outcome, *materialwrite.Error) {
			s.legacy = r
			return s.finish("legacy")
		},
	}
}

func materialFixture(t *testing.T) (http.Handler, string, *materialSpy) {
	t.Helper()
	s := &materialSpy{out: &materialwrite.Outcome{Result: json.RawMessage(`{"revision":1,"draft":{}}`)}}
	h, e := NewWithMaterials(port, "0.1.7-dev", func() (map[string]any, error) { return map[string]any{}, nil }, emptyProfile, emptyDocuments, s.actions())
	if e != nil {
		t.Fatal(e)
	}
	token := sessionTokenOf(h)
	return h, token, s
}

func materialPost(path, token, body string) *http.Request {
	r := request("POST", path, token)
	r.Body = io.NopCloser(strings.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestMaterialHTTPDraftReviewAndExportDispatch(t *testing.T) {
	h, token, s := materialFixture(t)
	capabilities := response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)
	if capabilities["material_integrity"] != float64(1) {
		t.Fatal("material capability not advertised")
	}
	base := "/api/jobs/synthetic/materials/resume"
	response(t, h, request("GET", base, token), 200)
	if s.draft.JobID != "synthetic" || s.draft.Kind != "resume" || s.draft.Operation != "load" {
		t.Fatal("editor selection")
	}
	response(t, h, materialPost(base, token, `{"action":"approve","expected_revision":2,"draft":{"status":"draft"}}`), 200)
	if !s.draft.Approve || s.draft.ExpectedRevision != 2 || s.draft.Operation != "save" || string(s.draft.Payload) != `{"status":"draft"}` {
		t.Fatal("save envelope mapping")
	}
	response(t, h, materialPost(base, token, `{"action":"approve","action":"save_draft","expected_revision":-0,"draft":{}}`), 200)
	if s.draft.Approve || s.draft.ExpectedRevision != 0 {
		t.Fatal("source last-key/integer behavior changed")
	}
	response(t, h, request("GET", "/api/jobs/Fr%C3%A9d%20Job/materials/cover-letter", token), 200)
	if s.draft.JobID != "Fréd Job" || s.draft.Kind != "cover_letter" {
		t.Fatal("valid encoded job segment lost")
	}
	response(t, h, request("GET", base+"/reviews", token), 200)
	if s.review.Operation != "inspect" || len(s.review.TargetIDs) != 0 {
		t.Fatal("review GET changed state")
	}
	body := `{"operation":"confirm","expected_revision":0,"review_sha256":"` + strings.Repeat("a", 64) + `","target_ids":["fact_` + strings.Repeat("b", 64) + `"]}`
	response(t, h, materialPost(base+"/reviews", token, body), 200)
	if s.review.Operation != "confirm" || s.review.JobID != "synthetic" || s.review.Kind != "resume" {
		t.Fatal("explicit review request")
	}
	response(t, h, materialPost(base+"/exports", token, `{"format":" DOCX "}`), 200)
	if s.export.FileFormat != "docx" {
		t.Fatal("format selection")
	}
	response(t, h, materialPost(base+"/exports", token, `{}`), 200)
	if s.export.FileFormat != "pdf" {
		t.Fatal("source default format")
	}
}

type materialBodySpy struct{ reads int }

func (s *materialBodySpy) Read([]byte) (int, error) { s.reads++; return 0, io.EOF }
func (*materialBodySpy) Close() error               { return nil }

func TestMaterialHTTPAuthorizationBeforeBodyOrStorage(t *testing.T) {
	h, token, s := materialFixture(t)
	for _, suffix := range []string{"", "/reviews", "/exports"} {
		for _, change := range []string{"token", "origin", "host", "fetch", "duplicate-token"} {
			t.Run(suffix+"/"+change, func(t *testing.T) {
				before := len(s.calls)
				r := materialPost("/api/jobs/job/materials/resume"+suffix, token, `{}`)
				body := &materialBodySpy{}
				r.Body = body
				switch change {
				case "token":
					r.Header.Del(CapabilityHeader)
				case "origin":
					r.Header.Set("Origin", "https://example.invalid")
				case "host":
					r.Host = "example.invalid:8766"
				case "fetch":
					r.Header.Set("Sec-Fetch-Site", "cross-site")
				case "duplicate-token":
					r.Header[CapabilityHeader] = []string{token, token}
				}
				response(t, h, r, 403)
				if body.reads != 0 || len(s.calls) != before {
					t.Fatal("unauthorized body/callback touched")
				}
			})
		}
	}
	r := request("GET", "/api/jobs/job/materials/resume/file", token)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	response(t, h, r, 403)
	for _, path := range []string{"/api/jobs/%2e%2e/materials/resume", "/api/jobs/a%2fb/materials/resume", "/api/jobs/a%5cb/materials/resume", "/api/jobs/a/materials/%72esume", "/%61pi/jobs/a/materials/resume", "/api/jobs/a/materials/resume/", "/api/jobs/a/materials/resume/unknown"} {
		response(t, h, request("GET", path, token), 404)
	}
	if len(s.calls) != 0 {
		t.Fatal("alternate route selected a service")
	}
	readonly, readToken := fixture(t, func() (map[string]any, error) { return map[string]any{}, nil })
	response(t, readonly, request("GET", "/api/jobs/job/materials/resume", readToken), 404)
	response(t, readonly, materialPost("/api/jobs/job/materials/resume", readToken, `{}`), 404)
	for _, method := range []string{"HEAD", "PUT", "DELETE", "PATCH"} {
		response(t, h, request(method, "/api/jobs/job/materials/resume", token), 404)
	}
	response(t, h, request("OPTIONS", "/api/jobs/job/materials/resume", token), 403)
}

func TestMaterialHTTPRequestValidation(t *testing.T) {
	h, token, s := materialFixture(t)
	base := "/api/jobs/job/materials/resume"
	for _, body := range []string{`null`, `[]`, `{}`, `{"action":"save_draft","expected_revision":null,"draft":{}}`, `{"action":"save_draft","expected_revision":true,"draft":{}}`, `{"action":"save_draft","expected_revision":1.0,"draft":{}}`, `{"action":"save_draft","expected_revision":1e0,"draft":{}}`, `{"action":"save_draft","expected_revision":-1,"draft":{}}`, `{"action":"save_draft","expected_revision":9223372036854775808,"draft":{}}`, `{"action":"save_draft","expected_revision":0,"draft":null}`, `{"action":"save_draft","expected_revision":0,"draft":{},"job_id":"other"}`, `{"action":"save_draft","expected_revision":0,"draft":{}} {}`} {
		response(t, h, materialPost(base, token, body), 400)
	}
	for _, test := range []struct{ path, body string }{{"/reviews", `{"operation":"inspect"}`}, {"/reviews", `{"operation":"confirm","expected_revision":0,"review_sha256":"x","target_ids":[]}`}, {"/exports", `{"format":"html"}`}, {"/exports", `{"format":"pdf","source":"secret"}`}} {
		response(t, h, materialPost(base+test.path, token, test.body), 400)
	}
	for _, mode := range []string{"type", "duplicate-type", "duplicate-length", "chunked", "short", "large", "utf8"} {
		r := materialPost(base, token, `{}`)
		status := 400
		switch mode {
		case "type":
			r.Header.Set("Content-Type", "text/plain")
			status = 415
		case "duplicate-type":
			r.Header["Content-Type"] = []string{"application/json", "application/json"}
			status = 415
		case "duplicate-length":
			r.Header["Content-Length"] = []string{"2", "2"}
		case "chunked":
			r.TransferEncoding = []string{"chunked"}
		case "short":
			r.ContentLength = 3
		case "large":
			r.ContentLength = MaxMaterialRequestBytes + 1
			status = 413
		case "utf8":
			r.Body = io.NopCloser(bytes.NewReader([]byte{'{', '"', 0xff, '"', ':', '0', '}'}))
			r.ContentLength = 7
		}
		response(t, h, r, status)
	}
	if len(s.calls) != 0 {
		t.Fatal("invalid request dispatched")
	}
}

func materialFileOutcome(kind, format, provenance string) *materialwrite.Outcome {
	content := []byte("private document bytes")
	sum := sha256.Sum256(content)
	raw, _ := json.Marshal(map[string]any{"job_id": "job", "kind": kind, "format": format, "filename": "Sample_Resume_Employer." + format, "sha256": hex.EncodeToString(sum[:]), "size_bytes": len(content), "artifact_id": "material_" + strings.Repeat("a", 64), "provenance": provenance})
	return &materialwrite.Outcome{Result: raw, Content: content}
}

func TestMaterialHTTPDownloadsAndMetadata(t *testing.T) {
	h, token, s := materialFixture(t)
	base := "/api/jobs/job/materials/resume"
	for _, format := range []string{"pdf", "docx", "txt"} {
		s.out = materialFileOutcome("resume", format, "versioned")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("GET", base+"/file?format="+format, token))
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), s.out.Content) || w.Header().Get("Content-Disposition") != "attachment; filename*=UTF-8''Sample_Resume_Employer."+format || w.Header().Get("X-Career-Hub-Provenance") != "versioned" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("download data/headers changed")
		}
		if s.export.FileFormat != format {
			t.Fatal("compatibility export format")
		}
	}
	s.out = materialFileOutcome("resume", "pdf", "versioned")
	before := len(s.calls)
	response(t, h, materialPost(base+"/exports", token, `{"format":"pdf"}`), 200)
	if len(s.calls) != before+1 || s.calls[len(s.calls)-1] != "export" {
		t.Fatal("POST export dispatch")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", base+"/file?artifact_id=material_"+strings.Repeat("a", 64), token))
	if w.Code != 200 || s.calls[len(s.calls)-1] != "download" {
		t.Fatal("existing download rerendered")
	}
	response(t, h, request("GET", base+"/file?artifact_id=material_"+strings.Repeat("b", 64), token), 500)
	s.out = materialFileOutcome("resume", "pdf", "legacy_unverified")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", base+"/legacy-file?filename=Sample_Resume_Employer.pdf", token))
	if w.Code != 200 || s.legacy.Filename != "Sample_Resume_Employer.pdf" || w.Header().Get("X-Career-Hub-Provenance") != "legacy_unverified" || w.Header().Get("X-Career-Hub-Artifact-ID") != "" {
		t.Fatal("legacy labeled as verified")
	}
	response(t, h, request("GET", base+"/file?format=pdf&artifact_id=material_"+strings.Repeat("a", 64), token), 400)
	for _, change := range []string{"bytes", "job", "filename", "format", "provenance", "nil", "partial"} {
		s.out = materialFileOutcome("resume", "pdf", "versioned")
		switch change {
		case "bytes":
			s.out.Content = append(s.out.Content, 'x')
		case "job":
			s.out.Result = bytes.Replace(s.out.Result, []byte(`"job_id":"job"`), []byte(`"job_id":"other"`), 1)
		case "filename":
			s.out.Result = bytes.Replace(s.out.Result, []byte("Sample_Resume_Employer.pdf"), []byte(`evil\r\nInjected: yes.pdf`), 1)
		case "format":
			s.out.Result = bytes.Replace(s.out.Result, []byte(`"format":"pdf"`), []byte(`"format":"html"`), 1)
		case "provenance":
			s.out.Result = bytes.Replace(s.out.Result, []byte("versioned"), []byte("legacy_unverified"), 1)
		case "nil":
			s.out = nil
		case "partial":
			s.out.Partial = true
		}
		body := response(t, h, request("GET", base+"/file", token), 500)
		if strings.Contains(fmt.Sprint(body), "private document") {
			t.Fatal("failed file bytes leaked")
		}
	}
}

func TestMaterialHTTPFailureStatePanicAndCancellation(t *testing.T) {
	h, token, s := materialFixture(t)
	base := "/api/jobs/job/materials/resume"
	for _, test := range []struct {
		code   string
		status int
	}{{"MATERIAL_REVISION_CONFLICT", 409}, {"MATERIAL_EXPORT_FACTS_BLOCKED", 409}, {"MATERIAL_ARTIFACT_CHANGED", 409}, {"private-marker", 500}} {
		s.out = &materialwrite.Outcome{Committed: true, CleanupFailed: true, Content: []byte("private-marker"), Result: json.RawMessage(`{"private-marker":true}`)}
		s.problem = &materialwrite.Error{Code: test.code}
		body := response(t, h, request("GET", base, token), test.status)
		state := body["operation_state"].(map[string]any)
		if state["committed"] != true || state["cleanup_failed"] != true || strings.Contains(fmt.Sprint(body), "private-marker") || body["run_id"] != nil {
			t.Fatal("late failure lost commit facts/leaked data")
		}
	}
	s.problem = nil
	s.panicCall = true
	body := response(t, h, request("GET", base, token), 500)
	if body["operation_state"].(map[string]any)["known"] != false {
		t.Fatal("panic claimed no writes")
	}
	s.panicCall = false
	s.out = &materialwrite.Outcome{Result: json.RawMessage(`{"revision":1}`)}
	response(t, h, request("GET", base, token), 200)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := len(s.calls)
	response(t, h, request("GET", base, token).WithContext(ctx), 408)
	if len(s.calls) != before {
		t.Fatal("pre-canceled dispatch")
	}
	ctx, cancel = context.WithCancel(context.Background())
	s.cancel = cancel
	s.out.Committed = true
	body = response(t, h, request("GET", base, token).WithContext(ctx), 408)
	if body["operation_state"].(map[string]any)["committed"] != true {
		t.Fatal("late cancellation claimed rollback")
	}
}

type materialDeadlineWriter struct {
	*httptest.ResponseRecorder
	failure  error
	deadline time.Time
}

func (w *materialDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return w.failure
}

func TestMaterialHTTPConstructionResponseBoundsAndDeadline(t *testing.T) {
	s := &materialSpy{}
	for _, missing := range []string{"draft", "review", "export", "download", "legacy"} {
		a := s.actions()
		switch missing {
		case "draft":
			a.Draft = nil
		case "review":
			a.Review = nil
		case "export":
			a.Export = nil
		case "download":
			a.Download = nil
		case "legacy":
			a.Legacy = nil
		}
		if h, e := NewWithMaterials(port, "0.1.7", func() (map[string]any, error) { return map[string]any{}, nil }, emptyProfile, emptyDocuments, a); e == nil || h != nil {
			t.Fatal("incomplete material service admitted")
		}
	}
	h, token, s := materialFixture(t)
	base := "/api/jobs/job/materials/resume"
	for _, raw := range [][]byte{nil, []byte(`[]`), []byte(`{"incomplete":`), {'{', '"', 0xff, '"', ':', '0', '}'}, []byte(`{"large":"` + strings.Repeat("x", materials.MaxResponseBytes) + `"}`)} {
		s.out = &materialwrite.Outcome{Result: raw, Committed: true}
		body := response(t, h, request("GET", base, token), 500)
		if body["operation_state"].(map[string]any)["committed"] != true {
			t.Fatal("invalid response erased publication state")
		}
	}
	s.out = &materialwrite.Outcome{Result: json.RawMessage(`{"revision":1}`)}
	before := len(s.calls)
	w := &materialDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), failure: errors.New("private deadline cause")}
	h.ServeHTTP(w, request("GET", base, token))
	if w.Code != 500 || len(s.calls) != before || strings.Contains(w.Body.String(), "private deadline cause") {
		t.Fatal("failed deadline entered owner/leaked cause")
	}
	w = &materialDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	start := time.Now()
	h.ServeHTTP(w, request("GET", base, token))
	if w.Code != 200 || w.deadline.Before(start.Add(24*time.Second)) || w.deadline.After(time.Now().Add(26*time.Second)) {
		t.Fatal("material response deadline not installed")
	}
}
