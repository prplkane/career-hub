package httpapi

import (
	"careerhub.local/backend/internal/intake/intakeview"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const intakeSource = "src_fictional_1234567890"

func TestIntakeApprovedImportBodylessActionsAndStatuses(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, test := range []struct{ method, suffix, operation string }{
		{http.MethodPost, "/keep-for-applications", "review.keep"}, {http.MethodDelete, "/source", "review.remove_source"},
	} {
		target := "/api/reviews/" + intakeSource + test.suffix
		spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"resume":{"id":"resume_one"},"already_saved":false}`), Committed: true}
		status := 200
		if test.operation == "review.keep" {
			status = 201
		}
		response(t, h, vaultRequest(test.method, target, token, ""), status)
		if spy.intake.Operation != test.operation || spy.intake.SourceID != intakeSource {
			t.Fatal("wrong approved action")
		}
		if test.operation == "review.keep" {
			spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"resume":{"id":"resume_one"},"already_saved":true}`)}
			response(t, h, vaultRequest(test.method, target, token, ""), 200)
		}
		before := len(spy.calls)
		response(t, h, vaultRequest(test.method, target, token, `{}`), 404)
		response(t, h, vaultRequest(test.method, target+"?", token, ""), 404)
		response(t, h, vaultRequest(test.method, target+"/", token, ""), 404)
		response(t, h, vaultRequest(test.method, target, "wrong", ""), 403)
		if len(spy.calls) != before {
			t.Fatal("invalid lifecycle request reached owner")
		}
	}
}

func TestIntakeApprovalApplyStrictAuthenticatedRevisions(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"status":"applied"}`), Committed: true}
	target := "/api/reviews/" + intakeSource + "/apply-approval"
	hash := strings.Repeat("a", 64)
	body := `{"expected_review_revision":"sha256:` + hash + `","expected_vault_revision":"sha256:` + hash + `","expected_registry_revision":"` + hash + `"}`
	response(t, h, vaultRequest(http.MethodPost, target, token, body), 200)
	if spy.intake.Operation != "review.apply" || spy.intake.SourceID != intakeSource || spy.intake.ExpectedReviewRevision == nil || *spy.intake.ExpectedReviewRevision != "sha256:"+hash || spy.intake.ExpectedVaultRevision != "sha256:"+hash || spy.intake.ExpectedRegistryRevision != hash {
		t.Fatal("wrong approval dispatch")
	}
	before := len(spy.calls)
	for _, bad := range []string{`{}`, strings.Replace(body, `"expected_vault_revision"`, `"extra"`, 1), strings.Replace(body, `"expected_review_revision":`, `"expected_review_revision":null,"expected_review_revision":`, 1), strings.Replace(body, hash, strings.Repeat("A", 64), 1), strings.TrimSuffix(body, "}") + `,"extra":1}`} {
		response(t, h, vaultRequest(http.MethodPost, target, token, bad), 400)
	}
	for _, path := range []string{target + "?", target + "?x=1", target + "/", strings.Replace(target, "apply-approval", "%61pply-approval", 1)} {
		response(t, h, vaultRequest(http.MethodPost, path, token, body), 404)
	}
	r := vaultRequest(http.MethodPost, target, "wrong", body)
	r.Body = io.NopCloser(panicIntakeReader{})
	response(t, h, r, 403)
	r = vaultRequest(http.MethodPost, target, token, body)
	r.ContentLength = 16385
	response(t, h, r, 400)
	if len(spy.calls) != before {
		t.Fatal("invalid approval reached owner")
	}
	for code, status := range map[string]int{"APPROVAL_ALREADY_APPLIED": 409, "VAULT_REVISION_CONFLICT": 409, "SKILL_REGISTRY_REVISION_CONFLICT": 409, "APPROVAL_WRITE_UNCERTAIN": 503, "APPROVAL_DIAGNOSTIC_FAILED": 503, "APPROVAL_INVALID": 400} {
		spy.out = &ApplicationOutcome{Committed: true, Partial: true, RecoveryRequired: true}
		spy.err = &ApplicationError{Code: code, Category: "intake_review"}
		got := response(t, h, vaultRequest(http.MethodPost, target, token, body), status)
		if got["operation_state"] == nil {
			t.Fatal("missing publication facts")
		}
	}
}

func TestIntakeApprovalPreviewExactAuthenticatedRead(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"status":"preview"}`)}
	target := "/api/reviews/" + intakeSource + "/approval-preview"
	response(t, h, vaultRequest(http.MethodGet, target, token, ""), http.StatusOK)
	if spy.intake.Operation != "review.preview" || spy.intake.SourceID != intakeSource {
		t.Fatal("incorrect preview route")
	}
	before := len(spy.calls)
	for _, path := range []string{target + "/", target + "?", target + "?x=1", strings.Replace(target, "approval-preview", "%61pproval-preview", 1)} {
		response(t, h, vaultRequest(http.MethodGet, path, token, ""), http.StatusNotFound)
	}
	response(t, h, vaultRequest(http.MethodGet, target, token, `{}`), http.StatusNotFound)
	response(t, h, vaultRequest(http.MethodPost, target, token, `{}`), http.StatusNotFound)
	response(t, h, vaultRequest(http.MethodGet, target, "wrong", ""), http.StatusForbidden)
	if len(spy.calls) != before {
		t.Fatal("invalid preview reached owner")
	}
	for code, status := range map[string]int{"APPROVAL_PREVIEW_INCOMPLETE": 409, "APPROVAL_PREVIEW_INPUT_CHANGED": 409, "APPROVAL_PREVIEW_UNAVAILABLE": 503} {
		spy.err = &ApplicationError{Code: code, Category: "intake_review"}
		response(t, h, vaultRequest(http.MethodGet, target, token, ""), status)
	}
}

