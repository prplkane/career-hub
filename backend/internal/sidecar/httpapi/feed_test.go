package httpapi

import (
	"strings"
	"testing"
)

func TestFeedWideQueryReportsActionableLimitWithoutPartialCounts(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{}
	spy.err = &ApplicationError{Code: "DISCOVERY_QUERY_TOO_BROAD"}
	out := response(t, h, request("GET", "/api/discoveries?queue=review", token), 422)
	if out["items"] != nil || out["total"] != nil || !strings.Contains(out["suggestion"].(string), "Narrow the search") {
		t.Fatal("query limit disguised as complete results", out)
	}
}

func TestFeedQueryContract(t *testing.T) {
	p, ok := feedPage("state=inbox,shortlisted&state=inbox&source_kind=&source_kind=email_file&q=Python&limit=10&cursor=offset:20&sort=title_asc")
	if !ok || len(p.States) != 2 || len(p.SourceKinds) != 1 || p.Search != "Python" || p.Offset != 20 || p.Limit != 10 || p.Sort != "title_asc" {
		t.Fatalf("query: %+v %v", p, ok)
	}
	p, ok = feedPage("limit=&limit=20&state=%20inbox%20")
	if !ok || p.Limit != 20 || p.States[0] != "inbox" {
		t.Fatal("first nonblank source semantics")
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=null", "limit=1.0", "limit=1e1", "cursor=20", "cursor=offset:-1", "cursor=offset:9223372036854775808", "state=applied", "source_kind=sql", "sort=random", "q=%FF", "q=%ZZ", "observed_after=not-a-date"} {
		if _, ok := feedPage(query); ok {
			t.Fatal("accepted", query)
		}
	}
}
func TestFeedRoutesAndEnvelopes(t *testing.T) {
	h, token, s := applicationFixture(t)
	for _, tc := range []struct{ method, path, body, operation string }{
		{"GET", "/api/discoveries", "", "list:"},
		{"GET", "/api/discoveries/exact_1", "", "detail:exact_1"},
		{"PUT", "/api/discoveries/exact_1", `{"state":"shortlisted","expected_revision":1}`, "transition:exact_1"},
		{"PUT", "/api/discoveries/exact_1/field-overrides", `{"contract_version":1,"expected_override_revision":0,"overrides":{"title":"Edited","company":null,"location":null}}`, "overrides:exact_1"},
		{"POST", "/api/discoveries/exact_1/confirm-content", `{"contract_version":1,"expected_revision":1}`, "confirm:exact_1"},
		{"POST", "/api/discoveries/exact_1/promote", `{"contract_version":1,"action":"save_job","expected_revision":1}`, "promote:exact_1"},
		{"POST", "/api/discoveries/exact_1/resolve-public-link", `{"contract_version":1,"expected_revision":1}`, "resolve_public:exact_1"},
		{"DELETE", "/api/discoveries/exact_1", `{"expected_revision":1}`, "delete:exact_1"},
		{"POST", "/api/discoveries/flush-archived", `{}`, "flush:"},
	} {
		response(t, h, vaultRequest(tc.method, tc.path, token, tc.body), 200)
		if s.calls[len(s.calls)-1] != "feed."+tc.operation {
			t.Fatal("route mismatch", tc, s.calls)
		}
	}
	before := len(s.calls)
	for _, body := range []string{`{}`, `{"state":"inbox","expected_revision":null}`, `{"state":"inbox","expected_revision":"1"}`, `{"state":"inbox","expected_revision":1.0}`, `{"state":"inbox","expected_revision":0}`, `{"state":"inbox","expected_revision":9223372036854775808}`, `{"state":"inbox","expected_revision":true}`, `{"state":"inbox","expected_revision":1,"path":"private"}`, `{"state":"\ud800","expected_revision":1}`} {
		response(t, h, vaultRequest("PUT", "/api/discoveries/exact_1", token, body), 400)
	}
	for _, path := range []string{"/api/discoveries/exact_1/promote", "/api/discoveries/a/b", "/api/discoveries/%2e%2e", "/api/discoveries/%2Fetc", "/api/%64iscoveries/exact_1", "/api/discoveries/exact_1/recovery"} {
		t.Run(path, func(t *testing.T) { response(t, h, vaultRequest("PUT", path, token, `{}`), 404) })
	}
	response(t, h, request("GET", "/api/discoveries?limit=101", token), 400)
	response(t, h, vaultRequest("PUT", "/api/discoveries/exact_1", "wrong", `{}`), 403)
	if len(s.calls) != before {
		t.Fatal("invalid requests reached owner")
	}
}

