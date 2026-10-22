package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"careerhub.local/backend/internal/resumelibrary"
)

func resumeFileOutcome(name string, content []byte) *ApplicationOutcome {
	sum := sha256.Sum256(content)
	meta, _ := json.Marshal(map[string]any{
		"id": "resume-one", "file_name": name,
		"sha256": hex.EncodeToString(sum[:]), "size_bytes": len(content),
	})
	return &ApplicationOutcome{Result: meta, Content: content}
}

func submittedFileOutcome(name string, content []byte) *ApplicationOutcome {
	sum := sha256.Sum256(content)
	meta, _ := json.Marshal(map[string]any{
		"resume_id": "resume-two", "file_name": name,
		"sha256": hex.EncodeToString(sum[:]), "size_bytes": len(content),
	})
	return &ApplicationOutcome{Result: meta, Content: content}
}

func TestAttachmentDownloadAcceptsVerifiedLegacyNullID(t *testing.T) {
	h, token, spy := applicationFixture(t)
	path := "/api/jobs/job-one/application/resume/file"
	for _, legacy := range []bool{false, true} {
		content := []byte("fictional attachment")
		spy.out = submittedFileOutcome("Attached Resume.txt", content)
		if legacy {
			spy.out.Result = bytes.Replace(spy.out.Result, []byte(`"resume-two"`), []byte(`null`), 1)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("GET", path, token))
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), content) || w.Header().Get("Content-Disposition") != "inline; filename*=UTF-8''Attached%20Resume.txt" || spy.calls[len(spy.calls)-1] != "jobs.resume.attachment_file:job-one" {
			t.Fatal("attachment response changed", w.Code, w.Body.String())
		}
	}
	count := len(spy.calls)
	response(t, h, request("GET", path, ""), 403)
	response(t, h, request("POST", path, token), 404)
	response(t, h, request("GET", "/api/jobs/%2e%2e/application/resume/file", token), 404)
	if len(spy.calls) != count {
		t.Fatal("invalid attachment request reached owner")
	}
	for _, identity := range []string{`""`, `123`, `false`, `{}`} {
		spy.out = submittedFileOutcome("file.txt", []byte("fixture"))
		spy.out.Result = bytes.Replace(spy.out.Result, []byte(`"resume-two"`), []byte(identity), 1)
		response(t, h, request("GET", path, token), 500)
	}
	spy.out = resumeFileOutcome("file.txt", []byte("fixture"))
	response(t, h, request("GET", path, token), 500)
	spy.out = &ApplicationOutcome{}
	for _, tc := range []struct {
		code   string
		status int
	}{{"APPLICATION_RESUME_NOT_FOUND", 404}, {"APPLICATION_RESUME_INTEGRITY_FAILED", 409}, {"APPLICATION_RESUME_READ_FAILED", 500}} {
		spy.err = &ApplicationError{Code: tc.code}
		if got := response(t, h, request("GET", path, token), tc.status); got["error_code"] != tc.code {
			t.Fatal(got)
		}
	}
}

func TestSubmittedResumeFileIsJobScopedInlineAndTokenProtected(t *testing.T) {
	h, token, spy := applicationFixture(t)
	content := []byte("%PDF-1.4 fictional submitted resume")
	spy.out = submittedFileOutcome("Submitted Draft (v2).pdf", content)
	path := "/api/jobs/job-one/application/submitted-resume/file"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", path, token))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), content) ||
		w.Header().Get("Content-Type") != "application/pdf" ||
		w.Header().Get("Content-Disposition") != "inline; filename*=UTF-8''Submitted%20Draft%20%28v2%29.pdf" ||
		w.Header().Get("Cache-Control") != "no-store" ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" ||
		w.Header().Get("Content-Length") != strconv.Itoa(len(content)) {
		t.Fatal("submitted bytes/headers not preserved", w.Code, w.Header(), w.Body.String())
	}
	if spy.calls[len(spy.calls)-1] != "jobs.resume.submitted_file:job-one" {
		t.Fatal("route did not bind the job ID to the owner", spy.calls)
	}
	count := len(spy.calls)
	response(t, h, request("GET", path, "wrong"), 403)
	for _, c := range []struct{ method, path string }{
		{"POST", path}, {"GET", "/api/jobs/job-one/application/submitted-resume/other"},
		{"GET", "/api/jobs/a%2Fb/application/submitted-resume/file"},
	} {
		response(t, h, request(c.method, c.path, token), 404)
	}
	if len(spy.calls) != count {
		t.Fatal("refused request reached the resume owner", spy.calls)
	}
}