func TestIntakeReviewSaveClosedRevisionAndValidationResponse(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"source_id":"` + intakeSource + `"}`), Committed: true}
	target := "/api/reviews/" + intakeSource
	revision := "sha256:" + strings.Repeat("a", 64)
	body := `{"review":{"status":"draft"},"expected_review_revision":"` + revision + `"}`
	response(t, h, vaultRequest(http.MethodPut, target, token, body), http.StatusOK)
	if spy.intake.Operation != "review.save" || spy.intake.SourceID != intakeSource || spy.intake.ExpectedReviewRevision == nil || *spy.intake.ExpectedReviewRevision != revision || string(spy.intake.Review) != `{"status":"draft"}` {
		t.Fatal("wrong save dispatch")
	}
	before := len(spy.calls)
	for _, body := range []string{`{}`, `{"review":{},"expected_review_revision":null}`, `{"review":{},"expected_review_revision":"bad"}`, `{"review":null,"expected_review_revision":"` + revision + `"}`, `{"review":{},"review":{},"expected_review_revision":"` + revision + `"}`, `{"review":{},"extra":1,"expected_review_revision":"` + revision + `"}`} {
		response(t, h, vaultRequest(http.MethodPut, target, token, body), http.StatusBadRequest)
	}
	r := vaultRequest(http.MethodPut, target, token, body)
	r.ContentLength = MaxMaterialRequestBytes + 1
	response(t, h, r, http.StatusBadRequest)
	r = vaultRequest(http.MethodPut, target, "wrong", body)
	r.Body = io.NopCloser(panicIntakeReader{})
	response(t, h, r, http.StatusForbidden)
	if len(spy.calls) != before {
		t.Fatal("invalid review save reached owner")
	}
	spy.out = &ApplicationOutcome{ReviewValidation: &intakeview.ReviewValidation{Status: "error", ErrorCount: 1}}
	spy.err = &ApplicationError{Code: "REVIEW_INTEGRITY_FAILED", Category: "intake_review"}
	got := response(t, h, vaultRequest(http.MethodPut, target, token, body), http.StatusBadRequest)
	if got["validation"] == nil {
		t.Fatal("missing validation")
	}
	spy.err.Code = "REVIEW_REVISION_CONFLICT"
	got = response(t, h, vaultRequest(http.MethodPut, target, token, body), http.StatusConflict)
	if got["validation"] != nil {
		t.Fatal("validation leaked into unrelated failure")
	}
}

func TestIntakeUploadCheckEnvelopeAndAuth(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"prior_imports":[],"same_name_conflict":false}`)}
	makeRequest := func() *http.Request {
		r := vaultRequest(http.MethodPost, "/api/upload/check", token, "fictional content")
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("X-Source-Name", "private-name.txt")
		return r
	}
	response(t, h, makeRequest(), http.StatusOK)
	if spy.intake == nil || spy.intake.Operation != "source.check" || string(spy.intake.Content) != "fictional content" || spy.intake.EncodedName != "private-name.txt" {
		t.Fatal("wrong upload dispatch")
	}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if got := fmt.Sprintf(verb, *spy.intake); got != "IntakeRequest{redacted}" {
			t.Fatal("private request not redacted")
		}
	}
	before := len(spy.calls)
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("X-Source-Name") },
		func(r *http.Request) { r.Header.Add("X-Source-Name", "other.txt") },
		func(r *http.Request) { r.Header.Add("Content-Type", "application/octet-stream") },
		func(r *http.Request) { r.Header.Set("Content-Type", "application/json") },
		func(r *http.Request) { r.Header.Set("X-Source-Reimport", "process-again") },
		func(r *http.Request) { r.ContentLength = 0 },
		func(r *http.Request) { r.ContentLength = -1 },
		func(r *http.Request) { r.ContentLength = (10 << 20) + 1 },
		func(r *http.Request) { r.TransferEncoding = []string{"chunked"} },
		func(r *http.Request) { r.ContentLength++ },
		func(r *http.Request) { r.ContentLength-- },
		func(r *http.Request) { r.Body = nil },
	} {
		r := makeRequest()
		change(r)
		got := response(t, h, r, http.StatusBadRequest)
		if got["error_code"] != "SOURCE_UPLOAD_CHECK_FAILED" {
			t.Fatal(got)
		}
	}
	for _, target := range []string{"/api/upload/check/", "/api/upload/%63heck", "/api/upload/check?x=1", "/api/upload/check?"} {
		r := makeRequest()
		r.URL, _ = r.URL.Parse(target)
		response(t, h, r, http.StatusNotFound)
	}
	r := makeRequest()
	r.Header.Set(CapabilityHeader, "wrong")
	r.Body = io.NopCloser(panicIntakeReader{})
	response(t, h, r, http.StatusForbidden)
	if len(spy.calls) != before {
		t.Fatal("invalid upload reached owner")
	}
}

