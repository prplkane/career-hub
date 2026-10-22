package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"careerhub.local/backend/internal/resumelibrary"
)

func uploadRequest(token string, content []byte) *http.Request {
	r := request("POST", "/api/resumes", token)
	r.Body = io.NopCloser(bytes.NewReader(content))
	r.ContentLength = int64(len(content))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("X-Resume-Name", "R%C3%A9sum%C3%A9+final.TXT")
	r.Header.Set("X-Resume-Label", "Primary%20resume")
	return r
}

func attachRequest(token, jobID string, content []byte) *http.Request {
	r := uploadRequest(token, content)
	r.URL.Path = "/api/jobs/" + jobID + "/application/resume"
	r.URL.RawPath = ""
	return r
}

func TestResumeUploadUsesBoundedBinaryAndExplicitOwner(t *testing.T) {
	h, token, spy := applicationFixture(t)
	content := []byte("Synthetic resume\x00\xff")
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"id":"resume_one","file_name":"Résumé+final.TXT"}`)}
	actual := response(t, h, uploadRequest(token, content), http.StatusCreated)
	if actual["id"] != "resume_one" || spy.calls[len(spy.calls)-1] != "jobs.resume.upload:" ||
		spy.fields["file_name"] != "Résumé+final.TXT" || spy.fields["display_name"] != "Primary resume" ||
		!bytes.Equal(spy.content, content) {
		t.Fatal("upload did not reach the bounded native owner with exact bytes and decoded metadata")
	}
	// The HTTP request is binary and may reach the domain's full 10 MiB limit.
	large := bytes.Repeat([]byte("x"), resumelibrary.MaxResumeBytes)
	response(t, h, uploadRequest(token, large), http.StatusCreated)
	if len(spy.content) != len(large) {
		t.Fatal("valid 10 MiB upload was truncated")
	}
	if !strings.Contains(fmt.Sprintf("%v", JobRequest{Content: content}), "redacted") {
		t.Fatal("job request formatting must not print resume bytes")
	}
}

func TestResumeUploadRefusesMalformedOrUnauthorizedInputBeforeOwner(t *testing.T) {
	h, token, spy := applicationFixture(t)
	baseline := len(spy.calls)
	for _, tc := range []struct {
		name   string
		edit   func(*http.Request)
		status int
	}{
		{"empty", func(r *http.Request) {}, 400},
		{"oversized", func(r *http.Request) { r.ContentLength = resumelibrary.MaxResumeBytes + 1 }, 400},
		{"missing media", func(r *http.Request) { r.Header.Del("Content-Type") }, 400},
		{"wrong media", func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }, 400},
		{"duplicate media", func(r *http.Request) { r.Header.Add("Content-Type", "application/octet-stream") }, 400},
		{"duplicate name", func(r *http.Request) { r.Header.Add("X-Resume-Name", "other.txt") }, 400},
		{"missing name", func(r *http.Request) { r.Header.Del("X-Resume-Name") }, 400},
		{"bad escape", func(r *http.Request) { r.Header.Set("X-Resume-Name", "bad%GG.txt") }, 400},
		{"invalid UTF8", func(r *http.Request) { r.Header.Set("X-Resume-Label", "%FF") }, 400},
		{"chunked", func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }, 400},
		{"short body", func(r *http.Request) { r.ContentLength++ }, 400},
		{"extra body", func(r *http.Request) { r.ContentLength-- }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := []byte("synthetic")
			if tc.name == "empty" {
				content = nil
			}
			r := uploadRequest(token, content)
			tc.edit(r)
			response(t, h, r, tc.status)
		})
	}
	response(t, h, uploadRequest("wrong", []byte("synthetic")), 403)
	crossOrigin := uploadRequest(token, []byte("synthetic"))
	crossOrigin.Header.Set("Origin", "https://other.example")
	response(t, h, crossOrigin, 403)
	response(t, h, request("PUT", "/api/resumes", token), 404)
	if len(spy.calls) != baseline {
		t.Fatal("refused upload reached native owner")
	}
}

func TestApplicationResumeAttachUsesBoundedBinaryAndSourceResponse(t *testing.T) {
	h, token, spy := applicationFixture(t)
	content := []byte("Synthetic application resume\x00")
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"job_id":"job-one","current_status":"captured","resume_selection":{"resume":{"id":"resume_one","display_name":"Primary resume"}}}`)}
	actual := response(t, h, attachRequest(token, "job-one", content), http.StatusOK)
	if actual["job_id"] != "job-one" || actual["current_status"] != "captured" ||
		spy.calls[len(spy.calls)-1] != "jobs.resume.attach:job-one" ||
		spy.fields["job_id"] != "job-one" || spy.fields["file_name"] != "Résumé+final.TXT" ||
		spy.fields["label"] != "Primary resume" || !bytes.Equal(spy.content, content) {
		t.Fatal("application resume attach did not preserve the closed source request/response contract")
	}
}

