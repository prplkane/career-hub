//go:build windows

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/email/emaildiagnostics"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

func TestEmailConfigureRealHTTP(t *testing.T) {
	if os.Getenv("CAREERHUB_EMAIL_CONFIGURE_CHILD") == "1" {
		checkEmailConfigureRealHTTP(t, os.Getenv("CAREERHUB_EMAIL_CONFIGURE_ROOT"))
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmailConfigureRealHTTP$", "-test.v")
	child.Env = append(os.Environ(), "CAREERHUB_EMAIL_CONFIGURE_CHILD=1", "CAREERHUB_EMAIL_CONFIGURE_ROOT="+root)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("fictional connection HTTP child: %v %.2500s", err, output)
	}
}

func checkEmailConfigureRealHTTP(t *testing.T, root string) {
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
	backend := &emailHTTPSecrets{}
	start.SessionToken = testSessionToken
	prepared := preparedApplicationWithEmailBackend(w, start, func() (secretstore.Backend, error) { return backend, nil })
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
	call := func(method, path, token, body string) (int, map[string]any) {
		t.Helper()
		request, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			request.Header.Set(httpapi.CapabilityHeader, token)
		}
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, httpapi.MaxResponseBytes+1))
		if err != nil || response.Header.Get("Cache-Control") != "no-store" ||
			strings.Contains(string(raw), "abcdefghijklmnop") || strings.Contains(string(raw), "qrstuvwxyzabcdef") ||
			strings.Contains(string(raw), "jobs@example.test/provider/") {
			t.Fatalf("credential response leaked or changed headers: %v", err)
		}
		var result map[string]any
		if json.Unmarshal(raw, &result) != nil {
			t.Fatalf("invalid JSON response: %.300s", raw)
		}
		return response.StatusCode, result
	}
	status, session := call(http.MethodGet, "/api/session", testSessionToken, "")
	if status != 200 || session["capabilities"].(map[string]any)["email_connection"] != true {
		t.Fatal("composed Windows email UI is unavailable")
	}
	token := testSessionToken
	initial := `{"contract_version":2,"account_address":"jobs@example.test","app_password":"abcdefghijklmnop","sender_addresses":["alerts@example.test"],"application_sender_addresses":["tracking@example.test"],"schedule_enabled":false,"interval_minutes":30,"expected_revision":0}`
	status, result := call(http.MethodPut, "/api/email-connection", token, initial)
	if status != 200 || result["configured"] != true || result["credential_present"] != true || result["revision"] != float64(1) ||
		!emaildiagnostics.ValidRunID(result["run_id"].(string)) {
		t.Fatalf("initial native save/readback failed: %d %#v", status, result)
	}
	status, logs := call(http.MethodGet, "/api/logs?after=0", token, "")
	if status != 200 || len(logs["events"].([]any)) != 2 || logs["has_more"] != true ||
		logs["events"].([]any)[0].(map[string]any)["source"] != "gmail_connection" {
		t.Fatalf("connection run diagnostics were not visible: %d %#v", status, logs)
	}
	status, tail := call(http.MethodGet, "/api/logs?after="+logs["cursor"].(string), token, "")
	if status != 200 || len(tail["events"].([]any)) != 0 || tail["has_more"] != false {
		t.Fatalf("empty job stream did not end catch-up: %d %#v", status, tail)
	}
	edit := strings.Replace(strings.Replace(initial, `"app_password":"abcdefghijklmnop"`, `"app_password":null`, 1), `"expected_revision":0`, `"expected_revision":1`, 1)
	status, result = call(http.MethodPut, "/api/email-connection", token, edit)
	if status != 200 || result["revision"] != float64(2) || result["credential_present"] != true {
		t.Fatalf("no-password settings edit lost credential: %d %#v", status, result)
	}
	rotate := strings.Replace(strings.Replace(initial, `"app_password":"abcdefghijklmnop"`, `"app_password":"qrst uvwx yzab cdef"`, 1), `"expected_revision":0`, `"expected_revision":2`, 1)
	status, result = call(http.MethodPut, "/api/email-connection", token, rotate)
	if status != 200 || result["revision"] != float64(3) || result["credential_present"] != true {
		t.Fatalf("v2 rotation lost credential: %d %#v", status, result)
	}
	if len(backend.values) != 1 {
		t.Fatalf("rotation retained an old v2 target: %d", len(backend.values))
	}
	status, result = call(http.MethodPost, "/api/email-connection/disconnect", token, `{"contract_version":2,"expected_revision":2}`)
	if status != 409 || result["error_code"] != "EMAIL_CONNECTION_REVISION_CONFLICT" || len(backend.values) != 1 {
		t.Fatalf("stale disconnect changed the connection: %d %#v", status, result)
	}
	status, result = call(http.MethodPut, "/api/email-connection", token, initial)
	if status != 409 || result["error_code"] != "EMAIL_CONNECTION_REVISION_CONFLICT" {
		t.Fatalf("stale save was not refused: %d %#v", status, result)
	}
	store, err := secretstore.New("dev", backend)
	if err != nil || store.Set(emailcredentials.LegacyName, "SYNTHETIC-LEGACY") != nil {
		t.Fatal("fictional v1 target setup failed")
	}
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(data, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	var v2Target string
	if err := db.QueryRow(`SELECT secret_name FROM job_email_connections WHERE connection_id='primary'`).Scan(&v2Target); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, emailcredentials.LegacyName); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	v1Attempt := strings.Replace(rotate, `"expected_revision":2`, `"expected_revision":3`, 1)
	status, result = call(http.MethodPut, "/api/email-connection", token, v1Attempt)
	if status != 409 || result["error_code"] != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" {
		t.Fatalf("shared v1 target was not protected: %d %#v", status, result)
	}
	if value, found, err := store.Get(emailcredentials.LegacyName); err != nil || !found || value != "SYNTHETIC-LEGACY" {
		t.Fatalf("v1 target was changed after refusal: found=%v err=%v", found, err)
	}
	for _, operation := range []string{"disconnect", "forget"} {
		status, result = call(http.MethodPost, "/api/email-connection/"+operation, token, `{"contract_version":2,"expected_revision":3}`)
		if status != 409 || result["error_code"] != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" {
			t.Fatalf("shared v1 %s was not refused: %d %#v", operation, status, result)
		}
	}
	if value, found, err := store.Get(emailcredentials.LegacyName); err != nil || !found || value != "SYNTHETIC-LEGACY" {
		t.Fatalf("v1 target was changed after retirement refusals: found=%v err=%v", found, err)
	}
	db, err = sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(data, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, v2Target); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.Delete(emailcredentials.LegacyName); err != nil || !deleted {
		t.Fatalf("fictional v1 setup cleanup failed: deleted=%v err=%v", deleted, err)
	}
	status, result = call(http.MethodPost, "/api/email-connection/disconnect", token, `{"contract_version":2,"expected_revision":3}`)
	if status != 200 || result["configured"] != true || result["enabled"] != false || result["schedule_enabled"] != false ||
		result["credential_present"] != false || result["revision"] != float64(4) || len(backend.values) != 0 {
		t.Fatalf("native disconnect did not retain only disabled settings: %d %#v", status, result)
	}
	status, result = call(http.MethodPost, "/api/email-connection/forget", token, `{"contract_version":2,"expected_revision":3}`)
	if status != 409 || result["error_code"] != "EMAIL_CONNECTION_REVISION_CONFLICT" {
		t.Fatalf("stale forget changed the connection: %d %#v", status, result)
	}
	status, result = call(http.MethodPost, "/api/email-connection/forget", token, `{"contract_version":2,"expected_revision":4}`)
	if status != 200 || result["configured"] != false || result["credential_present"] != false ||
		result["revision"] != float64(0) || len(backend.values) != 0 {
		t.Fatalf("native forget did not remove only the email setup: %d %#v", status, result)
	}
	// Recreate an unbound synthetic row: ordinary save must not adopt it, while
	// explicit reconnect publishes a new local target and preserves the old key.
	status, result = call(http.MethodPut, "/api/email-connection", token, initial)
	if status != 200 || result["revision"] != float64(1) {
		t.Fatalf("reconnect setup: %d %#v", status, result)
	}
	db, err = sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(data, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE job_email_connections SET secret_name=? WHERE connection_id='primary'`, emailcredentials.LegacyName); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(emailcredentials.LegacyName, "SYNTHETIC-LEGACY"); err != nil {
		t.Fatal(err)
	}
	reconnect := strings.Replace(initial, `"expected_revision":0`, `"expected_revision":1`, 1)
	status, result = call(http.MethodPut, "/api/email-connection", token, reconnect)
	if status != 409 || result["error_code"] != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" {
		t.Fatalf("ordinary save adopted legacy: %d %#v", status, result)
	}
	status, result = call(http.MethodPost, "/api/email-connection/reconnect", token, strings.Replace(reconnect, `"app_password":"abcdefghijklmnop"`, `"app_password":null`, 1))
	if status != 400 {
		t.Fatal("reconnect accepted no password")
	}
	status, result = call(http.MethodPost, "/api/email-connection/reconnect", token, reconnect)
	if status != 200 || result["revision"] != float64(2) || result["credential_present"] != true || !emaildiagnostics.ValidRunID(result["run_id"].(string)) {
		t.Fatalf("explicit reconnect failed: %d %#v", status, result)
	}
	if value, found, err := store.Get(emailcredentials.LegacyName); err != nil || !found || value != "SYNTHETIC-LEGACY" {
		t.Fatal("reconnect changed original key")
	}
	status, result = call(http.MethodPost, "/api/email-connection/reconnect", token, strings.Replace(reconnect, `"expected_revision":1`, `"expected_revision":2`, 1))
	if status != 409 || result["error_code"] != "EMAIL_CREDENTIAL_RECONNECT_NOT_REQUIRED" {
		t.Fatalf("duplicate reconnect was not refused: %d %#v", status, result)
	}
	journal, err := os.ReadFile(filepath.Join(data, "career_vault", "logs", "native-email", "segment-000001.jsonl"))
	if err != nil || strings.Contains(string(journal), "abcdefghijklmnop") || strings.Contains(string(journal), "SYNTHETIC-LEGACY") || strings.Contains(string(journal), "jobs@example.test") {
		t.Fatalf("journal exposed a credential or account: %v", err)
	}
}