type panicIntakeReader struct{}

func TestIntakeExtractionClosedEnvelope(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"source_id":"src_test_123"}`), Committed: true}
	for _, body := range []string{`{"source":"file.txt"}`, `{"source":"file.txt","overwrite":true}`, `{"source":"file.txt","overwrite":false}`} {
		response(t, h, vaultRequest(http.MethodPost, "/api/extract", token, body), http.StatusOK)
		if spy.intake.Operation != "source.extract" || spy.intake.SourceName != "file.txt" || spy.intake.Overwrite != strings.Contains(body, "true") {
			t.Fatal("wrong extraction request")
		}
	}
	before := len(spy.calls)
	for _, body := range []string{`{}`, `{"source":null}`, `{"source":""}`, `{"source":"file.txt","overwrite":null}`, `{"source":"file.txt","overwrite":1}`, `{"source":"file.txt","source":"other.txt"}`, `{"source":"file.txt","extra":1}`} {
		response(t, h, vaultRequest(http.MethodPost, "/api/extract", token, body), http.StatusBadRequest)
	}
	r := vaultRequest(http.MethodPost, "/api/extract", "wrong", `{"source":"file.txt"}`)
	r.Body = io.NopCloser(panicIntakeReader{})
	response(t, h, r, http.StatusForbidden)
	for _, target := range []string{"/api/extract/", "/api/%65xtract", "/api/extract?overwrite=true"} {
		response(t, h, vaultRequest(http.MethodPost, target, token, `{"source":"file.txt"}`), http.StatusNotFound)
	}
	if len(spy.calls) != before {
		t.Fatal("invalid extract reached owner")
	}
}

func TestIntakeUploadMutationRouteAndReimportMode(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"name":"resume.txt","path":"career_vault/raw/resume.txt"}`), Committed: true}
	makeRequest := func(mode string) *http.Request {
		r := vaultRequest(http.MethodPost, "/api/upload", token, "fictional")
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("X-Source-Name", "resume.txt")
		if mode != "" {
			r.Header.Set("X-Source-Reimport", mode)
		}
		return r
	}
	for _, mode := range []string{"", "process-again", " PROCESS-AGAIN "} {
		response(t, h, makeRequest(mode), http.StatusCreated)
		if spy.intake.Operation != "source.upload" || spy.intake.Reimport != (mode != "") {
			t.Fatal("wrong upload mode")
		}
	}
	before := len(spy.calls)
	response(t, h, makeRequest("overwrite"), http.StatusBadRequest)
	r := makeRequest("process-again")
	r.Header.Add("X-Source-Reimport", "process-again")
	response(t, h, r, http.StatusBadRequest)
	r = makeRequest("")
	r.Header.Set(CapabilityHeader, "wrong")
	r.Body = io.NopCloser(panicIntakeReader{})
	response(t, h, r, http.StatusForbidden)
	response(t, h, request(http.MethodGet, "/api/upload", token), http.StatusNotFound)
	if len(spy.calls) != before {
		t.Fatal("invalid upload reached owner")
	}
	spy.out = &ApplicationOutcome{CommitUnknown: true, RunID: "20260921T010203.000000Z_01234567"}
	spy.err = &ApplicationError{Code: "SOURCE_UPLOAD_WRITE_UNCERTAIN", Category: "intake_upload"}
	got := response(t, h, makeRequest(""), http.StatusServiceUnavailable)
	state, ok := got["operation_state"].(map[string]any)
	if !ok || state["known"] != false || state["committed"] != nil || got["run_id"] != spy.out.RunID {
		t.Fatal("lost publication uncertainty", got)
	}
}

