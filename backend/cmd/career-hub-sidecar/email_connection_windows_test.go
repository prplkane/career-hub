//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/email/emaildiagnostics"
	"careerhub.local/backend/internal/email/emailgate"
	"careerhub.local/backend/internal/email/emailworker"
	"careerhub.local/backend/internal/jobs/jobdiagnostics"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

type emailHTTPSecrets struct {
	values map[string][]byte
	reads  int
	fail   bool
	onRead func()
}

func (s *emailHTTPSecrets) Write(target string, secret []byte) error {
	if s.values == nil {
		s.values = make(map[string][]byte)
	}
	s.values[target] = append([]byte(nil), secret...)
	return nil
}
func (s *emailHTTPSecrets) Read(target string) ([]byte, bool, error) {
	s.reads++
	if s.onRead != nil {
		s.onRead()
	}
	if s.fail {
		return nil, false, errors.New("private synthetic secret failure")
	}
	value, found := s.values[target]
	return append([]byte(nil), value...), found, nil
}
func (s *emailHTTPSecrets) Delete(target string) (bool, error) {
	_, found := s.values[target]
	delete(s.values, target)
	return found, nil
}

func TestEmailConnectionRealHTTP(t *testing.T) {
	if os.Getenv("CAREERHUB_EMAIL_HTTP_CHILD") == "1" {
		checkEmailConnectionRealHTTP(t, os.Getenv("CAREERHUB_EMAIL_HTTP_ROOT"))
		return
	}
	root := filepath.Join(t.TempDir(), "fixture")
	data, resources := filepath.Join(root, "data"), filepath.Join(root, "resources")
	materialFixtureFile(t, filepath.Join(data, datahome.MarkerPath), []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`))
	materialFixtureFile(t, filepath.Join(resources, "VERSION"), []byte("1.2.3-dev.01+build.7\n"))
	materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreGroupsPath)), []byte("[]\n"))
	materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreSkillsPath)), []byte("- {id: core, name: Core, category: tool}\n"))
	materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(catalogstore.CustomGroupsPath)), []byte("[]\n"))
	materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(catalogstore.CustomSkillsPath)), []byte("[]\n"))
	if err := os.MkdirAll(filepath.Join(resources, "src", "intake_ui_static"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmailConnectionRealHTTP$", "-test.v")
	child.Env = append(os.Environ(), "CAREERHUB_EMAIL_HTTP_CHILD=1", "CAREERHUB_EMAIL_HTTP_ROOT="+root)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("synthetic email HTTP child failed: %v %.2500s", err, output)
	}
}

func checkEmailConnectionRealHTTP(t *testing.T, root string) {
	t.Helper()
	data, resources := filepath.Join(root, "data"), filepath.Join(root, "resources")
	start := protocol.StartRecord{DataRoot: data, ResourceRoot: resources, Channel: "dev"}
	w, err := readonly.OpenForProcess(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schema.PrepareOwned(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	home, err := w.StartupHome()
	if err != nil || home.DataHomeID != "dh_0123456789abcdef0123456789abcdef" || home.Channel != "dev" || home.FormatVersion != 2 {
		t.Fatalf("startup Home not pinned: %+v %v", home, err)
	}
	home.DataHomeID = "dh_00000000000000000000000000000000"
	second, err := w.StartupHome()
	if err != nil || second.DataHomeID == home.DataHomeID {
		t.Fatal("caller mutated startup Home baseline")
	}
	secrets := &emailHTTPSecrets{}
	syncCalls, syncFailureCode, syncResultStatus := 0, "", "succeeded"
	start.SessionToken = testSessionToken
	prepared := preparedApplicationWithEmailDependencies(w, start,
		func() (secretstore.Backend, error) { return secrets, nil },
		func(ctx context.Context, owner *applicationwrite.Service, backend secretstore.Backend, trigger string) (emailworker.Result, error) {
			syncCalls++
			if backend != secrets || trigger != "manual" {
				t.Fatal("sync did not receive the explicit backend and trigger")
			}
			config, err := owner.EmailSyncState().ConnectionConfig(ctx)
			if err != nil {
				return emailworker.Result{}, err
			}
			if config.Home.DataHomeID != second.DataHomeID || config.AccountAddress != "jobs@example.test" {
				t.Fatal("sync owner was not pinned to the admitted Home")
			}
			if syncFailureCode != "" {
				return emailworker.Result{}, &emailworker.Error{Code: syncFailureCode}
			}
			if syncResultStatus == "partial" {
				return emailworker.Result{ContractVersion: 2, Status: "partial", ErrorCode: "EMAIL_SYNC_MESSAGE_RETRY",
					Counts:            emailworker.Counts{MessagesRetryPending: 1},
					FailureCodeCounts: map[string]int{"EMAIL_IMPORT_DISCOVERY_FAILED": 1}}, nil
			}
			return emailworker.Result{ContractVersion: 2, Status: "succeeded", FailureCodeCounts: map[string]int{}, CompletedAt: "2026-09-13T12:00:00Z"}, nil
		})
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := prepared.Handler(port)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	defer server.Close()
	client := server.Client()
	privateV2Target := ""
	get := func(path, token string) (int, map[string]any) {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			request.Header.Set(httpapi.CapabilityHeader, token)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, httpapi.MaxResponseBytes+1))
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" ||
			strings.Contains(string(body), emailcredentials.LegacyName) || strings.Contains(string(body), "SYNTHETIC-SECRET") ||
			privateV2Target != "" && strings.Contains(string(body), privateV2Target) {
			t.Fatal("email HTTP response leaked a target/secret or changed cache policy")
		}
		var value map[string]any
		if err := json.Unmarshal(body, &value); err != nil {
			t.Fatalf("not one bounded JSON response: %v", err)
		}
		return response.StatusCode, value
	}
	diagnosticRunID := ""
	post := func(path, token, payload string) (int, map[string]any) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(httpapi.CapabilityHeader, token)
		request.Header.Set("Content-Type", "application/json")
		if diagnosticRunID != "" {
			request.Header.Set("X-Career-Hub-Diagnostic-Run", diagnosticRunID)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, httpapi.MaxResponseBytes+1))
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(body), "SYNTHETIC-SECRET") || strings.Contains(string(body), emailcredentials.LegacyName) {
			t.Fatal("email mutation response leaked private target or secret")
		}
		var value map[string]any
		if err := json.Unmarshal(body, &value); err != nil {
			t.Fatalf("email mutation returned invalid JSON: %v", err)
		}
		return response.StatusCode, value
	}
	status, session := get("/api/session", testSessionToken)
	if status != 200 || session["capabilities"].(map[string]any)["email_connection"] != true {
		t.Fatal("composed Windows email controls are unavailable")
	}
	token := testSessionToken
	legacyPath := filepath.Join(data, "career_vault", "logs", "intake.jsonl")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"run_id":"legacy","status":"success"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if status, got := get("/api/logs?after=0", ""); status != 403 || got["error_code"] == nil {
		t.Fatalf("unpaired diagnostics route was readable: %d %#v", status, got)
	}
	status, got := get("/api/logs?after=0", token)
	if status != 200 || len(got["events"].([]any)) != 1 || got["has_more"] != true {
		t.Fatalf("real HTTP did not page historical diagnostics first: %d %#v", status, got)
	}
	legacyCursor := got["cursor"].(string)
	status, got = post("/api/email-connection/reset-cursor", token, `{"contract_version":2,"expected_revision":1}`)
	if status != 400 || got["error_code"] != "EMAIL_CONNECTION_NOT_CONFIGURED" || got["operation_state"].(map[string]any)["committed"] != false {
		t.Fatalf("unconfigured mutation not refused safely: %d %#v", status, got)
	}
	_, file, _, _ := runtime.Caller(0)
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	raw, err := os.ReadFile(filepath.Join(repo, "tests", "fixtures", "migration_email_connection_snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Unconfigured struct {
			Want map[string]any `json:"want"`
		} `json:"unconfigured"`
		Configured struct {
			Connection   map[string]any   `json:"connection"`
			Sync         map[string]any   `json:"sync"`
			Rules        []map[string]any `json:"rules"`
			Receipts     []map[string]any `json:"receipts"`
			Discoveries  []map[string]any `json:"discoveries"`
			Observations []map[string]any `json:"observations"`
			Want         map[string]any   `json:"want"`
		} `json:"configured"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	status, got = get("/api/email-connection", token)
	if status != 200 || !reflect.DeepEqual(got, source.Unconfigured.Want) {
		t.Fatalf("unconfigured real HTTP differs from Python source: status=%d result=%#v", status, got)
	}
	store, err := secretstore.New("dev", secrets)
	if err != nil || store.Set(emailcredentials.LegacyName, "SYNTHETIC-SECRET") != nil {
		t.Fatal("fictional credential setup failed")
	}
	readsBeforeUnbound := secrets.reads
	status, got = get("/api/email-connection", token)
	if status != 200 || got["credential_present"] != false || got["configured"] != false || secrets.reads != readsBeforeUnbound {
		t.Fatalf("unconfigured snapshot probed legacy orphan: %d %#v", status, got)
	}
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(data, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO job_email_connections (
		connection_id, provider, account_address, folder, secret_name,
		enabled, schedule_enabled, interval_minutes, lookback_days,
		max_messages_per_sync, last_test_status, last_error_code, revision,
		created_at, updated_at
	) VALUES ('primary', 'gmail_imap', 'jobs@example.test', 'INBOX', ?,
		1, 0, 30, 14, 250, 'not_tested', '', 1, 't0', 't0')`, emailcredentials.LegacyName); err != nil {
		t.Fatal(err)
	}
	status, got = get("/api/email-connection", token)
	if status != 200 || got["credential_present"] != false || got["last_error_code"] != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" || secrets.reads != readsBeforeUnbound {
		t.Fatal("legacy row did not require reconnect without store access")
	}
	ownedRoot, scopeProblem := runtimepaths.Open(data)
	if scopeProblem != nil {
		t.Fatal(scopeProblem)
	}
	scope, scopeProblem := ownedRoot.CredentialScope()
	if scopeProblem != nil {
		t.Fatal(scopeProblem)
	}
	localTarget, err := emailcredentials.NewScopedGenerationName(second, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := ownedRoot.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(localTarget, "SYNTHETIC-SECRET"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, localTarget); err != nil {
		t.Fatal(err)
	}
	wantConfigured := source.Unconfigured.Want
	wantConfigured["configured"] = true
	wantConfigured["account_address"] = "jobs@example.test"
	wantConfigured["enabled"] = true
	wantConfigured["credential_present"] = true
	wantConfigured["revision"] = float64(1)
	status, got = get("/api/email-connection", token)
	if status != 200 || !reflect.DeepEqual(got, wantConfigured) {
		t.Fatalf("configured source-shaped real HTTP differs: status=%d result=%#v", status, got)
	}
	diagnosticRunID = "20260913T120000.123456Z_deadbeef"
	status, got = post("/api/email-connection/sync", token, `{"contract_version":2,"trigger":"manual"}`)
	if status != 200 || got["status"] != "succeeded" || !reflect.DeepEqual(got["connection"], wantConfigured) ||
		got["completed_at"] != "2026-09-13T12:00:00Z" || got["deferred_until"] != nil ||
		got["run_id"] != diagnosticRunID || syncCalls != 1 {
		t.Fatalf("fictional sync route lost source readback: %d %#v; calls=%d", status, got, syncCalls)
	}
	logStatus, logPage := get("/api/logs?after="+url.QueryEscape(legacyCursor), token)
	if logStatus != 200 || len(logPage["events"].([]any)) != 2 || logPage["has_more"] != true ||
		logPage["events"].([]any)[0].(map[string]any)["run_id"] != diagnosticRunID {
		t.Fatalf("real HTTP did not merge subsequent native run events: %d %#v", logStatus, logPage)
	}
	logStatus, emptyJobPage := get("/api/logs?after="+url.QueryEscape(logPage["cursor"].(string)), token)
	if logStatus != 200 || len(emptyJobPage["events"].([]any)) != 0 || emptyJobPage["has_more"] != false {
		t.Fatalf("empty job stream did not end catch-up: %d %#v", logStatus, emptyJobPage)
	}
	jobRun, jobErr := jobdiagnostics.New("fictional-job")
	if jobErr != nil {
		t.Fatal(jobErr)
	}
	jobStarted, jobErr := jobRun.Started()
	if jobErr != nil {
		t.Fatal(jobErr)
	}
	if appended, jobProblem := jobdiagnostics.Append(context.Background(), data, second, jobStarted); jobProblem != nil || !appended.Committed {
		t.Fatalf("fictional job diagnostic append failed: %+v %v", appended, jobProblem)
	}
	logStatus, jobPage := get("/api/logs?after="+url.QueryEscape(emptyJobPage["cursor"].(string)), token)
	if logStatus != 200 || len(jobPage["events"].([]any)) != 1 ||
		jobPage["events"].([]any)[0].(map[string]any)["source"] != "saved_job" {
		t.Fatalf("authenticated job diagnostic was not visible: %d %#v", logStatus, jobPage)
	}
	if jobPage["has_more"] == true {
		// The native-intake stream follows saved-job diagnostics, even when
		// this fixture has no intake events. Consume its explicit empty page.
		logStatus, tail := get("/api/logs?after="+url.QueryEscape(jobPage["cursor"].(string)), token)
		if logStatus != 200 || len(tail["events"].([]any)) != 0 || tail["has_more"] != false {
			t.Fatalf("empty intake diagnostic continuation: %d %#v", logStatus, tail)
		}
		jobPage["cursor"] = tail["cursor"]
	}
	jobSegment := filepath.Join(data, "career_vault", "logs", "native-jobs", "segment-000001.jsonl")
	jobBytes, jobErr := os.ReadFile(jobSegment)
	if jobErr != nil {
		t.Fatal(jobErr)
	}
	if jobErr := os.WriteFile(jobSegment, append(jobBytes, []byte("damaged tail")...), 0600); jobErr != nil {
		t.Fatal(jobErr)
	}
	logStatus, damagedJobPage := get("/api/logs?after="+url.QueryEscape(jobPage["cursor"].(string)), token)
	if logStatus != 409 || damagedJobPage["error_code"] != "JOB_DIAGNOSTIC_RECOVERY_REQUIRED" || damagedJobPage["diagnostic_category"] != "diagnostic_read" {
		t.Fatalf("corrupt job journal did not return a safe diagnostic error: %d %#v", logStatus, damagedJobPage)
	}
	diagnosticRunID = ""
	runIDs := []string{got["run_id"].(string)}
	syncResultStatus = "partial"
	status, got = post("/api/email-connection/sync", token, `{"contract_version":2,"trigger":"manual"}`)
	if status != 200 || got["status"] != "partial" || got["error_code"] != "EMAIL_SYNC_MESSAGE_RETRY" ||
		got["counts"].(map[string]any)["messages_retry_pending"] != float64(1) || syncCalls != 2 {
		t.Fatalf("partial sync did not retain worker facts: %d %#v; calls=%d", status, got, syncCalls)
	}
	runIDs = append(runIDs, got["run_id"].(string))
	syncResultStatus = "succeeded"
	secrets.fail = true
	status, got = post("/api/email-connection/sync", token, `{"contract_version":2,"trigger":"manual"}`)
	if status != 503 || got["error_code"] != "EMAIL_SECRET_STORE_FAILED" ||
		got["operation_state"].(map[string]any)["committed"] != true ||
		got["operation_state"].(map[string]any)["revision_refresh_required"] != true ||
		!emaildiagnostics.ValidRunID(got["run_id"].(string)) {
		t.Fatalf("post-sync readback failure lost completed effects: %d %#v", status, got)
	}
	runIDs = append(runIDs, got["run_id"].(string))
	secrets.fail = false
	syncFailureCode = "EMAIL_SYNC_NETWORK_FAILED"
	status, got = post("/api/email-connection/sync", token, `{"contract_version":2,"trigger":"manual"}`)
	if status != 400 || got["error_code"] != syncFailureCode || got["operation_state"].(map[string]any)["known"] != false || syncCalls != 4 {
		t.Fatalf("fictional sync failure was not conservative: %d %#v; calls=%d", status, got, syncCalls)
	}
	runIDs = append(runIDs, got["run_id"].(string))
	syncFailureCode = "private synthetic password"
	status, got = post("/api/email-connection/sync", token, `{"contract_version":2,"trigger":"manual"}`)
	if status != 500 || got["error_code"] != "EMAIL_SYNC_FAILED" || strings.Contains(strings.ToLower(got["error"].(string)), "private") {
		t.Fatalf("sync leaked an unexpected host exception: %d %#v", status, got)
	}
	runIDs = append(runIDs, got["run_id"].(string))
	journal, err := os.ReadFile(filepath.Join(data, "career_vault", "logs", "native-email", "segment-000001.jsonl"))
	if err != nil || strings.Contains(string(journal), "private synthetic password") ||
		strings.Contains(string(journal), "SYNTHETIC-SECRET") || strings.Contains(string(journal), "jobs@example.test") {
		t.Fatalf("native email journal was unavailable or held private content: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(journal), "\n"), "\n")
	if len(lines) != 2*len(runIDs) {
		t.Fatalf("native email journal lost a terminal event: %d lines for %d runs", len(lines), len(runIDs))
	}
	wantTerminal := []string{"completed", "completed", "failed", "failed", "failed"}
	wantCode := []string{"", "EMAIL_SYNC_MESSAGE_RETRY", "EMAIL_SECRET_STORE_FAILED", "EMAIL_SYNC_NETWORK_FAILED", "EMAIL_SYNC_FAILED"}
	for index, runID := range runIDs {
		if !emaildiagnostics.ValidRunID(runID) {
			t.Fatal("HTTP response lost a safe run ID")
		}
		for step := 0; step < 2; step++ {
			var fields map[string]any
			if json.Unmarshal([]byte(lines[2*index+step]), &fields) != nil ||
				fields["run_id"] != runID || fields["source"] != "gmail_sync" ||
				fields["stage"] != "email_sync_run" {
				t.Fatal("native diagnostic event changed shape or correlation", fields)
			}
			if step == 0 && fields["event"] != "started" || step == 1 && fields["event"] != wantTerminal[index] {
				t.Fatal("native diagnostic event ordering changed", fields)
			}
			if step == 1 && index == 1 && fields["status"] != "success_with_warnings" {
				t.Fatal("partial run was logged as a clean success", fields)
			}
			if step == 1 && wantCode[index] != "" &&
				fields["details"].(map[string]any)["error_code"] != wantCode[index] {
				t.Fatal("native failure code diverged from HTTP", fields)
			}
		}
	}
	syncFailureCode = ""
	if _, failed := readEmailConnection(context.Background(), w, data,
		func() (secretstore.Backend, error) { return nil, errors.New("private initialization detail") }); failed == nil || failed.Code != "EMAIL_SECRET_STORE_FAILED" {
		t.Fatalf("unavailable credential backend did not fail closed: %v", failed)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	initialized := false
	if _, failed := readEmailConnection(canceled, w, data,
		func() (secretstore.Backend, error) { initialized = true; return secrets, nil }); failed == nil || failed.Code != "EMAIL_CONNECTION_CANCELED" || initialized {
		t.Fatalf("canceled read initialized a backend: %v initialized=%v", failed, initialized)
	}
	v2Target, err := emailcredentials.NewGenerationName(second)
	if err != nil {
		t.Fatal(err)
	}
	privateV2Target = v2Target
	if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, v2Target); err != nil {
		t.Fatal(err)
	}
	readsBeforeV2 := secrets.reads
	status, got = get("/api/email-connection", token)
	// Unbound v2, present or missing, never authorizes a store probe or fallback.
	if status != 200 || got["credential_present"] != false || got["last_error_code"] != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" || secrets.reads != readsBeforeV2 {
		t.Fatalf("missing v2 target fell back to legacy: %d %#v", status, got)
	}
	if err := store.Set(v2Target, "SYNTHETIC-V2"); err != nil {
		t.Fatal(err)
	}
	status, got = get("/api/email-connection", token)
	if status != 200 || got["credential_present"] != false || got["last_error_code"] != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" || secrets.reads != readsBeforeV2 {
		t.Fatalf("unbound v2 credential was probed: %d %#v", status, got)
	}
	if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, emailcredentials.LegacyName); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		version int
		code    string
		status  int
	}{{schema.Version - 1, "EMAIL_CONNECTION_SCHEMA_PREPARATION_REQUIRED", 503}, {schema.Version + 1, "EMAIL_CONNECTION_SCHEMA_TOO_NEW", 409}} {
		if _, err := db.Exec("PRAGMA user_version=" + strconv.Itoa(tc.version)); err != nil {
			t.Fatal(err)
		}
		status, got = get("/api/email-connection", token)
		if status != tc.status || got["error_code"] != tc.code || got["configured"] != nil {
			t.Fatalf("schema %d returned partial/incorrect result: %d %#v", tc.version, status, got)
		}
	}
	if _, err := db.Exec("PRAGMA user_version=" + strconv.Itoa(schema.Version)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE job_email_connections SET secret_name='foreign.private.target' WHERE connection_id='primary'`); err != nil {
		t.Fatal(err)
	}
	readsBeforeInvalid := secrets.reads
	status, got = get("/api/email-connection", token)
	if status != 409 || got["error_code"] != "EMAIL_CONNECTION_INVALID" || secrets.reads != readsBeforeInvalid {
		t.Fatalf("foreign credential target was not refused: %d %#v", status, got)
	}
	if _, err := db.Exec(`UPDATE job_email_connections SET secret_name='email.v2.not_valid' WHERE connection_id='primary'`); err != nil {
		t.Fatal(err)
	}
	status, got = get("/api/email-connection", token)
	if status != 409 || got["error_code"] != "EMAIL_CONNECTION_INVALID" || secrets.reads != readsBeforeInvalid {
		t.Fatalf("malformed credential target reached backend: %d %#v", status, got)
	}
	if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, localTarget); err != nil {
		t.Fatal(err)
	}
	secrets.fail = true
	status, got = get("/api/email-connection", token)
	if status != 503 || got["error_code"] != "EMAIL_SECRET_STORE_FAILED" || strings.Contains(strings.ToLower(got["error"].(string)), "private") {
		t.Fatalf("backend failure leaked/changed route: %d %#v", status, got)
	}
	secrets.fail = false
	recoveryPath := filepath.Join(data, datahome.EmailCredentialRecoveryPath)
	var mutationErr error
	secrets.onRead = func() {
		secrets.onRead = nil
		mutationErr = os.MkdirAll(filepath.Dir(recoveryPath), 0700)
		if mutationErr == nil {
			mutationErr = os.WriteFile(recoveryPath, []byte("fictional during-read recovery"), 0600)
		}
	}
	status, got = get("/api/email-connection", token)
	if mutationErr != nil || status != 503 || got["error_code"] != "WORKSPACE_UNAVAILABLE" || got["configured"] != nil {
		t.Fatalf("recovery appearing during read returned partial success: status=%d result=%#v write=%v", status, got, mutationErr)
	}
	if err := os.Remove(recoveryPath); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(data, datahome.MarkerPath)
	original, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	readBefore := secrets.reads
	materialFixtureFile(t, markerPath, []byte(strings.Replace(string(original), "dh_0123456789abcdef0123456789abcdef", "dh_00000000000000000000000000000000", 1)))
	status, got = get("/api/email-connection", token)
	if status != 503 || got["error_code"] != "WORKSPACE_UNAVAILABLE" || got["diagnostic_category"] != "email_connection_read" || secrets.reads != readBefore {
		t.Fatalf("changed startup Home reached backend: %d %#v", status, got)
	}
	materialFixtureFile(t, markerPath, original)
	materialFixtureFile(t, recoveryPath, []byte("synthetic recovery sentinel"))
	status, got = get("/api/email-connection", token)
	if status != 503 || got["error_code"] != "WORKSPACE_UNAVAILABLE" || got["diagnostic_category"] != "email_connection_read" || secrets.reads != readBefore {
		t.Fatalf("pending recovery reached backend: %d %#v", status, got)
	}
	if err := os.Remove(recoveryPath); err != nil {
		t.Fatal(err)
	}
	release, err := emailgate.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/email-connection", nil).WithContext(short)
	request.Host = "127.0.0.1:" + portText
	request.Header.Set(httpapi.CapabilityHeader, token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	release()
	var blocked map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &blocked); err != nil || recorder.Code != 408 || blocked["error_code"] != "EMAIL_CONNECTION_CANCELED" {
		t.Fatalf("occupied email gate not canceled: status=%d body=%s err=%v", recorder.Code, recorder.Body.String(), err)
	}
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM job_email_connections WHERE connection_id='primary'`).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("read route changed connection revision: %d %v", revision, err)
	}
	// Build the complete fictional source case in the actual schema, then
	// compare the full HTTP JSON envelope—not only selected settings fields.
	field := func(row map[string]any, name string, fallback any) any {
		if value, ok := row[name]; ok {
			return value
		}
		return fallback
	}
	c := source.Configured.Connection
	if _, err := db.Exec(`UPDATE job_email_connections SET interval_minutes=?,
		lookback_days=?, max_messages_per_sync=?, last_tested_at=?,
		last_test_status=?, revision=? WHERE connection_id='primary'`,
		c["interval_minutes"], c["lookback_days"], c["max_messages_per_sync"],
		c["last_tested_at"], c["last_test_status"], c["revision"]); err != nil {
		t.Fatal(err)
	}
	s := source.Configured.Sync
	if _, err := db.Exec(`INSERT INTO job_email_sync_state (
		connection_id, folder, uidvalidity, last_uid, last_started_at,
		last_completed_at, last_result, consecutive_failures, next_retry_at,
		last_error_code, revision
	) VALUES ('primary', 'INBOX', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s["uidvalidity"], s["last_uid"], s["last_started_at"],
		s["last_completed_at"], s["last_result"], s["consecutive_failures"],
		s["next_retry_at"], s["last_error_code"], s["revision"]); err != nil {
		t.Fatal(err)
	}
	for _, rule := range source.Configured.Rules {
		if _, err := db.Exec(`INSERT INTO job_email_sender_rules (
			connection_id, sender_address, disposition, purpose,
			subject_policy_json, created_at, updated_at
		) VALUES ('primary', ?, ?, ?, ?, 't0', 't0')`,
			rule["sender_address"], rule["disposition"], rule["purpose"], rule["subject_policy_json"]); err != nil {
			t.Fatal(err)
		}
	}
	for i, receipt := range source.Configured.Receipts {
		if _, err := db.Exec(`INSERT INTO job_email_message_receipts (
			connection_id, folder, uidvalidity, message_uid, sender_address,
			status, error_code, message_id_hash, message_sent_date,
			first_seen_at, updated_at, subject_job_signal,
			subject_application_signal, subject_service_signal, subject_route
		) VALUES ('primary', 'INBOX', '314', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			i+1, receipt["sender_address"], receipt["status"],
			field(receipt, "error_code", ""), field(receipt, "message_id_hash", ""),
			receipt["message_sent_date"], receipt["first_seen_at"], receipt["updated_at"],
			field(receipt, "subject_job_signal", 0), field(receipt, "subject_application_signal", 0),
			field(receipt, "subject_service_signal", 0), field(receipt, "subject_route", "")); err != nil {
			t.Fatal(err)
		}
	}
	for _, discovery := range source.Configured.Discoveries {
		if _, err := db.Exec(`INSERT INTO job_discoveries (
			discovery_id, state, title, company, location, observed_url,
			canonical_url, content_scope, first_seen_at, last_seen_at,
			updated_at, revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'link_only', 't0', 't0', 't0', 1)`,
			discovery["discovery_id"], discovery["state"], discovery["title"],
			discovery["company"], discovery["location"], discovery["source_url"], discovery["source_url"]); err != nil {
			t.Fatal(err)
		}
	}
	for i, observation := range source.Configured.Observations {
		if _, err := db.Exec(`INSERT INTO job_discovery_observations (
			discovery_id, source_kind, source_channel, request_fingerprint,
			request_payload_sha256, observed_at, recorded_at, metadata_json
		) VALUES (?, ?, 'email', ?, '', 't0', 't0', ?)`,
			observation["discovery_id"], observation["source_kind"],
			"fictional-"+strconv.Itoa(i), observation["metadata_json"]); err != nil {
			t.Fatal(err)
		}
	}
	status, got = get("/api/email-connection", token)
	if status != 200 || !reflect.DeepEqual(got, source.Configured.Want) {
		t.Fatalf("full configured HTTP JSON differs from Python source: status=%d result=%#v", status, got)
	}
	status, got = post("/api/email-connection/senders", token, `{"contract_version":2,"sender_address":"new@example.test","disposition":"allowed","purpose":"application_tracking","expected_revision":2}`)
	if status != 409 || got["error_code"] != "EMAIL_CONNECTION_REVISION_CONFLICT" || got["operation_state"].(map[string]any)["committed"] != false {
		t.Fatalf("stale sender revision changed data: %d %#v", status, got)
	}
	status, got = post("/api/email-connection/senders", token, `{"contract_version":2,"sender_address":"new@example.test","disposition":"allowed","purpose":"application_tracking","expected_revision":3}`)
	if status != 200 || got["revision"] != float64(4) || got["configured"] != true {
		t.Fatalf("sender edit failed to return current source snapshot: %d %#v", status, got)
	}
	newRule := false
	for _, item := range got["sender_rules"].([]any) {
		rule := item.(map[string]any)
		if rule["sender_address"] == "new@example.test" && rule["purpose"] == "application_tracking" && rule["disposition"] == "allowed" {
			newRule = true
		}
	}
	if !newRule {
		t.Fatal("sender rule absent from post-write snapshot")
	}
	status, got = post("/api/email-connection/reset-cursor", token, `{"contract_version":2,"expected_revision":4}`)
	if status != 200 || got["revision"] != float64(5) || got["sync"].(map[string]any)["last_uid"] != float64(0) || got["receipts"].(map[string]any)["imported"] != float64(1) || got["receipts"].(map[string]any)["filtered_subject"] != float64(0) {
		t.Fatalf("cursor reset lost source receipt policy or readback: %d %#v", status, got)
	}
	secrets.fail = true
	status, got = post("/api/email-connection/reset-cursor", token, `{"contract_version":2,"expected_revision":5}`)
	if status != 503 || got["error_code"] != "EMAIL_SECRET_STORE_FAILED" || got["operation_state"].(map[string]any)["committed"] != true || got["operation_state"].(map[string]any)["revision_refresh_required"] != true {
		t.Fatalf("post-commit credential read failure hid write: %d %#v", status, got)
	}
	secrets.fail = false
	if err := db.QueryRow(`SELECT revision FROM job_email_connections WHERE connection_id='primary'`).Scan(&revision); err != nil || revision != 6 {
		t.Fatalf("post-commit failure did not preserve revision: %d %v", revision, err)
	}
	for _, row := range []struct{ id, fingerprint, title, observed, resolution string }{
		{"email_outcome_unlinked", "sha256:" + strings.Repeat("a", 64), "Unlinked synthetic role", "2026-08-15T10:00:00Z", "unmatched"},
		{"email_outcome_current", "sha256:" + strings.Repeat("b", 64), "Current synthetic role", "2026-08-17T10:00:00Z", "application_already_current"},
	} {
		if _, err := db.Exec(`INSERT INTO job_email_application_outcomes (
			outcome_id,message_fingerprint,source_provider,posting_identity,source_url,
			title,company,location,application_status,evidence_code,observed_at,
			resolution,revision,created_at,updated_at
		) VALUES (?,?, 'linkedin','linkedin:123','https://example.test/job',
			?,'Synthetic Co','Remote','applied','provider_confirmed',?,?,1,'t0','t0')`,
			row.id, row.fingerprint, row.title, row.observed, row.resolution); err != nil {
			t.Fatal(err)
		}
	}
	secretsBeforeUpdates := secrets.reads
	status, got = get("/api/application-email-updates?limit=1", token)
	if status != 200 || got["total"] != float64(2) || got["unlinked"] != float64(1) ||
		got["next_cursor"] != "offset:1" || got["items"].([]any)[0].(map[string]any)["title"] != "Unlinked synthetic role" {
		t.Fatalf("real HTTP email update first page differs: %d %#v", status, got)
	}
	status, got = get("/api/application-email-updates?limit=1&cursor=offset%3A1", token)
	if status != 200 || got["next_cursor"] != nil || got["items"].([]any)[0].(map[string]any)["title"] != "Current synthetic role" || secrets.reads != secretsBeforeUpdates {
		t.Fatalf("real HTTP email update page read touched credential backend or changed order: %d %#v", status, got)
	}
	status, got = get("/api/application-email-updates?cursor=offset%3A2", token)
	if status != 200 || got["total"] != float64(2) || got["next_cursor"] != nil || len(got["items"].([]any)) != 0 || secrets.reads != secretsBeforeUpdates {
		t.Fatalf("real HTTP exhausted email update page was not empty and credential-free: %d %#v", status, got)
	}
	const linkedJob = "synthetic-email-linked-job"
	const linkOutcome = "email_outcome_cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, err := db.Exec(`INSERT INTO job_applications
		(job_id,company,title,source_url,content_fingerprint,current_status,created_at,updated_at)
		VALUES(?,'Synthetic Co','Engineer','https://example.test/job',?,'captured','t0','t0')`,
		linkedJob, strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO application_status_events(job_id,status,occurred_at,recorded_at,note)
		VALUES(?,'captured','t0','t0','')`, linkedJob); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO job_email_application_outcomes (
		outcome_id,message_fingerprint,source_provider,posting_identity,source_url,
		title,company,location,application_status,evidence_code,observed_at,
		resolution,revision,created_at,updated_at
	) VALUES (?,?,'handshake','','','Engineer','Synthetic Co','','applied',
		'handshake_application_sent','2026-08-18T18:45:00Z','unmatched',1,'t0','t0')`,
		linkOutcome, "sha256:"+strings.Repeat("e", 64)); err != nil {
		t.Fatal(err)
	}
	linkBody := `{"contract_version":1,"outcome_id":"` + linkOutcome + `","job_id":"` + linkedJob + `","expected_revision":1}`
	status, got = post("/api/application-email-updates/link", token, linkBody)
	update, updateOK := got["update"].(map[string]any)
	application, applicationOK := got["application"].(map[string]any)
	if status != 200 || !updateOK || !applicationOK || update["resolution"] != "application_updated" ||
		update["revision"] != float64(2) || application["current_status"] != "applied" || secrets.reads != secretsBeforeUpdates {
		t.Fatalf("real HTTP link did not return one committed application: %d %#v", status, got)
	}
	status, got = post("/api/application-email-updates/link", token, linkBody)
	if status != 409 || got["error_code"] != "EMAIL_APPLICATION_UPDATE_REVISION_CONFLICT" {
		t.Fatalf("stale real HTTP email link was not refused: %d %#v", status, got)
	}
	var appliedEvents int
	if err := db.QueryRow(`SELECT COUNT(*) FROM application_status_events WHERE job_id=? AND status='applied'`, linkedJob).Scan(&appliedEvents); err != nil || appliedEvents != 1 {
		t.Fatalf("email link retry duplicated application status: %d %v", appliedEvents, err)
	}
}