func TestSubmittedResumeFileRejectsUnverifiedOutputAndMapsOwnerFailures(t *testing.T) {
	h, token, spy := applicationFixture(t)
	path := "/api/jobs/job-one/application/submitted-resume/file"
	content := []byte("fictional")
	for _, bad := range []*ApplicationOutcome{
		resumeFileOutcome("wrong-owner.pdf", content),
		{Result: json.RawMessage(`{"resume_id":"resume-two","file_name":"synthetic.pdf","sha256":"bad","size_bytes":9}`), Content: content},
		submittedFileOutcome("synthetic.pdf", nil),
		submittedFileOutcome("synthetic.pdf", bytes.Repeat([]byte("x"), resumelibrary.MaxResumeBytes+1)),
	} {
		spy.out = bad
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("GET", path, token))
		if w.Code != 500 || w.Header().Get("Content-Disposition") != "" {
			t.Fatal("unverified submitted bytes reached response", w.Code, w.Header())
		}
	}
	for _, c := range []struct {
		code   string
		status int
	}{
		{"APPLICATION_SUBMITTED_RESUME_NOT_FOUND", 404},
		{"APPLICATION_SUBMITTED_RESUME_INTEGRITY_FAILED", 409},
		{"APPLICATION_SUBMITTED_RESUME_READ_FAILED", 500},
	} {
		spy.out, spy.err = &ApplicationOutcome{}, &ApplicationError{Code: c.code}
		if response(t, h, request("GET", path, token), c.status)["error_code"] != c.code {
			t.Fatal("submitted-file failure classification changed", c)
		}
	}
}

func TestResumeFileUsesVerifiedBinaryResponse(t *testing.T) {
	h, token, spy := applicationFixture(t)
	content := []byte("Synthetic resume\x00\xff")
	spy.out = resumeFileOutcome("Résumé final + %.DOCX", content)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", "/api/resumes/resume-one/file", token))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), content) ||
		w.Header().Get("Content-Type") != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" ||
		w.Header().Get("Content-Disposition") != "attachment; filename*=UTF-8''R%C3%A9sum%C3%A9%20final%20%2B%20%25.DOCX" ||
		w.Header().Get("Content-Length") != strconv.Itoa(len(content)) ||
		w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("wrong binary response: status=%d headers=%v body=%q", w.Code, w.Header(), w.Body.Bytes())
	}
	if spy.calls[len(spy.calls)-1] != "jobs.resume.file:resume-one" {
		t.Fatal("wrong owner", spy.calls)
	}
	for _, c := range []struct{ name, want string }{
		{"synthetic.doc", "application/msword"},
		{"synthetic.pdf", "application/pdf"},
		{"synthetic.rtf", "application/rtf"},
		{"synthetic.txt", "text/plain; charset=utf-8"},
	} {
		spy.out = resumeFileOutcome(c.name, content)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, request("GET", "/api/resumes/resume-one/file", token))
		if w.Code != 200 || w.Header().Get("Content-Type") != c.want {
			t.Fatal("wrong source MIME", c, w.Code, w.Header().Get("Content-Type"))
		}
	}
	// The binary route has its own 10 MiB ceiling, independent of the 4 MiB
	// JSON envelope limit. The owner still validates the exact stored bytes.
	large := bytes.Repeat([]byte("x"), MaxResponseBytes+1)
	spy.out = resumeFileOutcome("synthetic.txt", large)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", "/api/resumes/resume-one/file", token))
	if w.Code != 200 || w.Body.Len() != len(large) {
		t.Fatal("valid large binary response was rejected")
	}
}