func (panicIntakeReader) Read([]byte) (int, error) { panic("unauthenticated body read") }

func TestIntakeSourcesPaginationAndClosedRead(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"items":[],"total":0,"next_cursor":null}`)}
	response(t, h, request(http.MethodGet, "/api/sources?limit=3&cursor=offset:7", token), http.StatusOK)
	if spy.intake == nil || spy.intake.Operation != "source.list" || spy.intake.Offset != 7 || spy.intake.Limit != 3 {
		t.Fatal(spy.intake)
	}
	response(t, h, request(http.MethodGet, "/api/sources", token), http.StatusOK)
	if spy.intake.Offset != 0 || spy.intake.Limit != 20 {
		t.Fatal(spy.intake)
	}
	before := len(spy.calls)
	for _, query := range []string{"limit=101", "limit=0", "limit=a", "cursor=offset:-1", "cursor=offset:9999999999999999999999", "cursor=bad", "limit=1&limit=2", "path=private", "cursor=%ZZ"} {
		response(t, h, request(http.MethodGet, "/api/sources?"+query, token), http.StatusBadRequest)
	}
	for _, path := range []string{"/api/sources/", "/api/%73ources"} {
		response(t, h, request(http.MethodGet, path, token), http.StatusNotFound)
	}
	response(t, h, request(http.MethodGet, "/api/sources", "wrong"), http.StatusForbidden)
	response(t, h, vaultRequest(http.MethodGet, "/api/sources", token, `{}`), http.StatusNotFound)
	if len(spy.calls) != before {
		t.Fatal("invalid source read reached owner")
	}
}

func TestIntakeExtractionCollectionPagination(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"items":[],"total":0,"next_cursor":null}`)}
	response(t, h, request(http.MethodGet, "/api/extractions?limit=5&cursor=offset:9", token), http.StatusOK)
	if spy.intake == nil || spy.intake.Operation != "extraction.list" || spy.intake.Offset != 9 || spy.intake.Limit != 5 {
		t.Fatal(spy.intake)
	}
	before := len(spy.calls)
	response(t, h, request(http.MethodGet, "/api/extractions?limit=101", token), http.StatusBadRequest)
	response(t, h, request(http.MethodGet, "/api/extractions?cursor=offset:-1", token), http.StatusBadRequest)
	response(t, h, request(http.MethodGet, "/api/extractions", "wrong"), http.StatusForbidden)
	response(t, h, request(http.MethodGet, "/api/extractions/", token), http.StatusNotFound)
	response(t, h, vaultRequest(http.MethodGet, "/api/extractions", token, `{}`), http.StatusNotFound)
	if len(spy.calls) != before {
		t.Fatal("invalid extraction list reached owner")
	}
}

