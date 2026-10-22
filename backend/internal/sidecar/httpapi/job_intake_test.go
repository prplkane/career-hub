package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func intakeBody(source string) string {
	raw, _ := json.Marshal(map[string]any{
		"source_text": source, "source_name": "fictional.txt",
		"company": "Fictional Labs", "title": "Backend Engineer",
		"source_url": "https://example.test/jobs/1", "posting_id": "posting-1",
		"idempotency_key": "retry-key-001", "initial_status": "captured",
	})
	return string(raw)
}

func TestJobIntakeHTTPContractAndStatus(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"job":{"id":"fictional"},"duplicate":false,"run_id":"20260920T161500.000000Z_1234abcd"}`), Created: true}
	got := response(t, h, vaultRequest(http.MethodPost, "/api/jobs", token, intakeBody("Role: Synthetic Backend Engineer")), http.StatusCreated)
	if got["duplicate"] != false || spy.calls[len(spy.calls)-1] != "jobs.intake" || spy.jobIntake == nil ||
		spy.jobIntake.SourceText != "Role: Synthetic Backend Engineer" || spy.jobIntake.SourceName != "fictional.txt" ||
		spy.jobIntake.Company != "Fictional Labs" || spy.jobIntake.Title != "Backend Engineer" ||
		spy.jobIntake.SourceURL != "https://example.test/jobs/1" || spy.jobIntake.PostingID != "posting-1" ||
		spy.jobIntake.IdempotencyKey != "retry-key-001" || spy.jobIntake.InitialStatus != "captured" {
		t.Fatal("intake request changed", got, spy.jobIntake, spy.calls)
	}
	if strings.Contains(fmt.Sprint(*spy.jobIntake), "Synthetic") {
		t.Fatal("intake formatter exposed source")
	}
	spy.out = &ApplicationOutcome{Result: json.RawMessage(`{"job":{"id":"fictional"},"duplicate":true,"run_id":"20260920T161501.000000Z_1234abcd"}`)}
	if duplicate := response(t, h, vaultRequest(http.MethodPost, "/api/jobs", token,
		`{"source_text":"Role: Synthetic Backend Engineer","idempotency_key":"retry-key-001"}`), http.StatusOK); duplicate["duplicate"] != true || spy.jobIntake.InitialStatus != "captured" {
		t.Fatal("duplicate/default status response changed", duplicate, spy.jobIntake)
	}
}

func TestJobIntakeRefusesInvalidEnvelopeBeforeOwner(t *testing.T) {
	h, token, spy := applicationFixture(t)
	valid := intakeBody("Role: Synthetic Backend Engineer")
	before := len(spy.calls)
	wrong := vaultRequest(http.MethodPost, "/api/jobs", "wrong", valid)
	response(t, h, wrong, http.StatusForbidden)
	cross := vaultRequest(http.MethodPost, "/api/jobs", token, valid)
	cross.Header.Set("Origin", "https://outside.example")
	response(t, h, cross, http.StatusForbidden)
	response(t, h, vaultRequest(http.MethodPost, "/api/jobs?mode=private", token, valid), http.StatusBadRequest)
	cases := []struct {
		body string
		want int
	}{
		{`{"source_text":"Role","idempotency_key":"retry-key-001","path":"private"}`, 400},
		{`{"source_text":"Role","source_text":"Other","idempotency_key":"retry-key-001"}`, 400},
		{`{"source_text":"Role"}`, 400},
		{`{"source_text":"Role","idempotency_key":""}`, 400},
		{`{"source_text":true,"idempotency_key":"retry-key-001"}`, 400},
		{`{"source_text":"Role","idempotency_key":"retry-key-001","initial_status":"preparing"}`, 400},
		{`{"source_text":"   ","idempotency_key":"retry-key-001"}`, 422},
		{`{"source_text":"Role","source_name":"../private.txt","idempotency_key":"retry-key-001"}`, 400},
	}
	for _, tc := range cases {
		response(t, h, vaultRequest(http.MethodPost, "/api/jobs", token, tc.body), tc.want)
	}
	tooManyRunes := intakeBody(strings.Repeat("x", 500001))
	response(t, h, vaultRequest(http.MethodPost, "/api/jobs", token, tooManyRunes), http.StatusUnprocessableEntity)
	tooLarge := intakeBody(strings.Repeat("x", MaxMaterialRequestBytes))
	response(t, h, vaultRequest(http.MethodPost, "/api/jobs", token, tooLarge), http.StatusRequestEntityTooLarge)
	chunked := vaultRequest(http.MethodPost, "/api/jobs", token, valid)
	chunked.TransferEncoding = []string{"chunked"}
	chunked.ContentLength = -1
	response(t, h, chunked, http.StatusBadRequest)
	if len(spy.calls) != before {
		t.Fatal("refused intake reached owner", spy.calls[before:])
	}
}

func TestJobIntakeFailurePreservesOperationFacts(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{RunID: "20260920T161500.000000Z_1234abcd", Committed: true, CommitUnknown: true, RecoveryRequired: true, Partial: true}
	spy.err = &ApplicationError{Code: "JOB_INTAKE_RECOVERY_REQUIRED", Category: "job_intake"}
	got := response(t, h, vaultRequest(http.MethodPost, "/api/jobs", token, intakeBody("Role: Synthetic Backend Engineer")), http.StatusConflict)
	state := got["operation_state"].(map[string]any)
	if got["error_code"] != "JOB_INTAKE_RECOVERY_REQUIRED" || got["diagnostic_category"] != "job_intake" ||
		got["run_id"] != "20260920T161500.000000Z_1234abcd" || state["known"] != false ||
		state["committed"] != true || state["partial"] != true || state["recovery_required"] != true {
		t.Fatal("intake failure facts changed", got)
	}
}

func TestJobIntakeStorageFailureIsOperational(t *testing.T) {
	h, token, spy := applicationFixture(t)
	spy.out = &ApplicationOutcome{Partial: true}
	spy.err = &ApplicationError{Code: "JOB_INTAKE_STORAGE_UNAVAILABLE", Category: "job_intake"}
	got := response(t, h, vaultRequest(http.MethodPost, "/api/jobs", token, intakeBody("Synthetic role")), http.StatusServiceUnavailable)
	state := got["operation_state"].(map[string]any)
	if state["partial"] != true || state["committed"] != false || !strings.Contains(got["suggestion"].(string), "empty storage folders") {
		t.Fatal("namespace facts or recovery advice lost", got)
	}
}
