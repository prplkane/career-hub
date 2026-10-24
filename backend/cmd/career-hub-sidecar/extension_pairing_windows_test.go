//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
)

const pairingTestOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"

func TestExtensionPairingPersistsInTheDataHomeOwnedProcess(t *testing.T) {
	const childFlag, rootFlag = "CAREERHUB_TEST_PAIRING_CHILD", "CAREERHUB_TEST_PAIRING_ROOT"
	if os.Getenv(childFlag) == "1" {
		base := os.Getenv(rootFlag)
		start := protocol.StartRecord{DataRoot: filepath.Join(base, "data"), ResourceRoot: filepath.Join(base, "resources"), Channel: "dev", SessionToken: testSessionToken}
		w, err := readonly.OpenForProcess(start)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(start.DataRoot, filepath.FromSlash(extensionPairingPath))

		first := preparedApplication(w, start, pairingTestOrigin)
		h, err := first.Handler(8766)
		if err != nil {
			t.Fatal(err)
		}
		code := pairingCall(t, h, http.MethodPost, "/api/extension/pairing-code", testSessionToken, "", "", 200)["code"].(string)
		credential := pairingCall(t, h, http.MethodPost, "/api/extension/pair", "", pairingTestOrigin, `{"code":"`+code+`"}`, 200)["credential"].(string)
		raw, err := os.ReadFile(file)
		if err != nil || bytes.Contains(raw, []byte(credential)) || !bytes.Contains(raw, []byte("credential_sha256")) {
			t.Fatal("pairing file missing or holds the credential itself")
		}
		first.Release()
		if _, err := os.Stat(file); err != nil {
			t.Fatal("shutdown removed the pairing; the owner would have to pair again")
		}

		// A restarted sidecar honours the stored pairing.
		second := preparedApplication(w, start, pairingTestOrigin)
		h2, err := second.Handler(8766) // same port as the request Host below
		if err != nil {
			t.Fatal(err)
		}
		bootstrap := pairingRequest(http.MethodPost, "/api/session", "", pairingTestOrigin, "")
		bootstrap.Header.Set(httpapi.ExtensionCredentialHeader, credential)
		recorder := httptest.NewRecorder()
		h2.ServeHTTP(recorder, bootstrap)
		if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `"client":"extension"`) {
			t.Fatalf("stored pairing was not honoured after restart: %d %s", recorder.Code, recorder.Body.String())
		}
		pairingCall(t, h2, http.MethodDelete, "/api/extension/pairing", testSessionToken, "", "", 200)
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatal("revoke left the pairing file", err)
		}
		second.Release()
		return
	}

	base := t.TempDir()
	data, resources := filepath.Join(base, "data"), filepath.Join(base, "resources")
	fixture := map[string]string{
		filepath.Join(data, datahome.MarkerPath):              `{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`,
		filepath.Join(resources, "VERSION"):                   "1.2.3-dev.01+build.7\n",
		filepath.Join(resources, catalogstore.CoreGroupsPath): "- {id: databases_storage, name: Databases}\n",
		filepath.Join(resources, catalogstore.CoreSkillsPath): "[]\n",
		filepath.Join(data, catalogstore.CustomGroupsPath):    "[]\n",
		filepath.Join(data, catalogstore.CustomSkillsPath):    "[]\n",
	}
	for path, raw := range fixture {
		materialFixtureFile(t, path, []byte(raw))
	}
	if err := os.MkdirAll(filepath.Join(resources, "src/intake_ui_static"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExtensionPairingPersistsInTheDataHomeOwnedProcess$")
	child.Env = append(os.Environ(), childFlag+"=1", rootFlag+"="+base)
	child.WaitDelay = time.Second
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("owned pairing: %v %.1600s", err, output)
	}
}

func pairingRequest(method, target, token, origin, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Host = "127.0.0.1:8766"
	if token != "" {
		r.Header.Set(httpapi.CapabilityHeader, token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}
	return r
}

func pairingCall(t *testing.T, h http.Handler, method, target, token, origin, body string, status int) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, pairingRequest(method, target, token, origin, body))
	var value map[string]any
	if w.Code != status || json.Unmarshal(w.Body.Bytes(), &value) != nil {
		t.Fatalf("%s %s: %d %.300s", method, target, w.Code, w.Body.String())
	}
	return value
}