func TestResumeRoutesRejectUnauthorizedOrUnscopedRequests(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = resumeFileOutcome("synthetic.txt", []byte("synthetic"))
	count := len(spy.calls)
	response(t, h, request("GET", "/api/resumes/resume-one/file", "wrong"), 403)
	crossOrigin := request("GET", "/api/resumes/resume-one/file", token)
	crossOrigin.Header.Set("Origin", "https://other.example")
	response(t, h, crossOrigin, 403)
	crossOriginPost := request("POST", "/api/resumes/resume-one/archive", token)
	crossOriginPost.Header.Set("Origin", "https://other.example")
	response(t, h, crossOriginPost, 403)
	for _, tc := range []struct{ method, path string }{
		{"HEAD", "/api/resumes/resume-one/file"},
		{"POST", "/api/resumes/resume-one/file"},
		{"GET", "/api/resumes/resume-one/unknown"},
		{"GET", "/api/resumes/%2e%2e/file"},
		{"GET", "/api/resumes/a%2Fb/file"},
		{"POST", "/api/resumes/resume-one/delete"},
	} {
		response(t, h, request(tc.method, tc.path, token), 404)
	}
	withBody := vaultRequest("POST", "/api/resumes/resume-one/archive", token, `{}`)
	if response(t, h, withBody, 400)["error_code"] != "RESUME_LIBRARY_ARCHIVE_INVALID" {
		t.Fatal("unexpected archive body was accepted")
	}
	if len(spy.calls) != count {
		t.Fatal("unauthorized or unsupported route reached owner", spy.calls)
	}
	if encoded := encodeResumeFilename("unsafe\r\nX-Injected: yes.txt"); !strings.Contains(encoded, "%0D%0A") {
		t.Fatal("unsafe filename bytes were not encoded")
	}
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"id":"resume-one","archived":true}`)}
	response(t, h, request("POST", "/api/resumes/resume-one/archive", token), 200)
	if spy.calls[len(spy.calls)-1] != "jobs.resume.archive:resume-one" {
		t.Fatal("archive did not use owner")
	}
	response(t, h, request("POST", "/api/resumes/resume-one/restore", token), 200)
	if spy.calls[len(spy.calls)-1] != "jobs.resume.restore:resume-one" {
		t.Fatal("restore did not use owner")
	}
}

func TestResumeFileRejectsInconsistentMetadataAndPreservesFailureFacts(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, outcome := range []*ApplicationOutcome{
		{Result: json.RawMessage(`{"id":"other","file_name":"synthetic.txt","size_bytes":2}`), Content: []byte("hi")},
		{Result: json.RawMessage(`{"id":"resume-one","file_name":"synthetic.txt","size_bytes":2,"sha256":"bad"}`), Content: []byte("hi")},
		resumeFileOutcome("synthetic.txt", nil),
		{Result: json.RawMessage(`{"id":"resume-one","file_name":"synthetic.txt","size_bytes":2,"sha256":"bad"}`), Content: []byte("too long")},
	} {
		spy.out = outcome
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("GET", "/api/resumes/resume-one/file", token))
		if w.Code != 500 || w.Header().Get("Content-Disposition") != "" {
			t.Fatal("unverified bytes reached response", w.Code, w.Header(), w.Body.String())
		}
	}
	spy.out = &ApplicationOutcome{Committed: true, CommitUnknown: true, RecoveryRequired: true, CleanupFailed: true}
	spy.err = &ApplicationError{Code: "RESUME_LIBRARY_ARCHIVE_FAILED"}
	result := response(t, h, request("POST", "/api/resumes/resume-one/archive", token), 500)
	state := result["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != true || state["recovery_required"] != true || state["cleanup_failed"] != true {
		t.Fatal("archive erased operation facts", state)
	}
	spy.out = &ApplicationOutcome{}
	spy.err = &ApplicationError{Code: "RESUME_LIBRARY_NOT_FOUND"}
	if response(t, h, request("GET", "/api/resumes/resume-one/file", token), 404)["error_code"] != "RESUME_LIBRARY_NOT_FOUND" {
		t.Fatal("missing file was not classified")
	}
	spy.err = &ApplicationError{Code: "RESUME_VERSION_FILE_INVALID"}
	response(t, h, request("GET", "/api/resumes/resume-one/file", token), 409)
	spy.err = nil
	spy.out = resumeFileOutcome("synthetic.txt", bytes.Repeat([]byte("x"), resumelibrary.MaxResumeBytes+1))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", "/api/resumes/resume-one/file", token))
	if w.Code != 500 || w.Header().Get("Content-Disposition") != "" {
		t.Fatal("oversized binary response escaped the file limit", w.Code)
	}
}
