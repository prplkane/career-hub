//go:build windows

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
)

func TestAgentConnectionFileLifecycleOwnedProcess(t *testing.T) {
	const childFlag, rootFlag = "CAREERHUB_TEST_AGENT_SESSION_CHILD", "CAREERHUB_TEST_AGENT_SESSION_ROOT"
	if os.Getenv(childFlag) == "1" {
		base := os.Getenv(rootFlag)
		start := protocol.StartRecord{DataRoot: filepath.Join(base, "data"), ResourceRoot: filepath.Join(base, "resources"), Channel: "dev"}
		w, err := readonly.OpenForProcess(start)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(start.DataRoot, filepath.FromSlash(agentConnectionPath))

		first := preparedApplication(w, start)
		h, err := first.Handler(8766)
		if err != nil {
			t.Fatal(err)
		}
		agent, ok := httpapi.AgentToken(h)
		if !ok {
			t.Fatal("application handler has no agent token")
		}
		connection := readAgentConnection(t, file)
		if connection.ContractVersion != agentConnectionContract || connection.Server != "http://127.0.0.1:8766" || connection.Token != agent {
			t.Fatalf("connection file does not describe this process: %+v", connection.Server)
		}

		// The published token is admitted on an agent route and refused elsewhere.
		inScope := httptest.NewRecorder()
		h.ServeHTTP(inScope, agentRequest(http.MethodPost, "/api/agent/analyze", agent))
		if inScope.Code == http.StatusForbidden {
			t.Fatal("published agent token was refused on an agent route")
		}
		outOfScope := httptest.NewRecorder()
		h.ServeHTTP(outOfScope, agentRequest(http.MethodGet, "/api/vault/profile", agent))
		if outOfScope.Code != http.StatusForbidden {
			t.Fatalf("published agent token reached a vault route: %d", outOfScope.Code)
		}

		// A later start (as after a crash) replaces the stale file and token.
		second := preparedApplication(w, start)
		h2, err := second.Handler(8767)
		if err != nil {
			t.Fatal(err)
		}
		agent2, _ := httpapi.AgentToken(h2)
		replaced := readAgentConnection(t, file)
		if agent2 == agent || replaced.Token != agent2 || replaced.Server != "http://127.0.0.1:8767" {
			t.Fatal("restart did not replace the connection file")
		}

		// The superseded process must not delete the newer file; the owner does.
		first.Release()
		if _, err := os.Stat(file); err != nil {
			t.Fatal("a superseded release removed the current connection file")
		}
		second.Release()
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatal("release left the connection file behind", err)
		}
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
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAgentConnectionFileLifecycleOwnedProcess$")
	child.Env = append(os.Environ(), childFlag+"=1", rootFlag+"="+base)
	child.WaitDelay = time.Second
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("owned agent session: %v %.1600s", err, output)
	}
}

func readAgentConnection(t *testing.T, file string) agentConnection {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("connection file missing", err)
	}
	var connection agentConnection
	if json.Unmarshal(raw, &connection) != nil {
		t.Fatal("connection file is not JSON")
	}
	return connection
}

func agentRequest(method, target, token string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.Host = "127.0.0.1:8766"
	r.Header.Set(httpapi.CapabilityHeader, token)
	return r
}
