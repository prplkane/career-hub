package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"careerhub.local/backend/internal/email/emailimport"
	"careerhub.local/backend/internal/skills/demandgaps"
)

type appSpy struct {
	calls     []string
	fields    map[string]any
	content   []byte
	revision  string
	out       *ApplicationOutcome
	err       *ApplicationError
	panicCall bool
	cancel    context.CancelFunc
	jobIntake *JobIntakeRequest
	intake    *IntakeRequest
	job       *JobRequest
}

func (s *appSpy) finish(name string) (*ApplicationOutcome, *ApplicationError) {
	s.calls = append(s.calls, name)
	if s.panicCall {
		panic("private-marker")
	}
	if s.cancel != nil {
		s.cancel()
	}
	return s.out, s.err
}
func (s *appSpy) actions() ApplicationActions {
	return ApplicationActions{
		Demand: func(_ context.Context, request demandgaps.Request) (json.RawMessage, error) {
			s.fields = map[string]any{"from": request.From, "to": request.To, "as_of": request.AsOf}
			out, e := s.finish("demand")
			if e != nil {
				return nil, errors.New("private-marker")
			}
			return out.Result, nil
		},
		VaultSetup: func(_ context.Context, request VaultSetupRequest) (*ApplicationOutcome, *ApplicationError) {
			s.fields = map[string]any{"apply": request.Apply, "confirmed_plan": request.ConfirmedPlan, "files": len(request.Files)}
			if request.NewProfileName != nil {
				s.fields["profile_name"] = *request.NewProfileName
			}
			return s.finish("vault.setup")
		},
		EmailImport: func(_ context.Context, _ *emailimport.Batch) (*ApplicationOutcome, *ApplicationError) {
			return s.finish("email.import")
		},
		Logs: func(_ context.Context, after string) (*ApplicationOutcome, *ApplicationError) {
			s.revision = after
			return s.finish("logs")
		},
		Jobs: func(_ context.Context, request JobRequest) (*ApplicationOutcome, *ApplicationError) {
			copied := request
			s.job = &copied
			s.fields = request.Fields
			s.revision = request.ExpectedCardRevision
			s.content = append([]byte(nil), request.Content...)
			return s.finish("jobs." + request.Operation + ":" + request.ID)
		},
		JobIntake: func(_ context.Context, request JobIntakeRequest) (*ApplicationOutcome, *ApplicationError) {
			s.jobIntake = &request
			return s.finish("jobs.intake")
		},
		Intake: func(_ context.Context, request IntakeRequest) (*ApplicationOutcome, *ApplicationError) {
			s.intake = &request
			return s.finish("intake." + request.Operation + ":" + request.SourceID)
		},
		EmailConnection: func(_ context.Context) (*ApplicationOutcome, *ApplicationError) {
			return s.finish("email.connection")
		},
		EmailConfigure: func(_ context.Context, request EmailConfigureRequest, runID string) (*ApplicationOutcome, *ApplicationError) {
			s.fields = map[string]any{"reconnect": request.Reconnect, "account": request.AccountAddress, "password_supplied": request.PasswordSupplied,
				"senders": request.JobSenders, "application_senders": request.ApplicationSenders,
				"scheduled": request.ScheduleEnabled, "interval": request.IntervalMinutes, "revision": request.ExpectedRevision,
				"run_id": runID}
			return s.finish("email.configure")
		},
		EmailRetire: func(_ context.Context, operation string, revision int64) (*ApplicationOutcome, *ApplicationError) {
			s.fields = map[string]any{"operation": operation, "revision": revision}
			return s.finish("email.retire")
		},
		EmailSync: func(_ context.Context, trigger, runID string) (*ApplicationOutcome, *ApplicationError) {
			s.fields = map[string]any{"trigger": trigger, "run_id": runID}
			return s.finish("email.sync")
		},
		EmailUpdates: func(_ context.Context, offset int64, limit int) (*ApplicationOutcome, *ApplicationError) {
			s.fields = map[string]any{"offset": offset, "limit": limit}
			return s.finish("email.updates")
		},
		EmailUpdateLink: func(_ context.Context, request EmailUpdateLinkRequest) (*ApplicationOutcome, *ApplicationError) {
			s.fields = map[string]any{"outcome_id": request.OutcomeID, "job_id": request.JobID, "revision": request.ExpectedRevision}
			return s.finish("email.link")
		},
		EmailCursorReset: func(_ context.Context, revision int64) (*ApplicationOutcome, *ApplicationError) {
			s.revision = strconv.FormatInt(revision, 10)
			return s.finish("email.reset")
		},
		EmailSenderRule: func(_ context.Context, request EmailSenderRuleRequest) (*ApplicationOutcome, *ApplicationError) {
			s.revision = strconv.FormatInt(request.ExpectedRevision, 10)
			s.fields = map[string]any{"sender": request.SenderAddress, "disposition": request.Disposition, "purpose": request.Purpose}
			return s.finish("email.sender")
		},
		Feed: func(_ context.Context, request FeedRequest) (*ApplicationOutcome, *ApplicationError) {
			s.fields = request.Overrides
			if request.Revision != nil {
				s.revision = string(request.Revision.(json.Number))
			}
			return s.finish("feed." + request.Operation + ":" + request.ID)
		},
		Competencies: func(_ context.Context, archived bool) (json.RawMessage, error) {
			name := "competencies.active"
			if archived {
				name = "competencies.archived"
			}
			out, e := s.finish(name)
			if e != nil {
				return nil, errors.New("private-marker")
			}
			return out.Result, nil
		},
		Static: func(_ context.Context, name string) ([]byte, error) {
			_, e := s.finish(name)
			if e != nil {
				return nil, errors.New("private-marker")
			}
			return []byte("asset"), nil
		},
		Runtime: func(context.Context) (json.RawMessage, error) {
			out, e := s.finish("runtime")
			if e != nil {
				return nil, errors.New("private-marker")
			}
			return out.Result, nil
		},
		Profile: func(_ context.Context, kind string, record map[string]any, revision string) (*ApplicationOutcome, *ApplicationError) {
			s.fields, s.revision = record, revision
			return s.finish(kind)
		},
		Policy: func(_ context.Context, save bool, value any, revision string) (*ApplicationOutcome, *ApplicationError) {
			s.revision = revision
			if save {
				s.fields, _ = value.(map[string]any)
				return s.finish("policy.save")
			}
			return s.finish("policy.load")
		},
		Helper: func(_ context.Context, op string, fields map[string]any) (*ApplicationOutcome, *ApplicationError) {
			s.fields = fields
			return s.finish(op)
		},
		VaultEdit: func(_ context.Context, edit VaultEditRequest) (*ApplicationOutcome, *ApplicationError) {
			s.fields, s.revision = edit.Record, edit.Revision
			return s.finish(edit.Kind + "." + edit.Operation + ":" + edit.ID)
		},
	}
}
func applicationFixture(t *testing.T) (http.Handler, string, *appSpy) {
	t.Helper()
	s := &appSpy{out: &ApplicationOutcome{Result: json.RawMessage(`{"saved":true}`)}}
	m := &materialSpy{}
	h, e := NewApplication(port, "0.1.7-dev", func() (map[string]any, error) { return map[string]any{}, nil }, emptyProfile, emptyDocuments, m.actions(), s.actions())
	if e != nil {
		t.Fatal(e)
	}
	return h, sessionTokenOf(h), s
}

