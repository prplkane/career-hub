package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// connectionFile writes what the running sidecar publishes in the Data Home
// (ADR SEC-250); the CLI takes its scoped token only from this file.
func connectionFile(t *testing.T, server, token string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"contract_version": 1, "server": server, "token": token})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agent-connection.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
