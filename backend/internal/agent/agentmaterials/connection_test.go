package agentmaterials

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var connectionToken = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))

func writeConnection(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agent-connection.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConnectionFileSuppliesTheScopedToken(t *testing.T) {
	var sawToken string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawToken = r.Header.Get("X-Career-Hub-Token")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// No token in the response: the client must not need one.
		_, _ = io.WriteString(w, `{"client":"browser","capabilities":{"material_integrity":1}}`)
	}))
	defer s.Close()
	path := writeConnection(t, map[string]any{"contract_version": 1, "server": s.URL, "token": connectionToken})

	c, e := NewFromConnection(path, "")
	if e != nil {
		t.Fatal(e.Code)
	}
	token, e := c.session(context.Background())
	if e != nil || token != connectionToken || sawToken != connectionToken {
		t.Fatal("client did not authenticate with the connection token")
	}
	if _, e := NewFromConnection(path, s.URL); e != nil {
		t.Fatal("a matching explicit server was refused")
	}
}

func TestConnectionFileRefusesInvalidInput(t *testing.T) {
	good := "http://127.0.0.1:8766"
	cases := map[string]string{
		"missing":         filepath.Join(t.TempDir(), "absent.json"),
		"contract":        writeConnection(t, map[string]any{"contract_version": 2, "server": good, "token": connectionToken}),
		"short token":     writeConnection(t, map[string]any{"contract_version": 1, "server": good, "token": "abc"}),
		"non-loopback":    writeConnection(t, map[string]any{"contract_version": 1, "server": "http://example.test:8766", "token": connectionToken}),
		"server mismatch": writeConnection(t, map[string]any{"contract_version": 1, "server": "http://127.0.0.1:9999", "token": connectionToken}),
	}
	oversized := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(oversized, []byte(strings.Repeat(" ", maxConnectionFileBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	cases["oversized"] = oversized
	for name, path := range cases {
		explicit := ""
		if name == "server mismatch" {
			explicit = good
		}
		if _, e := NewFromConnection(path, explicit); e == nil {
			t.Errorf("%s connection file was accepted", name)
		}
	}
	if _, e := NewFromConnection("", ""); e == nil {
		t.Error("an empty path was accepted")
	}
}