func TestDemandReadRouteBoundaries(t *testing.T) {
	h, token, spy := applicationFixture(t)
	response(t, h, request("GET", "/api/demand-gaps?from=2026-08-01&to=2026-09-30&as_of=2026-09", token), 200)
	if len(spy.calls) != 1 || spy.calls[0] != "demand" || spy.fields["from"] != "2026-08-01" || spy.fields["to"] != "2026-09-30" || spy.fields["as_of"] != "2026-09" {
		t.Fatal("demand query not dispatched", spy.fields)
	}
	for _, suffix := range []string{"?from=2026-02-30", "?from=2026-09-02&to=2026-09-01", "?as_of=2026-13"} {
		response(t, h, request("GET", "/api/demand-gaps"+suffix, token), 400)
	}
	for _, method := range []string{"PUT", "POST", "DELETE"} {
		response(t, h, request(method, "/api/demand-gaps", token), 404)
	}
	response(t, h, request("GET", "/api/demand-gaps", ""), 403)
	if len(spy.calls) != 1 {
		t.Fatal("rejected request reached demand owner")
	}
	spy.err = &ApplicationError{Code: "private-marker"}
	result := response(t, h, request("GET", "/api/demand-gaps", token), 503)
	if result["error_code"] != "DEMAND_PROJECTION_UNAVAILABLE" {
		t.Fatal(result)
	}
}

