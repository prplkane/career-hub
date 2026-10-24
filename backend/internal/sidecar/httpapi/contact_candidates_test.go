package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications/contactcandidate"
)

const candidateDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func candidatePreviewBody() string {
	return `{"contract_version":1,"source_kind":"job","source_id":"fictional-job","candidate_id":"` + candidateDigest + `","expected_content_sha256":"` + candidateDigest + `"}`
}

func candidateConfirmationBody() string {
	return strings.TrimSuffix(candidatePreviewBody(), "}") + `,"action":"create","contact":{"name":"Jane Example","role":"Recruiter","company":"Example","context":"Recruiting contact","channels":[]}}`
}

func TestContactCandidateExactRoutesDecodeTypedRequestsAndSelectStatus(t *testing.T) {
	h, token, spy := applicationFixture(t)
	response(t, h, materialPost("/api/contacts/candidate-preview", token, candidatePreviewBody()), http.StatusOK)
	if spy.job == nil || spy.job.Operation != "contact.candidate_preview" || spy.job.ContactCandidate == nil ||
		spy.job.ContactCandidate.SourceID != "fictional-job" || spy.job.ContactConfirmation != nil {
		t.Fatal("preview request did not reach the typed owner boundary")
	}

	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"contract_version":1,"outcome":"created","contact":{}}`), Created: true}
	response(t, h, materialPost("/api/contacts/confirm-candidate", token, candidateConfirmationBody()), http.StatusCreated)
	if spy.job == nil || spy.job.Operation != "contact.candidate_confirm" || spy.job.ContactConfirmation == nil ||
		spy.job.ContactConfirmation.Action != "create" || spy.job.ContactCandidate != nil {
		t.Fatal("confirmation request did not reach the typed owner boundary")
	}
	spy.out.Created = false
	spy.out.Result = json.RawMessage(`{"contract_version":1,"outcome":"provenance_match","contact":{}}`)
	response(t, h, materialPost("/api/contacts/confirm-candidate", token, candidateConfirmationBody()), http.StatusOK)

	capabilities := response(t, h, request("GET", "/api/session", token), http.StatusOK)["capabilities"].(map[string]any)
	if _, advertised := capabilities["contact_candidates"]; advertised {
		t.Fatal("closed adapter was advertised as an active capability")
	}
}

type candidateUnreadBody struct{ reads int }

func (body *candidateUnreadBody) Read([]byte) (int, error) { body.reads++; return 0, io.EOF }
func (*candidateUnreadBody) Close() error                  { return nil }

func TestContactCandidateNearMissAccessAndInvalidBodiesStayClosed(t *testing.T) {
	h, token, spy := applicationFixture(t)
	valid := candidatePreviewBody()
	before := len(spy.calls)
	for _, test := range []struct{ method, target string }{
		{"GET", "/api/contacts/candidate-preview"},
		{"PUT", "/api/contacts/candidate-preview"},
		{"POST", "/api/contacts/candidate-preview?source=job"},
		{"POST", "/api/contacts/candidate-preview?"},
		{"POST", "/api/contacts/candidate-preview/"},
		{"POST", "/api/contacts/%63andidate-preview"},
		{"POST", "/api/contacts/confirm-candidate/extra"},
	} {
		response(t, h, vaultRequest(test.method, test.target, token, valid), http.StatusNotFound)
	}
	unauthorizedBody := &candidateUnreadBody{}
	unauthorized := materialPost("/api/contacts/candidate-preview", "wrong", valid)
	unauthorized.Body = unauthorizedBody
	response(t, h, unauthorized, http.StatusForbidden)
	crossSite := materialPost("/api/contacts/candidate-preview", token, valid)
	crossSite.Header.Set("Origin", "https://attacker.invalid")
	response(t, h, crossSite, http.StatusForbidden)
	if len(spy.calls) != before || unauthorizedBody.reads != 0 {
		t.Fatal("near-miss or unauthorized request reached the owner")
	}

	invalid := []*http.Request{
		materialPost("/api/contacts/candidate-preview", token, `{}`),
		materialPost("/api/contacts/candidate-preview", token, valid+` {}`),
		materialPost("/api/contacts/candidate-preview", token, strings.Replace(valid, `"contract_version":1`, `"contract_version":2`, 1)),
		materialPost("/api/contacts/candidate-preview", token, strings.TrimSuffix(valid, "}")+`,"extra":true}`),
		materialPost("/api/contacts/candidate-preview", token, strings.Replace(valid, `"source_id":"fictional-job"`, `"source_id":"\ud800"`, 1)),
	}
	wrongType := materialPost("/api/contacts/candidate-preview", token, valid)
	wrongType.Header.Set("Content-Type", "text/plain")
	invalid = append(invalid, wrongType)
	chunked := materialPost("/api/contacts/candidate-preview", token, valid)
	chunked.TransferEncoding = []string{"chunked"}
	invalid = append(invalid, chunked)
	for _, item := range invalid {
		result := response(t, h, item, http.StatusBadRequest)
		if result["error_code"] != "CONTACT_CANDIDATE_REQUEST_INVALID" && result["error_code"] != "CONTACT_CANDIDATE_PROTOCOL_UNSUPPORTED" {
			t.Fatal("invalid request escaped bounded candidate errors", result)
		}
	}
	unread := &candidateUnreadBody{}
	tooLarge := materialPost("/api/contacts/candidate-preview", token, valid)
	tooLarge.Body = unread
	tooLarge.ContentLength = contactcandidate.MaxRequestBytes + 1
	result := response(t, h, tooLarge, http.StatusBadRequest)
	if result["error_code"] != "CONTACT_CANDIDATE_REQUEST_INVALID" || unread.reads != 0 {
		t.Fatal("oversized body was read or misclassified")
	}
	if len(spy.calls) != before {
		t.Fatal("invalid candidate body reached the owner")
	}
}

func TestContactCandidatePublicErrorsPreserveOperationFacts(t *testing.T) {
	h, token, spy := applicationFixture(t)
	tests := []struct {
		path, body, code string
		status           int
	}{
		{"/api/contacts/candidate-preview", candidatePreviewBody(), "CONTACT_CANDIDATE_SOURCE_NOT_FOUND", 404},
		{"/api/contacts/candidate-preview", candidatePreviewBody(), "CONTACT_CANDIDATE_SOURCE_INCOMPLETE", 409},
		{"/api/contacts/candidate-preview", candidatePreviewBody(), "CONTACT_CANDIDATE_SOURCE_INVALID", 409},
		{"/api/contacts/candidate-preview", candidatePreviewBody(), "CONTACT_CANDIDATE_STALE", 409},
		{"/api/contacts/candidate-preview", candidatePreviewBody(), "CONTACT_CANDIDATE_PREVIEW_FAILED", 500},
		{"/api/contacts/confirm-candidate", candidateConfirmationBody(), "CONTACT_EXACT_MATCH_REQUIRES_LINK", 409},
		{"/api/contacts/confirm-candidate", candidateConfirmationBody(), "CONTACT_CONFLICT", 409},
		{"/api/contacts/confirm-candidate", candidateConfirmationBody(), "CONTACT_NOT_FOUND", 404},
		{"/api/contacts/confirm-candidate", candidateConfirmationBody(), "CONTACT_CANDIDATE_SAVE_FAILED", 500},
		{"/api/contacts/confirm-candidate", candidateConfirmationBody(), "CONTACT_CANDIDATE_SAVE_UNCONFIRMED", 503},
	}
	for _, test := range tests {
		spy.err = &ApplicationError{Code: test.code}
		spy.out = &ApplicationOutcome{Committed: test.code == "CONTACT_CANDIDATE_SAVE_FAILED", CommitUnknown: test.code == "CONTACT_CANDIDATE_SAVE_UNCONFIRMED", CleanupFailed: true, RecoveryRequired: true}
		result := response(t, h, materialPost(test.path, token, test.body), test.status)
		state := result["operation_state"].(map[string]any)
		if result["error_code"] != test.code || state["cleanup_failed"] != true || state["recovery_required"] != true {
			t.Fatal("candidate error or publication facts changed", test.code, result)
		}
		if test.code == "CONTACT_CANDIDATE_SAVE_UNCONFIRMED" && (state["known"] != false || state["committed"] != nil) {
			t.Fatal("uncertain commit was reported as a known rollback")
		}
	}
}