func TestIntakeReadRoutesUseExactAuthenticatedOwner(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"source_id":"` + intakeSource + `"}`)}
	for _, target := range []struct {
		path string
		op   string
	}{
		{"/api/extractions/" + intakeSource, "extraction.read"},
		{"/api/reviews/" + intakeSource, "review.read"},
	} {
		response(t, h, request(http.MethodGet, target.path, token), http.StatusOK)
		if spy.intake == nil || spy.intake.Operation != target.op || spy.intake.SourceID != intakeSource ||
			spy.calls[len(spy.calls)-1] != "intake."+target.op+":"+intakeSource {
			t.Fatal("wrong intake read dispatch", target, spy.intake, spy.calls)
		}
	}

	before := len(spy.calls)
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/extractions/"+intakeSource+"?private=1", token),
		request(http.MethodGet, "/api/reviews/not-a-source", token),
		request(http.MethodGet, "/api/reviews/src_fictional_%31%32%33%34%35", token),
		request(http.MethodPost, "/api/reviews/"+intakeSource, token),
		vaultRequest(http.MethodGet, "/api/extractions/"+intakeSource, token, `{}`),
	} {
		response(t, h, r, http.StatusNotFound)
	}
	response(t, h, request(http.MethodGet, "/api/reviews/"+intakeSource, "wrong"), http.StatusForbidden)
	if len(spy.calls) != before {
		t.Fatal("refused intake read reached owner", spy.calls[before:])
	}
}

func TestIntakeBuildAcceptsOnlyClosedRevisionEnvelope(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"source_id":"` + intakeSource + `","review_revision":"sha256:` + strings.Repeat("a", 64) + `"}`)}
	validRevision := "sha256:" + strings.Repeat("b", 64)
	for _, body := range []string{
		`{"source_id":"` + intakeSource + `"}`,
		`{"source_id":"` + intakeSource + `","expected_review_revision":null}`,
		`{"source_id":"` + intakeSource + `","expected_review_revision":"` + validRevision + `"}`,
	} {
		response(t, h, vaultRequest(http.MethodPost, "/api/reviews/build", token, body), http.StatusOK)
		if spy.intake == nil || spy.intake.Operation != "review.build" || spy.intake.SourceID != intakeSource {
			t.Fatal("wrong review build dispatch", spy.intake)
		}
		if strings.Contains(body, validRevision) {
			if spy.intake.ExpectedReviewRevision == nil || *spy.intake.ExpectedReviewRevision != validRevision {
				t.Fatal("revision was not retained", spy.intake)
			}
		} else if spy.intake.ExpectedReviewRevision != nil {
			t.Fatal("absent revision became material", spy.intake)
		}
	}

	before := len(spy.calls)
	for _, body := range []string{
		`{}`,
		`{"source_id":"bad"}`,
		`{"source_id":"` + intakeSource + `","expected_review_revision":"bad"}`,
		`{"source_id":"` + intakeSource + `","private_path":"x"}`,
		`{"source_id":"` + intakeSource + `","source_id":"src_other_12345"}`,
	} {
		got := response(t, h, vaultRequest(http.MethodPost, "/api/reviews/build", token, body), http.StatusBadRequest)
		if got["error_code"] != "INTAKE_REVIEW_BUILD_REQUEST_INVALID" {
			t.Fatal("wrong invalid-build code", got)
		}
	}
	unknown := vaultRequest(http.MethodPost, "/api/reviews/build", token, `{"source_id":"`+intakeSource+`"}`)
	unknown.ContentLength = -1
	unknown.TransferEncoding = []string{"chunked"}
	response(t, h, unknown, http.StatusBadRequest)
	if len(spy.calls) != before {
		t.Fatal("refused review build reached owner", spy.calls[before:])
	}
}