func TestApplicationSessionDoesNotAdvertiseUnavailableResumeDigest(t *testing.T) {
	h, _, _ := applicationFixture(t)
	capabilities := response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)
	if capabilities["resume_digest"] != true || capabilities["material_integrity"] != float64(1) {
		t.Fatal("application session did not preserve the preview capability boundary", capabilities)
	}
}

func appPut(path, token, body string) *http.Request {
	r := materialPost(path, token, body)
	r.Method = "PUT"
	return r
}

func TestApplicationDispatchAndClosedEnvelopes(t *testing.T) {
	h, token, s := applicationFixture(t)
	for _, tc := range []struct{ path, body, op string }{
		{"/api/runtime", "", "runtime"},
		{"/api/vault/profile", `{"expected_revision":"sha256:test","record":{"name":"Synthetic Ω"}}`, "profile"},
		{"/api/vault/eligibility", `{"expected_revision":"sha256:test","record":{"requires_future_sponsorship":false}}`, "eligibility"},
		{"/api/hard-blocker-policy", "", "policy.load"},
		{"/api/hard-blocker-policy", `{"contract_version":1,"expected_revision":"sha256:test","classes":{"active_clearance":false}}`, "policy.save"},
		{"/api/application-helper/configuration", "", "configuration.load"},
		{"/api/application-helper/configuration", `{"contract_version":2,"expected_revision":"sha256:test","enabled_fields":["email"]}`, "configuration.save"},
		{"/api/application-demographics", "", "demographics.load"},
		{"/api/application-demographics", `{"contract_version":1,"section":"US","expected_revision":"sha256:test","answers":{"gender":"Prefer not to answer"}}`, "demographics.save"},
	} {
		r := request("GET", tc.path, token)
		if tc.body != "" {
			r = appPut(tc.path, token, tc.body)
		}
		response(t, h, r, 200)
		if s.calls[len(s.calls)-1] != tc.op {
			t.Fatal("wrong fixed callback", tc.op)
		}
		if _, present := s.fields["contract_version"]; present {
			t.Fatal("HTTP envelope leaked into domain payload")
		}
	}
	before := len(s.calls)
	for _, path := range []string{"/api/vault/profile", "/api/vault/eligibility"} {
		if result := response(t, h, request("GET", path, token), 200); result["revision"] != "synthetic" {
			t.Fatal("existing Vault reader not used")
		}
	}
	for _, tc := range []struct{ path, body, code string }{
		{"/api/vault/profile", `{}`, "VAULT_RECORD_VALIDATION_FAILED"},
		{"/api/vault/profile", `{"expected_revision":null,"record":{}}`, "VAULT_RECORD_VALIDATION_FAILED"},
		{"/api/vault/profile", `{"expected_revision":"a","record":null}`, "VAULT_RECORD_VALIDATION_FAILED"},
		{"/api/vault/eligibility", `{"expected_revision":"a","record":{},"path":"private"}`, "VAULT_RECORD_VALIDATION_FAILED"},
		{"/api/hard-blocker-policy", `{"contract_version":true,"expected_revision":"a","classes":{}}`, "HARD_BLOCKER_POLICY_INVALID"},
		{"/api/application-helper/configuration", `{"contract_version":1,"expected_revision":"a","enabled_fields":[]}`, "APPLICATION_HELPER_CONFIGURATION_INVALID"},
		{"/api/application-demographics", `{"contract_version":2,"expected_revision":"a","answers":{}}`, "APPLICATION_DEMOGRAPHICS_INVALID"},
		{"/api/application-demographics", `{"contract_version":1,"expected_revision":"a","answers":{}}`, "APPLICATION_DEMOGRAPHICS_INVALID"},
		{"/api/application-demographics", `{"contract_version":1,"section":"other","expected_revision":"a","answers":{}}`, "APPLICATION_DEMOGRAPHICS_INVALID"},
	} {
		if result := response(t, h, appPut(tc.path, token, tc.body), 400); result["error_code"] != tc.code {
			t.Fatal("wrong fixed error", result)
		}
	}
	if len(s.calls) != before {
		t.Fatal("invalid envelope reached owner")
	}
}