func TestPromotionClosedEnvelope(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, action := range []string{"save_job", "prepare_application", "record_as_applied"} {
		response(t, h, vaultRequest("POST", "/api/discoveries/exact/promote", token,
			`{"contract_version":1,"action":"`+action+`","expected_revision":1}`), 200)
	}
	before := len(spy.calls)
	for _, body := range []string{
		`{}`, `{"contract_version":1,"action":"save_job"}`,
		`{"contract_version":true,"action":"save_job","expected_revision":1}`,
		`{"contract_version":1,"action":"submit","expected_revision":1}`,
		`{"contract_version":1,"action":"save_job","expected_revision":null}`,
		`{"contract_version":1,"action":"save_job","expected_revision":0}`,
		`{"contract_version":1,"action":"save_job","expected_revision":1,"job_id":"injected"}`,
		`{"contract_version":1,"action":"save_job","action":"record_as_applied","expected_revision":1}`,
	} {
		response(t, h, vaultRequest("POST", "/api/discoveries/exact/promote", token, body), 400)
	}
	response(t, h, vaultRequest("POST", "/api/discoveries/exact/promote?x=1", token, `{}`), 400)
	response(t, h, vaultRequest("POST", "/api/discoveries/%65xact/promote", token, `{}`), 404)
	response(t, h, vaultRequest("POST", "/api/discoveries/exact/promote", "wrong", `{}`), 403)
	if len(spy.calls) != before {
		t.Fatal("invalid promotion reached owner")
	}
}

func TestPromotionDiagnosticFailurePreservesEffects(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Committed: true, CommitUnknown: true, Partial: true, RecoveryRequired: true,
		RunID: "20260922T123000.123456Z_1234abcd"}
	spy.err = &ApplicationError{Code: "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED"}
	out := response(t, h, vaultRequest("POST", "/api/discoveries/exact/promote", token,
		`{"contract_version":1,"action":"save_job","expected_revision":1}`), 503)
	state := out["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != true || state["partial"] != true || state["recovery_required"] != true || out["run_id"] != spy.out.RunID || out["retryable"] != false {
		t.Fatal("diagnostic failure lost prior effects or invited unsafe retry", out)
	}
}
func TestFeedUnknownCommitIsNotReportedAsRollback(t *testing.T) {
	h, token, s := applicationFixture(t)
	s.out = &ApplicationOutcome{CommitUnknown: true, RecoveryRequired: true, CleanupFailed: true}
	s.err = &ApplicationError{Code: "DISCOVERY_WRITE_FAILED"}
	out := response(t, h, vaultRequest("PUT", "/api/discoveries/exact_1", token, `{"state":"inbox","expected_revision":1}`), 500)
	state := out["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != nil || state["recovery_required"] != true || state["cleanup_failed"] != true {
		t.Fatal("lost unknown-commit facts", state)
	}
	s.out.Committed = true
	out = response(t, h, vaultRequest("POST", "/api/discoveries/exact_1/confirm-content", token, `{"contract_version":1,"expected_revision":1}`), 500)
	state = out["operation_state"].(map[string]any)
	if state["known"] != false || state["committed"] != true {
		t.Fatal("uncertain later cache write erased known confirmation", state)
	}
}

func TestFeedAdmissionRejectsWrongHostAndOriginBeforeOwner(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, path := range []string{"/api/discoveries", "/api/discoveries/exact_1"} {
		r := request("GET", path, token)
		r.Host = "outside.invalid:8766"
		response(t, h, r, 403)
		r = request("GET", path, token)
		r.Header.Set("Origin", "https://outside.invalid")
		response(t, h, r, 403)
	}
	if len(spy.calls) != 0 {
		t.Fatal("untrusted request reached owner", spy.calls)
	}
}
