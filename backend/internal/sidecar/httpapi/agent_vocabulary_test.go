package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const vocabularyPath = "/api/jobs/job-one/vocabulary/exact"
const vocabularyBody = `{"name":"NebulaDB","primary_group_id":"databases_storage","source_line":3,"confirmed_exact":true}`

func vocabularyFixture(t *testing.T) (*handler, string, *int) {
	t.Helper()
	h, token, _ := applicationFixture(t)
	native := h.(*handler)
	calls := 0
	native.application.ExactVocabulary = func(_ context.Context, r ExactVocabularyRequest) (*ApplicationOutcome, *ApplicationError) {
		calls++
		if r.JobID != "job-one" || r.Name != "NebulaDB" || r.PrimaryGroupID != "databases_storage" || !r.ConfirmedExact || r.SourceLine == nil || *r.SourceLine != 3 {
			t.Fatal("lost exact source intent")
		}
		return &ApplicationOutcome{Result: json.RawMessage(`{"result":"created","job_id":"job-one","job_reanalyzed":false}`), VocabularyStages: &VocabularyStages{Registry: &ApplicationOutcome{Committed: true}, Reanalysis: &ApplicationOutcome{CommitUnknown: true, RecoveryRequired: true, RunID: "20260926T120000.000000Z_1234abcd", Result: json.RawMessage(`{"private":"SECRET"}`)}}}, nil
	}
	return native, token, &calls
}

func TestExactVocabularyHTTPClosedAdmission(t *testing.T) {
	legacy, token, _ := applicationFixture(t)
	caps := response(t, legacy, request("GET", "/api/session", sessionTokenOf(legacy)), 200)["capabilities"].(map[string]any)
	if _, found := caps["exact_vocabulary"]; found {
		t.Fatal("unwired capability advertised")
	}
	response(t, legacy, materialPost(vocabularyPath, token, vocabularyBody), 404)
	h, token, calls := vocabularyFixture(t)
	caps = response(t, h, request("GET", "/api/session", sessionTokenOf(h)), 200)["capabilities"].(map[string]any)
	if caps["exact_vocabulary"] != float64(1) {
		t.Fatal("missing wired capability")
	}
	got := response(t, h, materialPost(vocabularyPath, token, vocabularyBody), 200)
	stages := got["stages"].(map[string]any)
	if stages["registry"].(map[string]any)["committed"] != true || stages["reanalysis"].(map[string]any)["known"] != false || stages["reanalysis"].(map[string]any)["run_id"] != "20260926T120000.000000Z_1234abcd" {
		t.Fatal("two owners conflated")
	}
	serialized, _ := json.Marshal(got)
	if strings.Contains(string(serialized), "SECRET") {
		t.Fatal("retained domain output leaked")
	}
	for _, body := range []string{
		`{}`, `null`, `[]`, `{"name":"NebulaDB"}`,
		strings.Replace(vocabularyBody, `"confirmed_exact":true`, `"confirmed_exact":false`, 1),
		strings.Replace(vocabularyBody, `"confirmed_exact":true`, `"confirmed_exact":"true"`, 1),
		strings.Replace(vocabularyBody, `"primary_group_id":"databases_storage"`, `"primary_group_id":null`, 1),
		strings.Replace(vocabularyBody, `"NebulaDB"`, `"\ud800"`, 1),
		strings.Replace(vocabularyBody, `"NebulaDB"`, `"`+strings.Repeat("x", 161)+`"`, 1),
		strings.Replace(vocabularyBody, `"source_line":3`, `"source_line":null`, 1),
		strings.Replace(vocabularyBody, `"source_line":3`, `"source_line":3.0`, 1),
		strings.Replace(vocabularyBody, `"source_line":3`, `"source_line":200001`, 1),
		strings.Replace(vocabularyBody, `"source_line":3`, `"source_line":-1`, 1),
		strings.Replace(vocabularyBody, `"name":`, `"name":"other","name":`, 1),
		strings.Replace(vocabularyBody, `"name":`, `"\u006eame":"other","name":`, 1),
		strings.Replace(vocabularyBody, `"name":`, `"force":true,"name":`, 1),
	} {
		response(t, h, materialPost(vocabularyPath, token, body), 400)
	}
	response(t, h, materialPost(vocabularyPath+"?force=1", token, vocabularyBody), 400)
	response(t, h, materialPost(vocabularyPath, token, strings.Repeat(" ", 4097)), 413)
	response(t, h, materialPost(vocabularyPath, "", vocabularyBody), 403)
	response(t, h, request("GET", vocabularyPath, token), 404)
	response(t, h, materialPost("/api/jobs/a%2Fb/vocabulary/exact", token, vocabularyBody), 404)
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.Host = "evil.test" },
		func(r *http.Request) { r.Header.Set("Origin", "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") },
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	} {
		r := materialPost(vocabularyPath, token, vocabularyBody)
		change(r)
		response(t, h, r, 403)
	}
	if *calls != 1 {
		t.Fatal("invalid request reached owner")
	}
	seed := append(bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)...)
	if err := h.configureExtensions([]string{pairedOrigin}, bytes.NewReader(seed)); err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{token, h.extensions.capture, h.extensions.helper} {
		r := materialPost(vocabularyPath, credential, vocabularyBody)
		r.Header.Set("Origin", pairedOrigin)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		response(t, h, r, 403)
	}
	if *calls != 1 {
		t.Fatal("paired extension reached vocabulary writer")
	}
}

func TestExactVocabularyHTTPFailureFacts(t *testing.T) {
	h, token, _ := vocabularyFixture(t)
	for _, scenario := range []string{"failure", "canceled", "panic"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.application.ExactVocabulary = func(context.Context, ExactVocabularyRequest) (*ApplicationOutcome, *ApplicationError) {
				if scenario == "panic" {
					panic("SECRET")
				}
				out := &ApplicationOutcome{Committed: true, VocabularyStages: &VocabularyStages{Registry: &ApplicationOutcome{Committed: true, CleanupFailed: true, Write: &ApplicationWriteState{Completed: true}}}}
				if scenario == "canceled" {
					cancel()
					return out, nil
				}
				return out, &ApplicationError{Code: "AGENT_VOCABULARY_REGISTRY_FAILED"}
			}
			status := 500
			if scenario == "canceled" {
				status = 408
			}
			got := response(t, h, materialPost(vocabularyPath, token, vocabularyBody).WithContext(ctx), status)
			if scenario == "panic" {
				if got["operation_state"].(map[string]any)["known"] != false {
					t.Fatal("panic invented certainty")
				}
			} else {
				registry := got["stages"].(map[string]any)["registry"].(map[string]any)
				if registry["committed"] != true || registry["cleanup_failed"] != true || registry["write"].(map[string]any)["completed"] != true {
					t.Fatal("late failure erased committed write")
				}
			}
		})
	}
}