func TestApplicationAuthorizationAndBoundsBeforeCallbacks(t *testing.T) {
	h, token, s := applicationFixture(t)
	paths := []string{"/api/runtime", "/api/vault/profile", "/api/vault/eligibility", "/api/hard-blocker-policy", "/api/application-helper/configuration", "/api/application-demographics"}
	for _, path := range paths {
		for _, mode := range []string{"token", "origin", "host", "fetch", "duplicate-token"} {
			r := appPut(path, token, `{}`)
			spy := &materialBodySpy{}
			r.Body = spy
			switch mode {
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
			if spy.reads != 0 {
				t.Fatal("denied body was read")
			}
		}
	}
	for _, path := range paths[1:] {
		r := appPut(path, token, `{}`)
		r.ContentLength = MaxMaterialRequestBytes + 1
		response(t, h, r, 413)
		r = appPut(path, token, `{}`)
		r.TransferEncoding = []string{"chunked"}
		response(t, h, r, 400)
		r = appPut(path, token, `{}`)
		r.Header.Set("Content-Type", "text/plain")
		response(t, h, r, 415)
		response(t, h, appPut(path, token, "{} {}"), 400)
	}
	for _, path := range paths[4:] {
		r := appPut(path, token, `{}`)
		r.ContentLength = 16385
		response(t, h, r, 413)
	}
	if len(s.calls) != 0 {
		t.Fatal("denial reached callback")
	}
}

func TestApplicationLogsExactAuthenticatedCursorRoute(t *testing.T) {
	h, token, s := applicationFixture(t)
	s.out = &ApplicationOutcome{Result: json.RawMessage(`{"cursor":"dl1:3:ne:000001:0","events":[],"has_more":false}`)}
	result := response(t, h, request("GET", "/api/logs?after=dl1%3A3%3Ane%3A000001%3A0", token), 200)
	if result["cursor"] != "dl1:3:ne:000001:0" || s.revision != "dl1:3:ne:000001:0" || s.calls[len(s.calls)-1] != "logs" {
		t.Fatalf("diagnostic read did not retain its opaque cursor: %+v %q", result, s.revision)
	}
	dl3 := "dl3:3:ne:000001:0:nj:000001:0:ni:1:e3b0c44298fc1c149afbf4c8996fb924:1"
	response(t, h, request("GET", "/api/logs?after="+strings.ReplaceAll(dl3, ":", "%3A"), token), 200)
	if s.revision != dl3 {
		t.Fatalf("complete diagnostic cursor was truncated: %q", s.revision)
	}
	before := len(s.calls)
	for _, target := range []string{"/api/logs?after=0&after=1", "/api/logs?other=1", "/api/logs?after=", "/api/logs?after=0;other=1"} {
		if result := response(t, h, request("GET", target, token), 400); result["error_code"] != "DIAGNOSTIC_CURSOR_INVALID" {
			t.Fatalf("invalid diagnostic query accepted: %s %+v", target, result)
		}
	}
	response(t, h, request("POST", "/api/logs", token), 404)
	response(t, h, request("GET", "/api/logs", ""), 403)
	if len(s.calls) != before {
		t.Fatal("denied diagnostic request reached owner")
	}
}

func TestApplicationFailurePreservesPublicationFacts(t *testing.T) {
	for _, mode := range []string{"conflict", "after-commit", "bad-response", "panic", "unknown-code", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			h, token, s := applicationFixture(t)
			r := appPut("/api/vault/profile", token, `{"expected_revision":"a","record":{}}`)
			status := 500
			s.out.Committed = true
			switch mode {
			case "conflict":
				s.out.Committed = false
				s.err = &ApplicationError{Code: "VAULT_EDITOR_REVISION_CONFLICT"}
				status = 409
			case "after-commit":
				s.out.CleanupFailed = true
				s.err = &ApplicationError{Code: "VAULT_OPERATION_FAILED"}
			case "bad-response":
				s.out.Result = json.RawMessage(`[]`)
			case "panic":
				s.panicCall = true
			case "unknown-code":
				s.err = &ApplicationError{Code: "private-marker"}
			case "cancel":
				ctx, cancel := context.WithCancel(r.Context())
				defer cancel()
				r = r.WithContext(ctx)
				s.cancel = cancel
				status = 408
			}
			result := response(t, h, r, status)
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "private-marker") || result["retryable"] != false {
				t.Fatal("unsafe error response")
			}
			state := result["operation_state"].(map[string]any)
			if mode == "panic" {
				if state["known"] != false || state["committed"] != nil {
					t.Fatal("panic claimed rollback")
				}
			} else if state["known"] != true || state["committed"] != s.out.Committed || state["cleanup_failed"] != s.out.CleanupFailed {
				t.Fatal("lost operation state")
			}
		})
	}
}

