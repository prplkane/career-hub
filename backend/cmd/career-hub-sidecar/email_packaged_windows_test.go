//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

// Uses the actual pinned package and Windows secrets, but never opens a mailbox.
// No fixed legacy credential target or existing Home is read or changed.
func TestPackagedEmailCloneBackupReconnect(t *testing.T) {
	if os.Getenv("CAREERHUB_TEST_NATIVE_CREDENTIALS") != "1" {
		t.Skip("explicit synthetic native credential opt-in required")
	}
	executable, expected := os.Getenv("CAREERHUB_GO_SIDECAR"), os.Getenv("CAREERHUB_GO_SIDECAR_SHA256")
	resources := os.Getenv("CAREERHUB_GO_RESOURCES")
	if !filepath.IsAbs(executable) || !filepath.IsAbs(resources) || len(expected) != 64 {
		t.Fatal("absolute hash-pinned package required")
	}
	verifyBinary := func() {
		t.Helper()
		raw, err := os.ReadFile(executable)
		sum := sha256.Sum256(raw)
		if err != nil || hex.EncodeToString(sum[:]) != expected {
			t.Fatal("package hash changed")
		}
	}
	verifyBinary()
	defer verifyBinary()
	base, err := os.MkdirTemp("", "careerhub-packaged-email-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("synthetic email fixture: %s", base)
	source, clone := filepath.Join(base, "source"), filepath.Join(base, "clone")
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	marker, problem := datahome.MarkerBytes("dev", "dh_"+hex.EncodeToString(nonce[:]))
	if problem != nil {
		t.Fatal(problem.Code)
	}
	materialFixtureFile(t, filepath.Join(source, datahome.MarkerPath), marker)
	backend, err := secretstore.NewWindowsBackend()
	if err != nil {
		t.Fatal("native credential adapter unavailable")
	}
	const password = "abcdefghijklmnop" // Fictional; never used for mail login.
	owned := map[string]bool{}
	complete := false
	// Registered before process cleanups, so every child exits before keys.
	t.Cleanup(func() {
		clean := true
		for target := range owned {
			value, present, err := backend.Read(target)
			if err != nil || present && !bytes.Equal(value, []byte(password)) {
				clean = false
			} else if present {
				_, err = backend.Delete(target)
				clean = clean && err == nil
			}
			clear(value)
			after, found, err := backend.Read(target)
			clear(after)
			clean = clean && err == nil && !found
		}
		if !clean || !complete {
			t.Errorf("qualification incomplete; retain synthetic fixture %s; exact known-key cleanup confirmed=%t", base, clean)
			return
		}
		if !t.Failed() {
			if err := os.RemoveAll(base); err != nil {
				t.Error(err)
			}
		}
	})
	adopt := func(home string) string {
		t.Helper()
		// Only call after the real configured/reconnected receipt on our new
		// fixture. A DB pointer alone is never cleanup authority.
		db, err := sqlitestore.OpenReadOnly(context.Background(), filepath.Join(home, "applications", "career_hub.sqlite3"))
		if err != nil {
			t.Fatal("fixture row unavailable")
		}
		var name string
		err = db.QueryRow("SELECT secret_name FROM job_email_connections WHERE connection_id='primary'").Scan(&name)
		closeErr := db.Close()
		if err != nil || closeErr != nil || !strings.HasPrefix(name, "email.v3.") || len(name) != 75 {
			t.Fatal("fixture receipt has no scoped v3 target")
		}
		target := "CareerHub/dev/provider/" + name
		value, present, err := backend.Read(target)
		matches := err == nil && present && bytes.Equal(value, []byte(password))
		clear(value)
		if !matches || owned[target] {
			t.Fatal("new fixture receipt/key mismatch")
		}
		owned[target] = true
		return target
	}
	assertKey := func(target string, want bool) {
		t.Helper()
		value, found, err := backend.Read(target)
		matches := err == nil && found == want && (!found || bytes.Equal(value, []byte(password)))
		clear(value)
		if !matches {
			t.Fatal("exact fixture credential state mismatch")
		}
	}
	maintenance := func(home, command string, wantExit int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"command": command, "protocol_version": 1, "data_root": home,
			"resource_root": resources, "channel": "dev", "confirm_writers_stopped": true, "archive_relative_path": "backups/email.zip"})
		out, stderr, exit, err := runBoundedMigrationFixture(executable, []string{"--desktop-maintenance"}, resources, raw, 32768)
		var result map[string]any
		if err != nil || exit != wantExit || len(stderr) != 0 || bytes.Contains(out, []byte(password)) || json.Unmarshal(out, &result) != nil {
			t.Fatal("packaged email maintenance failed")
		}
		return result
	}
	call := func(client *materialWireClient, method, route string, body any, status int) map[string]any {
		t.Helper()
		raw, _ := client.call(t, method, route, body, status)
		var result map[string]any
		if bytes.Contains(raw, []byte(password)) || json.Unmarshal(raw, &result) != nil {
			t.Fatal("email response invalid or exposed fixture password")
		}
		return result
	}
	input := map[string]any{"contract_version": 2, "account_address": "jobs@example.test", "app_password": password,
		"sender_addresses": []string{"alerts@example.test"}, "application_sender_addresses": []string{},
		"schedule_enabled": false, "interval_minutes": 30, "expected_revision": 0}
	c, stop := startPackagedEmailFixture(t, executable, resources, source)
	result := call(c, "PUT", "/api/email-connection", input, 200)
	if result["credential_present"] != true || result["revision"] != float64(1) {
		t.Fatal("packaged configuration did not commit")
	}
	original := adopt(source)
	refused := maintenance(source, protocol.BackupCreateCommand, 4)
	if refused["success"] == true {
		t.Fatal("backup admitted a concurrent owned writer")
	}
	if _, err := os.Stat(filepath.Join(source, "backups", "email.zip")); !os.IsNotExist(err) {
		t.Fatal("refused backup published archive")
	}
	stop()
	if result := maintenance(source, protocol.BackupCreateCommand, 0); result["success"] != true {
		t.Fatal("stopped-writer backup failed")
	}
	if err := os.CopyFS(clone, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	c, stop = startPackagedEmailFixture(t, executable, resources, clone)
	input["expected_revision"] = 1
	refused = call(c, "PUT", "/api/email-connection", input, 409)
	if refused["error_code"] != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" {
		t.Fatal("copied credential was not isolated")
	}
	result = call(c, "POST", "/api/email-connection/reconnect", input, 200)
	if result["credential_present"] != true || result["revision"] != float64(2) {
		t.Fatal("explicit copied-Home reconnect failed")
	}
	copied := adopt(clone)
	assertKey(original, true)
	stop()
	c, stop = startPackagedEmailFixture(t, executable, resources, source)
	result = call(c, "GET", "/api/email-connection", nil, 200)
	if result["credential_present"] != true || result["revision"] != float64(1) {
		t.Fatal("copy reconnect changed original connection")
	}
	call(c, "POST", "/api/email-connection/disconnect", map[string]any{"contract_version": 2, "expected_revision": 1}, 200)
	assertKey(original, false)
	assertKey(copied, true)
	stop()
	if result := maintenance(source, protocol.BackupRestoreCommand, 0); result["success"] != true {
		t.Fatal("configured-Home restore failed")
	}
	c, stop = startPackagedEmailFixture(t, executable, resources, source)
	result = call(c, "GET", "/api/email-connection", nil, 200)
	if result["configured"] != true || result["credential_present"] != false || result["revision"] != float64(1) {
		t.Fatal("restore fabricated the removed OS credential")
	}
	stop()
	c, stop = startPackagedEmailFixture(t, executable, resources, clone)
	result = call(c, "POST", "/api/email-connection/disconnect", map[string]any{"contract_version": 2, "expected_revision": 2}, 200)
	if result["credential_present"] != false {
		t.Fatal("copy disconnect failed")
	}
	assertKey(copied, false)
	stop()
	complete = true
	t.Log("packaged clone/reconnect, writer refusal, backup/restore, restart and exact native-key cleanup passed; no mailbox access")
}