func TestApplicationResumeAttachRejectsInvalidInputBeforeOwner(t *testing.T) {
	h, token, spy := applicationFixture(t)
	baseline := len(spy.calls)
	badType := attachRequest(token, "job-one", []byte("synthetic"))
	badType.Header.Set("Content-Type", "application/json")
	if response(t, h, badType, 400)["error_code"] != "APPLICATION_RESUME_INVALID" {
		t.Fatal("attach media failure lost its operation-specific error")
	}
	badSuffix := attachRequest(token, "job-one", []byte("synthetic"))
	badSuffix.Header.Set("X-Resume-Name", "resume.exe")
	if response(t, h, badSuffix, 400)["error_code"] != "APPLICATION_RESUME_INVALID" {
		t.Fatal("attach validation failure lost its operation-specific error")
	}
	response(t, h, attachRequest("wrong", "job-one", []byte("synthetic")), 403)
	response(t, h, request("GET", "/api/jobs/job-one/application/resume", token), 404)
	if len(spy.calls) != baseline {
		t.Fatal("refused application resume attach reached the native owner")
	}
}

func TestResumeDeleteIsClosedBodylessAndRetainsFailureFacts(t *testing.T) {
	h, token, spy := applicationFixture(t)
	path := "/api/resumes/resume-one"
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"id":"resume-one","deleted":true}`)}
	if response(t, h, request("DELETE", path, token), 200)["deleted"] != true ||
		spy.calls[len(spy.calls)-1] != "jobs.resume.delete:resume-one" {
		t.Fatal("delete did not reach the native owner")
	}
	baseline := len(spy.calls)
	withBody := vaultRequest("DELETE", path, token, `{}`)
	if response(t, h, withBody, 400)["error_code"] != "RESUME_VERSION_DELETE_INVALID" {
		t.Fatal("nonempty delete body accepted")
	}
	response(t, h, request("DELETE", path, "wrong"), 403)
	crossOrigin := request("DELETE", path, token)
	crossOrigin.Header.Set("Origin", "https://other.example")
	response(t, h, crossOrigin, 403)
	for _, badPath := range []string{"/api/resumes/%2e%2e", "/api/resumes/a%2Fb", "/api/resumes/resume-one/file"} {
		response(t, h, request("DELETE", badPath, token), 404)
	}
	if len(spy.calls) != baseline {
		t.Fatal("refused delete reached native owner")
	}
	spy.out = &ApplicationOutcome{Committed: true, CommitUnknown: true, RecoveryRequired: true, CleanupFailed: true}
	spy.err = &ApplicationError{Code: "RESUME_VERSION_DELETE_FAILED"}
	state := response(t, h, request("DELETE", path, token), 500)["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != true || state["recovery_required"] != true || state["cleanup_failed"] != true {
		t.Fatal("delete failure hid publication facts", state)
	}
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"RESUME_LIBRARY_NOT_FOUND", 404}, {"RESUME_VERSION_IN_USE", 409},
		{"RESUME_VERSION_FILE_MISSING", 409}, {"RESUME_VERSION_FILE_INVALID", 409},
		{"RESUME_VERSION_IDENTITY_COLLISION", 409},
	} {
		spy.out = &ApplicationOutcome{}
		spy.err = &ApplicationError{Code: tc.code}
		if response(t, h, request("DELETE", path, token), tc.status)["error_code"] != tc.code {
			t.Fatal("delete failure classification changed", tc.code)
		}
	}
}