func TestApplicationExactStaticRoutes(t *testing.T) {
	h, token, s := applicationFixture(t)
	for path, asset := range staticFiles {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("GET", path, ""))
		if w.Code != 200 || w.Body.String() != "asset" || w.Header().Get("Content-Type") != asset.Type || w.Header().Get("Content-Security-Policy") != contentSecurityPolicy || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("asset boundary", path)
		}
		if s.calls[len(s.calls)-1] != asset.Name {
			t.Fatal("inferred asset path")
		}
	}
	before := len(s.calls)
	for _, path := range []string{"/index.html", "/../VERSION", "/%61pp.js", "/src/intake_ui_static/index.html", "/career_vault/profile.yaml", "/app.js/", "/styles/../app.js", "/api/runtime/", "/api/vault/profile/other", "http://127.0.0.1:8766/"} {
		response(t, h, request("GET", path, token), 404)
	}
	response(t, h, request("POST", "/", token), 404)
	r := request("GET", "/", "")
	r.Host = "attacker.invalid"
	response(t, h, r, 403)
	if len(s.calls) != before {
		t.Fatal("unlisted asset reached reader")
	}
	s.panicCall = true
	result := response(t, h, request("GET", "/", ""), 500)
	if strings.Contains(result["error"].(string), "private") {
		t.Fatal("static panic leak")
	}
	readonly, _ := fixture(t, func() (map[string]any, error) { return map[string]any{}, nil })
	response(t, readonly, request("GET", "/", ""), 404)
}

func TestApplicationJSONDoesNotRepairMalformedUnicode(t *testing.T) {
	h, token, s := applicationFixture(t)
	for _, raw := range []string{
		`{"expected_revision":"a","record":{"name":"\ud800"}}`,
		`{"expected_revision":"a","record":{"name":"\udc00"}}`,
		`{"expected_revision":"a","record":{"name":"\ud800","name":"Synthetic"}}`,
		`{"expected_revision":"a","record":{"name":"\ud800"},"record":{"name":"Synthetic"}}`,
	} {
		response(t, h, appPut("/api/vault/profile", token, raw), 400)
	}
	if len(s.calls) != 0 {
		t.Fatal("malformed Unicode reached writer")
	}
	for _, value := range []string{`\ud83d\ude00`, `\\ud800`} {
		response(t, h, appPut("/api/vault/profile", token, `{"expected_revision":"a","record":{"name":"`+value+`"}}`), 200)
	}
	if s.fields["name"] != `\ud800` {
		t.Fatal("literal escape was changed")
	}
}