func startPackagedEmailFixture(t *testing.T, executable, resources, home string) (*materialWireClient, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	cmd := exec.CommandContext(ctx, executable, "--sidecar-bootstrap")
	cmd.Dir = resources
	cmd.WaitDelay = time.Second
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stderr := &migrationFixtureOutput{limit: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = input.Close()
		_, _ = io.Copy(io.Discard, output)
		err := cmd.Wait()
		cancel()
		if err != nil || stderr.overflow || len(stderr.data) != 0 {
			t.Error("packaged email child did not stop cleanly")
		}
	}
	t.Cleanup(stop)
	if err := protocol.WriteRecord(input, map[string]any{"command": "start", "protocol_version": 1, "port": 0,
		"channel": "dev", "resource_root": resources, "data_root": home, "session_token": testSessionToken}); err != nil {
		t.Fatal(err)
	}
	ready, err := protocol.ReadRecord(output)
	if err != nil || ready["event"] != "ready" {
		t.Fatal("packaged email Home startup failed")
	}
	port, ok := ready["port"].(json.Number)
	if !ok {
		t.Fatal("invalid readiness port")
	}
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	c := &materialWireClient{url: "http://127.0.0.1:" + string(port), client: &http.Client{Transport: transport, Timeout: 15 * time.Second}}
	c.token = testSessionToken
	raw, _ := c.call(t, "GET", "/api/session", nil, 200)
	var session struct{ Token string }
	if json.Unmarshal(raw, &session) != nil || session.Token != "" {
		t.Fatal("packaged session unavailable or disclosed a token")
	}
	return c, stop
}