func TestIntakeSuggestAcceptsOnlyExactRevisionEnvelope(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"source_id":"` + intakeSource + `","review_revision":"sha256:` + strings.Repeat("a", 64) + `"}`)}
	validRevision := "sha256:" + strings.Repeat("b", 64)
	response(t, h, vaultRequest(http.MethodPost, "/api/reviews/"+intakeSource+"/suggest", token, `{"expected_review_revision":"`+validRevision+`"}`), http.StatusOK)
	if spy.intake == nil || spy.intake.Operation != "review.suggest" || spy.intake.SourceID != intakeSource ||
		spy.intake.ExpectedReviewRevision == nil || *spy.intake.ExpectedReviewRevision != validRevision {
		t.Fatal("wrong review suggestion dispatch", spy.intake)
	}

	before := len(spy.calls)
	for _, request := range []*http.Request{
		vaultRequest(http.MethodPost, "/api/reviews/"+intakeSource+"/suggest", token, `{}`),
		vaultRequest(http.MethodPost, "/api/reviews/"+intakeSource+"/suggest", token, `{"expected_review_revision":null}`),
		vaultRequest(http.MethodPost, "/api/reviews/"+intakeSource+"/suggest", token, `{"expected_review_revision":"bad"}`),
		vaultRequest(http.MethodPost, "/api/reviews/"+intakeSource+"/suggest", token, `{"expected_review_revision":"`+validRevision+`","source_id":"`+intakeSource+`"}`),
		vaultRequest(http.MethodPost, "/api/reviews/"+intakeSource+"/suggest", token, `{"expected_review_revision":"`+validRevision+`","expected_review_revision":"`+validRevision+`"}`),
	} {
		got := response(t, h, request, http.StatusBadRequest)
		if got["error_code"] != "INTAKE_REVIEW_SUGGEST_REQUEST_INVALID" {
			t.Fatal("wrong invalid-suggestion code", got)
		}
	}
	response(t, h, request(http.MethodGet, "/api/reviews/"+intakeSource+"/suggest", token), http.StatusNotFound)
	response(t, h, vaultRequest(http.MethodPost, "/api/reviews/"+intakeSource+"/suggest?private=1", token, `{"expected_review_revision":"`+validRevision+`"}`), http.StatusNotFound)
	response(t, h, vaultRequest(http.MethodPost, "/api/reviews/src_fictional_%31%32%33%34%35/suggest", token, `{"expected_review_revision":"`+validRevision+`"}`), http.StatusNotFound)
	unknown := vaultRequest(http.MethodPost, "/api/reviews/"+intakeSource+"/suggest", token, `{"expected_review_revision":"`+validRevision+`"}`)
	unknown.ContentLength = -1
	unknown.TransferEncoding = []string{"chunked"}
	response(t, h, unknown, http.StatusBadRequest)
	if len(spy.calls) != before {
		t.Fatal("refused review suggestion reached owner", spy.calls[before:])
	}
}

func TestIntakeFailuresPreserveSpecificStatusAndFacts(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"INTAKE_EXTRACTION_NOT_FOUND", http.StatusNotFound},
		{"INTAKE_REVIEW_BUILD_REVISION_CONFLICT", http.StatusConflict},
		{"INTAKE_REVIEW_SUGGEST_REVISION_CONFLICT", http.StatusConflict},
		{"INTAKE_REVIEW_INVALID", http.StatusUnprocessableEntity},
		{"INTAKE_VIEW_RESULT_LIMIT", http.StatusRequestEntityTooLarge},
		{"INTAKE_REVIEW_BUILD_COMMITTED_UNVERIFIED", http.StatusServiceUnavailable},
		{"INTAKE_REVIEW_SUGGEST_COMMITTED_UNVERIFIED", http.StatusServiceUnavailable},
	} {
		committed := strings.HasSuffix(tc.code, "COMMITTED_UNVERIFIED")
		spy.out = &ApplicationOutcome{Committed: committed, Partial: committed}
		spy.err = &ApplicationError{Code: tc.code, Category: "intake_review"}
		got := response(t, h, request(http.MethodGet, "/api/extractions/"+intakeSource, token), tc.status)
		state := got["operation_state"].(map[string]any)
		if got["error_code"] != tc.code || got["diagnostic_category"] != "intake_review" ||
			state["committed"] != spy.out.Committed || state["partial"] != spy.out.Partial {
			t.Fatal("intake error mapping changed", tc, got)
		}
	}
}
